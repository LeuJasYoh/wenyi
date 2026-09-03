package review

// review_loop.go 取证式 Agent Loop 与冲突仲裁（主规格 §12.6 + 分册 05 §12.6）。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

const (
	evidenceBatchLimit  = 128 * 1024 // _ActionLoop 累计批大小上限
	arbitrationPayloadLimit = 96 * 1024 // 仲裁 payload 上限
	maxArbitrationGroups    = 32
)

// ---- 纯函数：normalize / build / apply ----

// NormalizeReviewIssues 确定性清洗 + consistency 解析 + 轮内 issue_id。
// 排序键 (chapter, index, _chunk_id, type)；issue_key 跨轮稳定去重。
func NormalizeReviewIssues(issues []map[string]any, evidence *BookEvidenceIndex) []map[string]any {
	sorted := make([]map[string]any, len(issues))
	copy(sorted, issues)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, _ := StrictInt(sorted[i]["chapter"])
		cj, _ := StrictInt(sorted[j]["chapter"])
		if ci != cj {
			return ci < cj
		}
		ii, _ := StrictInt(sorted[i]["index"])
		ij, _ := StrictInt(sorted[j]["index"])
		if ii != ij {
			return ii < ij
		}
		ki, _ := sorted[i]["_chunk_id"].(string)
		kj, _ := sorted[j]["_chunk_id"].(string)
		if ki != kj {
			return ki < kj
		}
		ti, _ := sorted[i]["type"].(string)
		tj, _ := sorted[j]["type"].(string)
		return ti < tj
	})
	var out []map[string]any
	seenKeys := map[string]bool{}
	n := 0
	for _, issue := range sorted {
		enriched := CopyIssue(issue)
		// consistency 解析
		if consRaw, ok := issue["consistency"].(map[string]any); ok && consRaw != nil {
			kind, _ := consRaw["kind"].(string)
			subject, _ := consRaw["subject_source"].(string)
			proposed, _ := consRaw["proposed_value"].(string)
			consistency := map[string]any{}
			if kind != "" || subject != "" || proposed != "" {
				key := ""
				autoArbitration := true
				if kind == "term" && evidence != nil {
					term, ambiguous := evidence.CanonicalTerm(subject)
					if term != nil {
						key = fmt.Sprintf("term:glossary:%s", term.Source)
					} else if len(ambiguous) == 0 {
						key = fmt.Sprintf("term:%s", NormalizedText(subject))
					} else {
						// 歧义：不能自动仲裁
						autoArbitration = false
						key = fmt.Sprintf("term:ambiguous:%s", NormalizedText(subject))
						consistency["ambiguous_sources"] = SortedStrings(ambiguous)
					}
				} else if kind != "" {
					key = fmt.Sprintf("%s:%s", kind, NormalizedText(subject))
				}
				consistency["kind"] = kind
				consistency["subject_source"] = subject
				consistency["proposed_value"] = proposed
				consistency["key"] = key
				consistency["auto_arbitration"] = autoArbitration
			}
			enriched["consistency"] = consistency
		}
		// issue_key：sha256(chapter, index, type, subject)[:20]
		subject := ""
		if cons, ok := enriched["consistency"].(map[string]any); ok {
			subject, _ = cons["subject_source"].(string)
		}
		ch, _ := StrictInt(enriched["chapter"])
		idx, _ := StrictInt(enriched["index"])
		typ, _ := enriched["type"].(string)
		issueKey := IssueKey(ch, idx, typ, subject)
		enriched["issue_key"] = issueKey
		if issueKey != "" {
			if seenKeys[issueKey] {
				continue
			}
			seenKeys[issueKey] = true
		}
		n++
		enriched["issue_id"] = fmt.Sprintf("review-%05d", n)
		out = append(out, enriched)
	}
	return out
}

// IssueKey sha256(chapter, index, type, subject)[:20]。
func IssueKey(chapter, index int, typ, subject string) string {
	payload := CompactJSON([]any{chapter, index, typ, subject})
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:20]
}

// BuildConflictGroups 按 consistency.key 分组；组内 _chunk_id 集合 ≥2 且
// 规范化 proposed_value 集合 ≥2 才构成冲突；auto_arbitration=false 的组排除。
func BuildConflictGroups(issues []map[string]any) []map[string]any {
	byKey := map[string][]map[string]any{}
	var keyOrder []string
	for _, issue := range issues {
		cons, ok := issue["consistency"].(map[string]any)
		if !ok {
			continue
		}
		key, _ := cons["key"].(string)
		if key == "" {
			continue
		}
		if auto, ok := cons["auto_arbitration"].(bool); ok && !auto {
			continue
		}
		if _, seen := byKey[key]; !seen {
			keyOrder = append(keyOrder, key)
		}
		byKey[key] = append(byKey[key], issue)
	}
	var groups []map[string]any
	ordinal := 0
	for _, key := range keyOrder {
		members := byKey[key]
		chunkIDs := map[string]bool{}
		values := map[string]bool{}
		for _, issue := range members {
			if cid, ok := issue["_chunk_id"].(string); ok && cid != "" {
				chunkIDs[cid] = true
			}
			if cons, ok := issue["consistency"].(map[string]any); ok {
				if v, ok := cons["proposed_value"].(string); ok && v != "" {
					values[NormalizedText(v)] = true
				}
			}
		}
		if len(chunkIDs) < 2 || len(values) < 2 {
			continue
		}
		ordinal++
		issueIDs := make([]string, 0, len(members))
		for _, issue := range members {
			id, _ := issue["issue_id"].(string)
			issueIDs = append(issueIDs, id)
		}
		groups = append(groups, map[string]any{
			"conflict_id":     fmt.Sprintf("review-conflict-%04d", ordinal),
			"consistency_key": key,
			"issue_ids":       issueIDs,
			"issues":          members,
		})
	}
	return groups
}

// ApplyReviewArbitrations 把仲裁结果应用到最终问题视图。
// suggested → 落选问题改写为终局仲裁文案（pre_arbitration_* 保留原值）；
// 胜出与 unresolved 只附加 arbitration 注解。返回 (final, superseded_rows)。
func ApplyReviewArbitrations(issues []map[string]any, arbitrations []map[string]any) ([]map[string]any, []map[string]any) {
	arbitrationByID := map[string]map[string]any{}
	for _, arbitration := range arbitrations {
		for _, idRaw := range []any{arbitration["supported_issue_ids"], arbitration["rejected_issue_ids"], arbitration["issue_ids"]} {
			for _, id := range stringSliceOf(idRaw) {
				arbitrationByID[id] = arbitration
			}
		}
	}
	final := make([]map[string]any, 0, len(issues))
	var superseded []map[string]any
	for _, issue := range issues {
		id, _ := issue["issue_id"].(string)
		arbitration := arbitrationByID[id]
		if arbitration == nil {
			final = append(final, issue)
			continue
		}
		enriched := CopyIssue(issue)
		status, _ := arbitration["status"].(string)
		recommended, _ := arbitration["recommended_value"].(string)
		reason, _ := arbitration["reason"].(string)
		conflictID, _ := arbitration["conflict_id"].(string)
		if status == "suggested" {
			rejected := false
			for _, rid := range stringSliceOf(arbitration["rejected_issue_ids"]) {
				if rid == id {
					rejected = true
					break
				}
			}
			if rejected {
				enriched["pre_arbitration_detail"], _ = issue["detail"].(string)
				enriched["pre_arbitration_suggestion"], _ = issue["suggestion"].(string)
				enriched["detail"] = fmt.Sprintf("该处相关表达需按终局仲裁统一为「%s」。", recommended)
				enriched["suggestion"] = fmt.Sprintf("按终局仲裁将相关表达统一为「%s」。", recommended)
				if cons, ok := enriched["consistency"].(map[string]any); ok {
					cons["pre_arbitration_proposed_value"], _ = cons["proposed_value"].(string)
					cons["proposed_value"] = recommended
					enriched["consistency"] = cons
				}
				enriched["arbitration"] = map[string]any{
					"conflict_id": conflictID, "status": status,
					"recommended_value": recommended, "reason": reason, "action": "rewritten",
				}
				final = append(final, enriched)
				superseded = append(superseded, enriched)
				continue
			}
		}
		enriched["arbitration"] = map[string]any{
			"conflict_id": conflictID, "status": status,
			"recommended_value": recommended, "reason": reason,
		}
		final = append(final, enriched)
	}
	return final, superseded
}

// ---- _ActionLoop：request-evidence / final 工具循环 ----

type actionLoopResult struct {
	// data 为模型 final 的对象；ok=false 时 reason 为失败原因。
	data   *jsonx.OMap
	reason string
}

// runActionLoop 在普通 messages 上模拟取证循环。返回 final 对象或失败原因。
func runActionLoop(o *agents.Agent, opts *loopOptions) actionLoopResult {
	maxEvidenceRounds := opts.maxEvidenceRounds
	messages := append([]llm.Message{}, opts.messages...)
	allowedRefs := map[string]bool{}
	seenRequests := map[string]bool{}
	totalCalls := maxEvidenceRounds + 1
	turns := []map[string]any{}
	for call := 1; call <= totalCalls; call++ {
		user := messages[len(messages)-1].Content
		if call == totalCalls && maxEvidenceRounds > 0 {
			user = user + "\n\n取证轮已用尽，只能输出 final。"
			messages[len(messages)-1] = llm.Message{Role: messages[len(messages)-1].Role, Content: user}
		}
		if maxEvidenceRounds == 0 && call == 1 {
			messages[len(messages)-1] = llm.Message{Role: "user", Content: user + "\n\n禁止取证，直接输出 final。"}
		}
		response, err := o.Client.Complete(messages, opts.tier, true, nil, opts.stage)
		if err != nil {
			return actionLoopResult{reason: "llm_error"}
		}
		value, _, perr := llm.ParseJSONResult(response)
		if perr != nil {
			return actionLoopResult{reason: "malformed_json"}
		}
		data, ok := value.(*jsonx.OMap)
		if !ok {
			return actionLoopResult{reason: "response_not_object"}
		}
		last := data.LastKeys(1)
		if len(last) != 1 || last[0] != "complete" {
			return actionLoopResult{reason: "completion_marker_not_last"}
		}
		actionRaw, _ := data.Get("action")
		action, _ := actionRaw.(string)
		completeRaw, _ := data.Get("complete")
		complete, completeIsBool := completeRaw.(bool)
		if action == "final" {
			if !completeIsBool || !complete {
				return actionLoopResult{reason: "final_not_complete"}
			}
			return actionLoopResult{data: data}
		}
		if action != "request_evidence" {
			return actionLoopResult{reason: "unknown_action"}
		}
		if completeIsBool && complete {
			return actionLoopResult{reason: "evidence_marked_complete"}
		}
		if call == totalCalls {
			return actionLoopResult{reason: "evidence_round_limit"}
		}
		requestsRaw, _ := data.Get("requests")
		requests, ok := requestsRaw.([]any)
		if !ok || len(requests) < 1 || len(requests) > 4 {
			return actionLoopResult{reason: "invalid_requests"}
		}
		var results []map[string]any
		var resultPayloads []string
		batchSize := 0
		for _, reqRaw := range requests {
			req, ok := reqRaw.(*jsonx.OMap)
			if !ok {
				return actionLoopResult{reason: "request_not_object"}
			}
			requestIDRaw, _ := req.Get("request_id")
			requestID, _ := requestIDRaw.(string)
			toolRaw, _ := req.Get("tool")
			tool, _ := toolRaw.(string)
			argsRaw, _ := req.Get("arguments")
			args, hasArgs := OMapArgs(argsRaw)
			if !hasArgs {
				args = map[string]any{}
			}
			signature := tool + ":" + CompactJSON(args)
			dedupKey := requestID + "|" + signature
			if requestID == "" || seenRequests[dedupKey] {
				return actionLoopResult{reason: "duplicate_request"}
			}
			seenRequests[dedupKey] = true
			result := opts.evidence.Execute(map[string]any{
				"request_id": requestID, "tool": tool, "arguments": args,
			})
			result["request_id"] = requestID
			result["tool"] = tool
			payload := CompactJSON(result)
			batchSize += len(payload)
			results = append(results, result)
			resultPayloads = append(resultPayloads, payload)
		}
		for i := range results {
			if batchSize > evidenceBatchLimit {
				results[i] = map[string]any{"request_id": results[i]["request_id"], "tool": results[i]["tool"],
					"ok": false, "error": "evidence_batch_too_large"}
			}
		}
		for _, result := range results {
			for ref := range opts.evidence.EvidenceRefs(result) {
				allowedRefs[ref] = true
			}
		}
		turns = append(turns, map[string]any{
			"turn": len(turns) + 1, "messages": messagesToRows(messages),
			"raw_response": response, "parsed": data, "evidence_results": results,
		})
		messages = append(messages, llm.Message{Role: "assistant", Content: response})
		messages = append(messages, llm.Message{Role: "user", Content: "【证据工具返回 JSON】\n" + Indent2JSON(results)})
	}
	return actionLoopResult{reason: "evidence_round_limit"}
}

type loopOptions struct {
	messages          []llm.Message
	tier              string
	stage             string
	maxEvidenceRounds int
	evidence          *BookEvidenceIndex
}

// reviewCandidate 初审候选。
type reviewCandidate struct {
	id    string
	issue map[string]any
}

// ---- ReviewAgentLoop ----

// ReviewLoopOutcome 单块取证复核结果。
type ReviewLoopOutcome struct {
	Issues        []map[string]any
	Dismissed     []map[string]any
	FallbackReason string
}

// ReviewAgentLoop 块级取证复核。
type ReviewAgentLoop struct {
	Agent    agents.Agent
	evidence  *BookEvidenceIndex
	debug     *ReviewRunStore
}

func NewReviewAgentLoop(client llm.LLMClient, cfg *config.Config, evidence *BookEvidenceIndex, debug *ReviewRunStore) *ReviewAgentLoop {
	l := &ReviewAgentLoop{evidence: evidence, debug: debug}
	l.Agent = agents.NewAgent(client, cfg, "ReviewAgentLoop")
	return l
}

// ReviewChunk 核验一块的初审候选；失败回退原样保留候选。
func (l *ReviewAgentLoop) ReviewChunk(chapter, chunkBase int, sources, targets []string,
	initialIssues []map[string]any, reviewRound *int) *ReviewLoopOutcome {
	fallback := func(reason string) *ReviewLoopOutcome {
		kept := make([]map[string]any, 0, len(initialIssues))
		for ordinal, issue := range initialIssues {
			enriched := CopyIssue(issue)
			enriched["index"] = chunkBase + ordinal
			enriched["origin"] = "initial"
			enriched["agent_fallback"] = true
			enriched["fallback_reason"] = reason
			if l.evidence != nil {
				if ref := l.evidence.SegmentRefLookup(chapter, chunkBase+ordinal); ref != nil {
					enriched["evidence_refs"] = []string{ref.Ref()}
				}
			}
			kept = append(kept, enriched)
		}
		return &ReviewLoopOutcome{Issues: kept, Dismissed: []map[string]any{}, FallbackReason: reason}
	}
	if len(initialIssues) == 0 {
		return &ReviewLoopOutcome{Issues: []map[string]any{}, Dismissed: []map[string]any{}}
	}
	var candidates []reviewCandidate
	for ordinal, issue := range initialIssues {
		id := ReviewCandidateID(chapter, chunkBase, ordinal, reviewRound)
		enriched := CopyIssue(issue)
		enriched["candidate_id"] = id
		enriched["issue_id"] = id
		candidates = append(candidates, reviewCandidate{id: id, issue: enriched})
	}
	roundPrefix := ""
	if reviewRound != nil {
		roundPrefix = fmt.Sprintf("r%d-", *reviewRound)
	}
	agentID := fmt.Sprintf("%schunk-ch%d-base%d-n%d", roundPrefix, chapter, chunkBase, len(sources))
	// refs
	refs := make([]string, len(sources))
	for i := range sources {
		if ref := l.evidence.SegmentRefLookup(chapter, chunkBase+i); ref != nil {
			refs[i] = ref.Ref()
		}
	}
	refRows := make([]any, len(refs))
	for i, ref := range refs {
		refRows[i] = map[string]any{"index": i, "ref": ref}
	}
	segmentRefsJSON := Indent2JSON(refRows)
	candidatesJSON := Indent2JSON(issueSliceToAny(dereferenceCandidates(candidates)))
	system := agents.Render("review_agent_system", agents.RenderOptions{
		Src: l.Agent.Src, Tgt: l.Agent.Tgt,
		Vars: map[string]string{
			"max_evidence_rounds": fmt.Sprintf("%d", l.Agent.Config.Pipeline.ReviewAgentMaxEvidenceRounds),
		},
	})
	user := agents.Render("review_agent_user", agents.RenderOptions{
		Src: l.Agent.Src, Tgt: l.Agent.Tgt,
		Vars: map[string]string{
			"chapter":            fmt.Sprintf("%d", chapter),
			"last_index":         fmt.Sprintf("%d", len(sources)-1),
			"pairs":              agents.NumberedPairsWithRefs(sources, targets, refs),
			"segment_refs_json":  segmentRefsJSON,
			"candidates_json":    candidatesJSON,
		},
	})
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	result := runActionLoop(&l.Agent, &loopOptions{
		messages: messages, tier: l.Agent.Config.Pipeline.ReviewAgentTier, stage: "ReviewAgentLoop",
		maxEvidenceRounds: l.Agent.Config.Pipeline.ReviewAgentMaxEvidenceRounds,
		evidence: l.evidence,
	})
	if l.debug != nil {
		trace := map[string]any{
			"agent_id": agentID, "stage": "ReviewAgentLoop", "status": "finished",
		}
		if result.data == nil {
			trace["status"] = "fallback"
			trace["fallback_reason"] = result.reason
		}
		_ = l.debug.WriteJSON("agents/"+safeID(agentID)+".json", trace)
		if result.data == nil {
			_ = l.debug.LogEvent("review_agent_fallback", map[string]any{
				"agent_id": agentID, "reason": result.reason,
			})
		} else {
			_ = l.debug.LogEvent("review_evidence_supplied", map[string]any{
				"agent_id": agentID,
			})
		}
	}
	if result.data == nil {
		return fallback(result.reason)
	}
	final, reason := l.validateFinal(chapter, chunkBase, sources, result.data, candidates, refs)
	if final == nil {
		return fallback(reason)
	}
	return final
}

func dereferenceCandidates(candidates []reviewCandidate) []map[string]any {
	out := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.issue)
	}
	return out
}

func stringSliceToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func issueSliceToAny(ms []map[string]any) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}

func safeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// validateFinal 校验 final 输出并生成 issues/dismissed。
func (l *ReviewAgentLoop) validateFinal(chapter, chunkBase int, sources []string, data *jsonx.OMap,
	candidates []reviewCandidate, refs []string,
) (*ReviewLoopOutcome, string) {
	decisionsRaw, _ := data.Get("decisions")
	decisions, ok := decisionsRaw.([]any)
	if !ok {
		return nil, "decisions_not_list"
	}
	byID := map[string]map[string]any{}
	var decisionOrder []string
	for _, dRaw := range decisions {
		d, ok := dRaw.(*jsonx.OMap)
		if !ok {
			return nil, "decision_not_object"
		}
		idRaw, _ := d.Get("candidate_id")
		id, _ := idRaw.(string)
		if id == "" {
			return nil, "decision_missing_candidate_id"
		}
		if _, dup := byID[id]; dup {
			return nil, "decision_duplicate_candidate"
		}
		m := OMapToMap(d)
		byID[id] = m
		decisionOrder = append(decisionOrder, id)
	}
	if len(byID) != len(candidates) {
		return nil, "decisions_do_not_cover_candidates"
	}
	for _, c := range candidates {
		if _, ok := byID[c.id]; !ok {
			return nil, "decisions_do_not_cover_candidates"
		}
	}
	currentRefs := map[string]bool{}
	for _, ref := range refs {
		currentRefs[ref] = true
	}
	var issues []map[string]any
	var dismissed []map[string]any
	for _, c := range candidates {
		decision := byID[c.id]
		verdict, _ := decision["verdict"].(string)
		if verdict != "confirmed" && verdict != "dismissed" {
			return nil, "invalid_verdict"
		}
		reason, _ := decision["reason"].(string)
		detail, _ := decision["detail"].(string)
		suggestion, _ := decision["suggestion"].(string)
		if verdict == "dismissed" {
			if reason == "" {
				return nil, "dismissed_without_reason"
			}
			row := CopyIssue(c.issue)
			row["reason"] = reason
			row["index"] = chunkBase + issueOrdinal(c.issue)
			dismissed = append(dismissed, row)
			continue
		}
		if detail == "" {
			detail, _ = c.issue["detail"].(string)
		}
		if suggestion == "" {
			suggestion, _ = c.issue["suggestion"].(string)
		}
		if detail == "" || suggestion == "" {
			return nil, "confirmed_without_detail"
		}
		issue := CopyIssue(c.issue)
		issue["index"] = chunkBase + issueOrdinal(c.issue)
		issue["detail"] = detail
		issue["suggestion"] = suggestion
		issue["origin"] = "agent"
		// evidence_refs 白名单
		refsOut := []string{}
		if arr, ok := decision["evidence_refs"].([]any); ok {
			for _, r := range arr {
				if s, ok := r.(string); ok && s != "" {
					if !currentRefs[s] {
						return nil, "unknown_evidence_ref"
					}
					refsOut = append(refsOut, s)
				}
			}
		}
		if len(refsOut) == 0 {
			refsOut = []string{refs[issueOrdinal(c.issue)]}
		}
		issue["evidence_refs"] = refsOut
		// consistency 清洗
		if consRaw, ok := decision["consistency"].(map[string]any); ok && len(consRaw) > 0 {
			kind, _ := consRaw["kind"].(string)
			subject, _ := consRaw["subject_source"].(string)
			proposed, _ := consRaw["proposed_value"].(string)
			if kind == "" || subject == "" || proposed == "" {
				return nil, "invalid_consistency"
			}
			if kind != "term" && kind != "pronoun" && kind != "fixed" {
				return nil, "invalid_consistency"
			}
			issue["consistency"] = map[string]any{
				"kind": kind, "subject_source": subject, "proposed_value": proposed,
			}
		} else {
			issue["consistency"] = map[string]any{}
		}
		issues = append(issues, issue)
	}
	// new_issues
	newIssuesRaw, _ := data.Get("new_issues")
	newIssues, _ := newIssuesRaw.([]any)
	limit := 50
	if min := len(sources) * 2; min < limit {
		if min < 4 {
			limit = 4
		} else {
			limit = min
		}
	}
	if len(newIssues) > limit {
		return nil, "new_issues_over_limit"
	}
	for _, niRaw := range newIssues {
		ni, ok := niRaw.(*jsonx.OMap)
		if !ok {
			return nil, "new_issue_not_object"
		}
		idxRaw, _ := ni.Get("index")
		idx, ok := StrictInt(idxRaw)
		if !ok || idx < 0 || idx >= len(sources) {
			return nil, "new_issue_out_of_chunk"
		}
		typ, _ := ni.Get("type")
		typeStr, _ := typ.(string)
		if !reviewerIssueTypes[typeStr] {
			return nil, "new_issue_invalid_type"
		}
		detail, _ := ni.Get("detail")
		suggestion, _ := ni.Get("suggestion")
		detailStr, _ := detail.(string)
		suggestionStr, _ := suggestion.(string)
		if detailStr == "" || suggestionStr == "" {
			return nil, "new_issue_missing_fields"
		}
		issue := map[string]any{
			"index":      chunkBase + idx,
			"type":       typeStr,
			"detail":     detailStr,
			"suggestion": suggestionStr,
			"origin":     "agent",
		}
		refsOut := []string{}
		if arr, ok := OMapAny(ni, "evidence_refs"); ok {
			if list, ok := arr.([]any); ok {
				for _, r := range list {
					if s, ok := r.(string); ok && s != "" {
						if !currentRefs[s] {
							return nil, "unknown_evidence_ref"
						}
						refsOut = append(refsOut, s)
					}
				}
			}
		}
		if len(refsOut) == 0 {
			refsOut = []string{refs[idx]}
		}
		issue["evidence_refs"] = refsOut
		if consRaw, ok := ni.Get("consistency"); ok {
			if cons, ok := consRaw.(*jsonx.OMap); ok {
				kind, _ := cons.Get("kind")
				subject, _ := cons.Get("subject_source")
				proposed, _ := cons.Get("proposed_value")
				kindStr, _ := kind.(string)
				subjectStr, _ := subject.(string)
				proposedStr, _ := proposed.(string)
				if kindStr != "" || subjectStr != "" || proposedStr != "" {
					if (kindStr != "term" && kindStr != "pronoun" && kindStr != "fixed") || subjectStr == "" || proposedStr == "" {
						return nil, "invalid_consistency"
					}
					issue["consistency"] = map[string]any{
						"kind": kindStr, "subject_source": subjectStr, "proposed_value": proposedStr,
					}
				} else {
					issue["consistency"] = map[string]any{}
				}
			}
		}
		issues = append(issues, issue)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		ii, _ := StrictInt(issues[i]["index"])
		jj, _ := StrictInt(issues[j]["index"])
		return ii < jj
	})
	return &ReviewLoopOutcome{Issues: issues, Dismissed: dismissed}, ""
}

func issueOrdinal(issue map[string]any) int {
	idx, _ := StrictInt(issue["index"])
	return idx
}

func OMapAny(om *jsonx.OMap, key string) (any, bool) {
	return om.Get(key)
}

// ---- ReviewConflictArbiter ----

// ReviewConflictArbiter 冲突终局仲裁。
type ReviewConflictArbiter struct {
	Agent    agents.Agent
	evidence  *BookEvidenceIndex
	debug     *ReviewRunStore
}

func NewReviewConflictArbiter(client llm.LLMClient, cfg *config.Config, evidence *BookEvidenceIndex, debug *ReviewRunStore) *ReviewConflictArbiter {
	a := &ReviewConflictArbiter{evidence: evidence, debug: debug}
	a.Agent = agents.NewAgent(client, cfg, "ReviewConflictArbiter")
	return a
}

// Arbitrate 仲裁一个冲突组；失败 → unresolved 记录。
func (a *ReviewConflictArbiter) Arbitrate(conflict map[string]any) map[string]any {
	unresolved := func(reason string) map[string]any {
		return map[string]any{
			"conflict_id": conflict["conflict_id"], "status": "unresolved",
			"recommended_value": "", "reason": reason,
			"evidence_refs": []string{}, "supported_issue_ids": []string{}, "rejected_issue_ids": []string{},
		}
	}
	issuesRaw, _ := conflict["issues"].([]map[string]any)
	if issuesRaw == nil {
		if arr, ok := conflict["issues"].([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					issuesRaw = append(issuesRaw, m)
				}
			}
		}
	}
	if len(issuesRaw) == 0 {
		return unresolved("empty_conflict")
	}
	// 按规范化 proposed_value 分组
	groups := map[string][]map[string]any{}
	var groupOrder []string
	exactByNorm := map[string]string{}
	for _, issue := range issuesRaw {
		cons, _ := issue["consistency"].(map[string]any)
		proposed, _ := cons["proposed_value"].(string)
		norm := NormalizedText(proposed)
		if _, seen := groups[norm]; !seen {
			groupOrder = append(groupOrder, norm)
		}
		groups[norm] = append(groups[norm], issue)
		if _, exists := exactByNorm[norm]; !exists {
			exactByNorm[norm] = proposed
		}
	}
	if len(groups) > maxArbitrationGroups {
		return unresolved(fmt.Sprintf("提议组数 %d 超过上限 %d。", len(groups), maxArbitrationGroups))
	}
	// payload：conflict_id + 每组 {issue_count, proposed_value, samples[3]}（首/中/尾）
	payload := map[string]any{
		"conflict_id":     conflict["conflict_id"],
		"consistency_key": conflict["consistency_key"],
		"issue_ids":       conflict["issue_ids"],
		"proposals":       []any{},
	}
	var proposals []any
	sampleRefs := map[string]bool{}
	for _, norm := range groupOrder {
		members := groups[norm]
		sort.SliceStable(members, func(i, j int) bool {
			ci, _ := StrictInt(members[i]["chapter"])
			cj, _ := StrictInt(members[j]["chapter"])
			if ci != cj {
				return ci < cj
			}
			ii, _ := StrictInt(members[i]["index"])
			jj, _ := StrictInt(members[j]["index"])
			return ii < jj
		})
		pickIdx := func(pos int) int { return pos }
		_ = pickIdx
		sample := func(pos int) map[string]any {
			member := members[pos]
			ch, _ := StrictInt(member["chapter"])
			idx, _ := StrictInt(member["index"])
			row := map[string]any{"chapter": ch, "index": idx}
			if a.evidence != nil {
				if ref := a.evidence.SegmentRefLookup(ch, idx); ref != nil {
					row["segment"] = ref.Compact()
					sampleRefs[ref.Ref()] = true
				}
			}
			return row
		}
		var samples []map[string]any
		samples = append(samples, sample(0))
		if mid := (len(members) - 1) / 2; mid > 0 && mid < len(members)-1 {
			samples = append(samples, sample(mid))
		}
		if len(members) > 1 {
			samples = append(samples, sample(len(members)-1))
		}
		proposedValue, _ := members[0]["consistency"].(map[string]any)["proposed_value"].(string)
		proposals = append(proposals, map[string]any{
			"proposed_value": proposedValue,
			"issue_count":    len(members),
			"samples":        samples,
		})
	}
	payload["proposals"] = proposals
	payloadJSON := CompactJSON(payload)
	if len(payloadJSON) > arbitrationPayloadLimit {
		return unresolved("仲裁材料超出大小上限。")
	}
	system := agents.Render("review_arbiter_system", agents.RenderOptions{
		Src: a.Agent.Src, Tgt: a.Agent.Tgt,
		Vars: map[string]string{
			"max_evidence_rounds": fmt.Sprintf("%d", a.Agent.Config.Pipeline.ReviewAgentMaxEvidenceRounds),
		},
	})
	user := agents.Render("review_arbiter_user", agents.RenderOptions{
		Src: a.Agent.Src, Tgt: a.Agent.Tgt,
		Vars: map[string]string{"conflict_json": Indent2JSON(payload)},
	})
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	result := runActionLoop(&a.Agent, &loopOptions{
		messages: messages, tier: a.Agent.Config.Pipeline.ReviewAgentTier, stage: "ReviewConflictArbiter",
		maxEvidenceRounds: a.Agent.Config.Pipeline.ReviewAgentMaxEvidenceRounds,
		evidence: a.evidence,
	})
	if a.debug != nil {
		conflictID, _ := conflict["conflict_id"].(string)
		if result.data == nil {
			_ = a.debug.LogEvent("review_agent_fallback", map[string]any{
				"agent_id": "arbiter-" + conflictID, "reason": result.reason,
			})
		}
	}
	if result.data == nil {
		return unresolved("仲裁取证失败：" + result.reason)
	}
	// final 校验
	conflictIDRaw, _ := result.data.Get("conflict_id")
	conflictID, _ := conflictIDRaw.(string)
	wantID, _ := conflict["conflict_id"].(string)
	if conflictID != wantID {
		return unresolved("conflict_id_mismatch")
	}
	if _, present := result.data.Get("supported_issue_ids"); present {
		return unresolved("model_returned_issue_lists")
	}
	if _, present := result.data.Get("rejected_issue_ids"); present {
		return unresolved("model_returned_issue_lists")
	}
	statusRaw, _ := result.data.Get("status")
	status, _ := statusRaw.(string)
	if status != "suggested" && status != "unresolved" {
		return unresolved("invalid_status")
	}
	reasonRaw, _ := result.data.Get("reason")
	reason, _ := reasonRaw.(string)
	if reason == "" {
		return unresolved("missing_reason")
	}
	// evidence_refs 白名单：仅本轮实际取得的 ref（+样本段自身 ref？规格：白名单=取证所得）
	allowed := map[string]bool{}
	if arr, ok := OMapAny(result.data, "evidence_refs"); ok {
		if list, ok := arr.([]any); ok {
			for _, r := range list {
				if s, ok := r.(string); ok && s != "" {
					if !sampleRefs[s] && !allowed[s] {
						// 本轮取证可见性由 runActionLoop 的 allowedRefs 决定；
						// 仲裁器只认它自己取样/取证的 ref——样本身份可见
						return map[string]any{
							"conflict_id": wantID, "status": "unresolved", "recommended_value": "",
							"reason":      "unknown_evidence_ref", "evidence_refs": []string{},
							"supported_issue_ids": []string{}, "rejected_issue_ids": []string{},
						}
					}
					allowed[s] = true
				}
			}
		}
	}
	if status == "unresolved" {
		return map[string]any{
			"conflict_id": wantID, "status": "unresolved", "recommended_value": "",
			"reason": reason, "evidence_refs": SortedKeysOfBool(allowed),
			"supported_issue_ids": []string{}, "rejected_issue_ids": []string{},
		}
	}
	recommendedRaw, _ := result.data.Get("recommended_value")
	recommended, _ := recommendedRaw.(string)
	// recommended_value 必须等于某个输入 proposed_value（按规范化匹配，取精确拼写）
	normRecommended := NormalizedText(recommended)
	if _, ok := groups[normRecommended]; !ok {
		return unresolved("recommended_value_not_proposed")
	}
	exact := exactByNorm[normRecommended]
	// 本地重算 support/reject
	var supported, rejected []string
	for _, issue := range issuesRaw {
		id, _ := issue["issue_id"].(string)
		cons, _ := issue["consistency"].(map[string]any)
		proposed, _ := cons["proposed_value"].(string)
		if NormalizedText(proposed) == normRecommended {
			supported = append(supported, id)
		} else {
			rejected = append(rejected, id)
		}
	}
	return map[string]any{
		"conflict_id": wantID, "status": "suggested", "recommended_value": exact,
		"reason": reason, "evidence_refs": SortedKeysOfBool(allowed),
		"supported_issue_ids": supported, "rejected_issue_ids": rejected,
	}
}

func SortedKeysOfBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stringSliceOf 兼容 []string 与 []any 的字符串列表读取。
func stringSliceOf(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		var out []string
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
