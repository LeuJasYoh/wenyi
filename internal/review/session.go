package review

// session.go 只读全书 Review 会话编排（分册 07 §4.3 _run_review_session 等价）。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/jsonx"
	"wenyi/internal/ingest"
	"wenyi/internal/llm"
)

// Outcome 只读 Review 的正式结果（pipeline 侧消费）。
type Outcome struct {
	RunDir  string
	Result  map[string]any
	Issues  []map[string]any
	Changes []map[string]any
}

// StoreApi RunStore 能力接口（避免 review→pipeline 依赖环）。
type StoreApi interface {
	PendingChapters() ([]int, error)
	LoadManifest() (map[string]any, error)
	LoadChapter(ci int) (*ingest.Chapter, error)
	LoadAnalysis() (map[string]any, error)
}

// Progress 进度回调（与 Progress 同构）。
type Progress func(done, total int, label string)

// SessionDeps 会话依赖（由 pipeline 侧注入 orchestrator 能力）。
type SessionDeps struct {
	Store   StoreApi
	RunDir  string
	Client  llm.LLMClient
	Config  *config.Config
	Analyzer *agents.Analyzer
	FlushUsage func(scope string) (map[string]any, error)
	LogEvent   func(event string, data map[string]any) error
}

// RunReviewSession 执行全书只读审校；正式章节/manifest/术语库始终不变。
func RunReviewSession(terms []*glossary.Term, deps SessionDeps, progress Progress) (*Outcome, error) {
	store := deps.Store
	cfg := deps.Config
	// 前置：所有章节必须已完成翻译
	pending, err := store.PendingChapters()
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		shown := pending
		if len(shown) > 10 {
			shown = shown[:10]
		}
		var names []string
		for _, p := range shown {
			names = append(names, fmt.Sprintf("%d", p))
		}
		suffix := ""
		if len(pending) > 10 {
			suffix = "…"
		}
		return nil, fmt.Errorf("全书审校要求所有章节先完成翻译；仍待翻译章节：%s%s", strings.Join(names, "、"), suffix)
	}
	manifest, err := store.LoadManifest()
	if err != nil {
		return nil, err
	}
	var loaded []*ingest.Chapter
	chapterItems, _ := manifest["chapters"].([]any)
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := cm["index"].(int64)
		if !ok {
			continue
		}
		ch, err := store.LoadChapter(int(idx))
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, ch)
	}
	total := 0
	for _, ch := range loaded {
		total += len(ch.TextSegments())
	}
	analysis, err := store.LoadAnalysis()
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		analysis = map[string]any{}
	}
	reviewedContentDigest := reviewContentDigest(loaded)
	debug, err := NewReviewRunStore(deps.RunDir, nil)
	if err != nil {
		return nil, err
	}
	title, _ := manifest["title"].(string)
	sourcePath, _ := manifest["source_path"].(string)
	p := cfg.Pipeline
	if err := debug.Start(reviewedContentDigest, map[string]any{
		"source_path": sourcePath, "title": title,
		"source_lang": cfg.SourceLang, "target_lang": cfg.TargetLang,
		"chapter_count": len(loaded), "total_segments": total,
		"config": map[string]any{
			"review_concurrency": p.ReviewConcurrency, "review_output_retries": p.ReviewOutputRetries,
			"review_agent_loop": p.ReviewAgentLoop, "review_agent_tier": p.ReviewAgentTier,
			"review_agent_max_evidence_rounds": p.ReviewAgentMaxEvidenceRounds,
			"review_conflict_arbitration": p.ReviewConflictArbitration,
			"review_fix_loop": p.ReviewFixLoop, "review_fix_max_rounds": p.ReviewFixMaxRounds,
			"review_clean_confirmations": p.ReviewCleanConfirmations,
		},
	}); err != nil {
		return nil, err
	}
	_ = deps.LogEvent("review_started", map[string]any{
		"review_id": debug.ReviewID, "review_dir": debug.RunDir,
		"reviewed_content_digest": reviewedContentDigest,
	})
	usageBefore := deps.Client.UsageSummary()
	saveUsage := func() map[string]any {
		usage := llm.UsageDelta(deps.Client.UsageSummary(), usageBefore)
		_ = debug.SaveUsage(usage)
		_, _ = deps.FlushUsage("review")
		return usage
	}
	finishFailed := func(runErr error) (*Outcome, error) {
		initial, dismissed := debug.ResultSnapshots(nil)
		partialIssues := []map[string]any{}
		public := PublicIssues(partialIssues)
		_ = debug.WriteJSON("final/initial_issues.json", initial)
		_ = debug.WriteJSON("final/dismissed_issues.json", dismissed)
		_ = debug.WriteJSON("final/partial_issues.json", partialIssues)
		_ = debug.WriteJSON("final/partial_patches.json", []any{})
		_ = debug.WriteJSON("final/fix_failures.json", []any{})
		_, _ = debug.Finish("failed", "error", map[string]any{
			"issue_count": 0, "change_count": 0, "conflict_count": 0, "fallback_agent_count": 0,
		}, public, []map[string]any{}, map[string]any{"type": errorTypeName(runErr), "message": runErr.Error()})
		saveUsage()
		_ = deps.LogEvent("review_finished", map[string]any{
			"review_id": debug.ReviewID, "review_dir": debug.RunDir,
			"status": "failed", "termination": "error", "issue_count": 0, "change_count": 0,
			"error_type": errorTypeName(runErr), "error": runErr.Error(),
		})
		return nil, runErr
	}
	_ = finishFailed
	// ---- 主循环 ----
	targetOverrides := map[[2]int]string{}
	seenOverlays := map[string]bool{reviewOverlayDigest(loaded, targetOverrides): true}
	var patchRecords []map[string]any
	activePatches := map[[2]int]map[string]any{}
	var fixFailures []map[string]any
	blockedIssues := map[string]map[string]any{}
	var roundSummaries []map[string]any
	var latestFinal []map[string]any
	cleanStreak := 0
	fixRounds := 0
	termination := "not_started"
	fixLoop := p.ReviewFixLoop
	requiredClean := p.ReviewCleanConfirmations
	if !fixLoop {
		requiredClean = 1
	}
	maxReviewRounds := (p.ReviewFixMaxRounds + 1) * requiredClean
	if !fixLoop {
		maxReviewRounds = 1
	}
	overlaySnapshot := func() []map[string]any {
		var rows []map[string]any
		for loc, target := range targetOverrides {
			rows = append(rows, map[string]any{"chapter": loc[0], "index": loc[1], "target": target})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i]["chapter"].(int) != rows[j]["chapter"].(int) {
				return rows[i]["chapter"].(int) < rows[j]["chapter"].(int)
			}
			return rows[i]["index"].(int) < rows[j]["index"].(int)
		})
		return rows
	}
mainLoop:
	for reviewRound := 1; reviewRound <= maxReviewRounds; reviewRound++ {
		overlayDigest := reviewOverlayDigest(loaded, targetOverrides)
		evidence := NewBookEvidenceIndex(loaded, terms, analysis, targetOverrides)
		unlock, err := debug.RoundScope(reviewRound)
		if err != nil {
			return nil, err
		}
		_ = debug.LogEvent("review_round_started", map[string]any{
			"overlay_digest": overlayDigest, "override_count": len(targetOverrides),
		})
		_ = debug.WriteJSON("overlay.json", overlaySnapshot())
		_, latestFinal, err = runReviewRound(deps, debug, evidence, loaded, terms, analysis, reviewRound, targetOverrides, progress)
		unlock()
		if err != nil {
			return nil, err
		}
		// active_patches 状态推进
		currentKeys := map[string]bool{}
		for _, issue := range latestFinal {
			if k, ok := issue["issue_key"].(string); ok {
				currentKeys[k] = true
			}
		}
		for loc, record := range activePatches {
			if r, ok := record["round"].(int); !ok || r >= reviewRound {
				continue
			} else {
				_ = r
			}
			covered, _ := record["covered_issue_keys"].([]string)
			var rereported, notRereported []string
			for _, k := range covered {
				if currentKeys[k] {
					rereported = append(rereported, k)
				} else {
					notRereported = append(notRereported, k)
				}
			}
			record["rereported_issue_keys"] = rereported
			record["not_rereported_issue_keys"] = notRereported
			for _, k := range notRereported {
				delete(blockedIssues, k)
			}
			if len(rereported) > 0 {
				record["status"] = "needs_revision"
				record["failed_review_round"] = reviewRound
			} else {
				record["status"] = "not_rereported"
			}
			activePatches[loc] = record
		}
		roundSummary := map[string]any{
			"review_round": reviewRound, "overlay_digest": overlayDigest,
			"override_count": len(targetOverrides), "issue_count": len(latestFinal),
			"clean_streak_before": cleanStreak, "blocked_issue_count": len(blockedIssues),
		}
		if len(latestFinal) == 0 {
			if len(blockedIssues) > 0 {
				cleanStreak = 0
				termination = "unresolved_fixes"
				roundSummary["clean_streak_after"] = 0
				roundSummary["patch_count"] = 0
				roundSummary["termination"] = termination
				_ = debug.WriteJSON("summary.json", roundSummary)
				roundSummaries = append(roundSummaries, roundSummary)
				break mainLoop
			}
			cleanStreak++
			roundSummary["clean_streak_after"] = cleanStreak
			roundSummary["patch_count"] = 0
			if cleanStreak >= requiredClean {
				termination = "clean_confirmed"
			}
			roundSummary["termination"] = termination
			_ = debug.WriteJSON("summary.json", roundSummary)
			roundSummaries = append(roundSummaries, roundSummary)
			if termination == "clean_confirmed" {
				break mainLoop
			}
			continue mainLoop
		}
		if cleanStreak != 0 {
			cleanStreak = 0
		}
		roundSummary["clean_streak_after"] = 0
		if !fixLoop {
			termination = "issues_reported"
			roundSummary["patch_count"] = 0
			roundSummary["termination"] = termination
			_ = debug.WriteJSON("summary.json", roundSummary)
			roundSummaries = append(roundSummaries, roundSummary)
			break mainLoop
		}
		if fixRounds >= p.ReviewFixMaxRounds {
			termination = "max_rounds"
			roundSummary["termination"] = termination
			_ = debug.WriteJSON("summary.json", roundSummary)
			roundSummaries = append(roundSummaries, roundSummary)
			break mainLoop
		}
		// 影子修订提议
		patches, failures := proposeReviewPatches(deps, debug, evidence, terms, analysis, latestFinal, reviewRound, fixRounds+1, progress)
		for _, failure := range failures {
			failure["review_round"] = reviewRound
			fixFailures = append(fixFailures, failure)
		}
		registerBlocked(blockedIssues, latestFinal, failures)
		roundSummary["patch_count"] = len(patches)
		if len(patches) == 0 {
			termination = "no_progress"
			roundSummary["fix_failure_count"] = len(failures)
			roundSummary["blocked_issue_count"] = len(blockedIssues)
			_ = debug.WriteJSON("patches.json", []any{})
			_ = debug.WriteJSON("fix_failures.json", fixFailures)
			roundSummary["termination"] = termination
			_ = debug.WriteJSON("summary.json", roundSummary)
			roundSummaries = append(roundSummaries, roundSummary)
			break mainLoop
		}
		// 乐观校验（before_hash）
		keysByID := map[string]string{}
		for _, issue := range latestFinal {
			id, _ := issue["issue_id"].(string)
			key, _ := issue["issue_key"].(string)
			keysByID[id] = key
		}
		candidateOverrides := map[[2]int]string{}
		for k, v := range targetOverrides {
			candidateOverrides[k] = v
		}
		var applicable []*ProvisionalPatch
		var hashFailures []map[string]any
		for _, patch := range patches {
			loc := [2]int{patch.Chapter, patch.Index}
			current := evidence.SegmentRefLookup(patch.Chapter, patch.Index)
			if current == nil || TargetHash(current.Target) != patch.BeforeHash {
				failure := map[string]any{
					"patch_id": patch.PatchID, "issue_ids": patch.IssueIDs,
					"chapter": patch.Chapter, "index": patch.Index,
					"status": "failed", "reason": "before_hash_changed", "review_round": reviewRound,
				}
				fixFailures = append(fixFailures, failure)
				hashFailures = append(hashFailures, failure)
				continue
			}
			candidateOverrides[loc] = patch.After
			applicable = append(applicable, patch)
		}
		registerBlocked(blockedIssues, latestFinal, hashFailures)
		roundSummary["fix_failure_count"] = len(failures) + len(hashFailures)
		roundSummary["blocked_issue_count"] = len(blockedIssues)
		candidateDigest := reviewOverlayDigest(loaded, candidateOverrides)
		patchRows := make([]any, 0, len(patches))
		for _, patch := range patches {
			patchRows = append(patchRows, patch.AsDict())
		}
		_ = debug.WriteJSON("patches.json", patchRows)
		_ = debug.WriteJSON("fix_failures.json", fixFailures)
		roundSummary["candidate_overlay_digest"] = candidateDigest
		roundSummary["applicable_patch_count"] = len(applicable)
		if len(applicable) == 0 || candidateDigest == overlayDigest {
			termination = "no_progress"
			roundSummary["termination"] = termination
			_ = debug.WriteJSON("summary.json", roundSummary)
			roundSummaries = append(roundSummaries, roundSummary)
			break mainLoop
		}
		if seenOverlays[candidateDigest] {
			termination = "cycle_detected"
			for _, patch := range applicable {
				var keys []string
				for _, id := range patch.IssueIDs {
					if k := keysByID[id]; k != "" {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				record := patch.AsDict()
				record["issue_keys"] = keys
				record["status"] = "rejected_cycle"
				patchRecords = append(patchRecords, record)
			}
			roundSummary["termination"] = termination
			_ = debug.WriteJSON("summary.json", roundSummary)
			roundSummaries = append(roundSummaries, roundSummary)
			break mainLoop
		}
		// 接受补丁
		fixRounds++
		for _, patch := range applicable {
			loc := [2]int{patch.Chapter, patch.Index}
			var keys []string
			for _, id := range patch.IssueIDs {
				if k := keysByID[id]; k != "" {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			record := patch.AsDict()
			record["issue_keys"] = keys
			record["round"] = reviewRound
			if previous, ok := activePatches[loc]; ok {
				previous["status"] = "superseded"
				previous["superseded_by"] = patch.PatchID
			}
			record["covered_issue_keys"] = keys
			patchRecords = append(patchRecords, record)
			activePatches[loc] = record
		}
		targetOverrides = candidateOverrides
		seenOverlays[candidateDigest] = true
		roundSummary["fix_round"] = fixRounds
		_ = debug.WriteJSON("summary.json", roundSummary)
		roundSummaries = append(roundSummaries, roundSummary)
	}
	if reviewLoopDone(termination) == false && reviewRoundExhausted(termination) {
		termination = "max_rounds"
	}
	// 收尾
	unresolved := effectiveIssues(latestFinal, blockedIssues)
	publicIssues := PublicIssues(unresolved)
	changes := reviewNetChanges(loaded, targetOverrides, patchRecords, activePatches)
	summary := map[string]any{
		"issue_count": len(publicIssues), "change_count": len(changes),
		"review_round_count": len(roundSummaries), "fix_round_count": fixRounds,
		"patch_count": len(patchRecords), "blocked_issue_count": len(blockedIssues),
		"clean_streak": cleanStreak, "shadow_override_count": len(targetOverrides),
		"termination": termination,
	}
	result, err := debug.Finish("completed", termination, summary, publicIssues, changes, nil)
	if err != nil {
		return nil, err
	}
	usage := saveUsage()
	_ = deps.LogEvent("review_finished", map[string]any{
		"review_id": debug.ReviewID, "review_dir": debug.RunDir,
		"status": "completed", "termination": termination,
		"issue_count": len(publicIssues), "change_count": len(changes),
	})
	_ = usage
	return &Outcome{
		RunDir: debug.RunDir,
		Result: result,
		Issues: publicIssues,
		Changes: changes,
	}, nil
}

func reviewLoopDone(termination string) bool     { return termination != "not_started" && termination != "" }
func reviewRoundExhausted(termination string) bool { return termination == "not_started" }

// ---- 摘要与纯函数 ----

// PublicIssues 裁剪为面向用户的稳定问题列表。
func PublicIssues(issues []map[string]any) []map[string]any {
	byKey := map[string]map[string]any{}
	var order []string
	for _, issue := range issues {
		key, ok := issue["issue_key"].(string)
		if !ok || key == "" {
			continue
		}
		ch, okCh := StrictInt(issue["chapter"])
		idx, okIdx := StrictInt(issue["index"])
		if !okCh || !okIdx {
			continue
		}
		if _, dup := byKey[key]; dup {
			continue
		}
		typ, _ := issue["type"].(string)
		detail, _ := issue["detail"].(string)
		suggestion, _ := issue["suggestion"].(string)
		byKey[key] = map[string]any{
			"issue_key": key, "chapter": ch, "index": idx,
			"type": typ, "detail": detail, "suggestion": suggestion,
		}
		order = append(order, key)
	}
	out := make([]map[string]any, 0, len(order))
	sort.SliceStable(order, func(i, j int) bool { return order[i] < order[j] })
	for _, key := range order {
		out = append(out, byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, _ := out[i]["chapter"].(int)
		cj, _ := out[j]["chapter"].(int)
		if ci != cj {
			return ci < cj
		}
		ii, _ := out[i]["index"].(int)
		jj, _ := out[j]["index"].(int)
		if ii != jj {
			return ii < jj
		}
		return out[i]["issue_key"].(string) < out[j]["issue_key"].(string)
	})
	return out
}

// reviewOverlayDigest 影子覆盖指纹（无进展/振荡检测）。
func reviewOverlayDigest(chapters []*ingest.Chapter, overrides map[[2]int]string) string {
	var rows []any
	for _, ch := range chapters {
		for _, seg := range ch.TextSegments() {
			target := seg.TargetText()
			if override, ok := overrides[[2]int{ch.Index, seg.Index}]; ok {
				target = override
			}
			rows = append(rows, []any{ch.Index, seg.Index, target})
		}
	}
	sum := sha256.Sum256([]byte(CompactJSON(rows)))
	return hex.EncodeToString(sum[:])
}

// reviewContentDigest 本次 Review 读取的正式正文指纹。
func reviewContentDigest(chapters []*ingest.Chapter) string {
	var rows []any
	for _, ch := range chapters {
		for _, seg := range ch.TextSegments() {
			anchor := ""
			if seg.Anchor != nil {
				anchor = *seg.Anchor
			}
			rows = append(rows, []any{ch.Index, seg.Index, anchor, seg.Kind, seg.Source, seg.TargetText()})
		}
	}
	sum := sha256.Sum256([]byte(CompactJSON(rows)))
	return hex.EncodeToString(sum[:])
}

// reviewNetChanges 把影子补丁折叠成每段一条修改建议。
func reviewNetChanges(chapters []*ingest.Chapter, overrides map[[2]int]string,
	patchRecords []map[string]any, activePatches map[[2]int]map[string]any) []map[string]any {
	baseline := map[[2]int]string{}
	for _, ch := range chapters {
		for _, seg := range ch.TextSegments() {
			baseline[[2]int{ch.Index, seg.Index}] = seg.TargetText()
		}
	}
	keysByLocation := map[[2]int]map[string]bool{}
	for _, record := range patchRecords {
		if status, _ := record["status"].(string); status == "rejected_cycle" {
			continue
		}
		ch, okCh := StrictInt(record["chapter"])
		idx, okIdx := StrictInt(record["index"])
		if !okCh || !okIdx {
			continue
		}
		loc := [2]int{ch, idx}
		if keysByLocation[loc] == nil {
			keysByLocation[loc] = map[string]bool{}
		}
		if arr, ok := record["issue_keys"].([]string); ok {
			for _, k := range arr {
				if k != "" {
					keysByLocation[loc][k] = true
				}
			}
		}
	}
	var locations [][2]int
	for loc := range overrides {
		locations = append(locations, loc)
	}
	sort.Slice(locations, func(i, j int) bool {
		if locations[i][0] != locations[j][0] {
			return locations[i][0] < locations[j][0]
		}
		return locations[i][1] < locations[j][1]
	})
	var changes []map[string]any
	for _, loc := range locations {
		suggested := overrides[loc]
		if baseline[loc] == suggested {
			continue
		}
		var keys []string
		for k := range keysByLocation[loc] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		reviewResult := "provisional"
		if record, ok := activePatches[loc]; ok {
			if status, ok := record["status"].(string); ok && status != "" {
				reviewResult = status
			}
		}
		changes = append(changes, map[string]any{
			"chapter": loc[0], "index": loc[1], "suggested_target": suggested,
			"issue_keys": keys, "review_result": reviewResult,
		})
	}
	return changes
}

// effectiveIssues 合并本轮问题与 blocked_issues。
func effectiveIssues(current []map[string]any, blocked map[string]map[string]any) []map[string]any {
	currentKeys := map[string]bool{}
	out := make([]map[string]any, 0, len(current)+len(blocked))
	for _, issue := range current {
		key, _ := issue["issue_key"].(string)
		if key != "" {
			currentKeys[key] = true
			if b, ok := blocked[key]; ok {
				enriched := CopyIssue(issue)
				enriched["fix_failure"] = b["fix_failure"]
				out = append(out, enriched)
				continue
			}
		}
		out = append(out, issue)
	}
	for key, issue := range blocked {
		if !currentKeys[key] {
			out = append(out, issue)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, _ := StrictInt(out[i]["chapter"])
		cj, _ := StrictInt(out[j]["chapter"])
		if ci != cj {
			return ci < cj
		}
		ii, _ := StrictInt(out[i]["index"])
		jj, _ := StrictInt(out[j]["index"])
		if ii != jj {
			return ii < jj
		}
		ri, _ := StrictInt(out[i]["review_round"])
		rj, _ := StrictInt(out[j]["review_round"])
		return ri < rj
	})
	return out
}

// registerBlocked 把修复失败的问题登记为 blocked（防假 clean）。
func registerBlocked(blocked map[string]map[string]any, issues []map[string]any, failures []map[string]any) {
	byID := map[string]map[string]any{}
	for _, issue := range issues {
		if id, ok := issue["issue_id"].(string); ok && id != "" {
			byID[id] = issue
		}
	}
	for _, failure := range failures {
		var ids []string
		switch t := failure["issue_ids"].(type) {
		case []string:
			ids = t
		case []any:
			for _, v := range t {
				if s, ok := v.(string); ok {
					ids = append(ids, s)
				}
			}
		}
		for _, id := range ids {
			issue, ok := byID[id]
			if !ok {
				continue
			}
			key, ok := issue["issue_key"].(string)
			if !ok || key == "" {
				continue
			}
			status, _ := failure["status"].(string)
			reason, _ := failure["reason"].(string)
			round, _ := failure["review_round"].(int)
			enriched := CopyIssue(issue)
			enriched["fix_failure"] = map[string]any{
				"status": status, "reason": reason, "review_round": round,
			}
			blocked[key] = enriched
		}
	}
}

// ---- 单轮：初审 + Agent Loop + 仲裁 ----

func runReviewRound(deps SessionDeps, debug *ReviewRunStore, evidence *BookEvidenceIndex,
	loaded []*ingest.Chapter, terms []*glossary.Term, analysis map[string]any,
	reviewRound int, overrides map[[2]int]string, progress Progress,
) (raw []map[string]any, final []map[string]any, err error) {
	label := fmt.Sprintf("全书审校 R%d", reviewRound)
	if reviewRound > 1 {
		label = fmt.Sprintf("全书盲审 R%d", reviewRound)
	}
	callProgress(progress, 0, totalSegments(loaded), label)
	var rawIssues []map[string]any
	done := 0
	for _, chapter := range loaded {
		textSegs := chapter.TextSegments()
		termSnapshot := terms
		if deps.Config.Pipeline.GlossaryScope == "chapter" {
			var parts []string
			for _, s := range textSegs {
				parts = append(parts, s.Source)
			}
			termSnapshot = glossary.TermsIn(terms, strings.Join(parts, "\n"))
		}
		lites := toLites(termSnapshot)
		chunkIssues, err := reviewChapter(deps, debug, evidence, chapter.Index, textSegs, lites, reviewRound, overrides, func(n int) {
			done += n
			callProgress(progress, done, totalSegments(loaded), label)
		})
		if err != nil {
			return nil, nil, err
		}
		for _, issue := range chunkIssues {
			issue["chapter"] = chapter.Index
			issue["stage"] = "review_agent"
			issue["review_round"] = reviewRound
		}
		rawIssues = append(rawIssues, chunkIssues...)
		_ = debug.LogEvent("review_chapter_finished", map[string]any{
			"chapter": chapter.Index, "segment_count": len(textSegs), "issue_count": len(chunkIssues),
		})
	}
	normalized := NormalizeReviewIssues(rawIssues, evidence)
	for _, issue := range normalized {
		id, _ := issue["issue_id"].(string)
		issue["issue_id"] = fmt.Sprintf("r%d-%s", reviewRound, id)
	}
	groups := BuildConflictGroups(normalized)
	var arbitrations []map[string]any
	if len(groups) > 0 {
		if deps.Config.Pipeline.ReviewConflictArbitration {
			callProgress(progress, 0, len(groups), fmt.Sprintf("冲突仲裁 R%d", reviewRound))
			arbiter := NewReviewConflictArbiter(deps.Client, deps.Config, evidence, debug)
			for _, group := range groups {
				arbitrations = append(arbitrations, arbiter.Arbitrate(group))
			}
		} else {
			for _, group := range groups {
				arbitrations = append(arbitrations, map[string]any{
					"conflict_id": group["conflict_id"], "status": "unresolved",
					"recommended_value": "", "reason": "配置已关闭全书冲突仲裁。",
					"supported_issue_ids": []string{}, "rejected_issue_ids": []string{},
				})
			}
		}
	}
	finalIssues, superseded := ApplyReviewArbitrations(normalized, arbitrations)
	// 轮产物
	initialRows, dismissedRows := debug.ResultSnapshots(&reviewRound)
	_ = debug.WriteJSON("initial_issues.json", initialRows)
	_ = debug.WriteJSON("dismissed_issues.json", dismissedRows)
	_ = debug.WriteJSON("pre_arbitration_issues.json", normalized)
	_ = debug.WriteJSON("arbitration_superseded_issues.json", superseded)
	_ = debug.WriteJSON("final_issues.json", finalIssues)
	residual := BuildConflictGroups(finalIssues)
	_ = debug.WriteJSON("residual_conflicts.json", residualConflictsRows(residual))
	_ = debug.WriteJSON("conflicts.json", conflictRecords(groups, arbitrations))
	fallbackCount := 0
	seenChunks := map[string]bool{}
	for _, issue := range normalized {
		if b, _ := issue["agent_fallback"].(bool); b {
			if cid, ok := issue["_chunk_id"].(string); ok && !seenChunks[cid] {
				seenChunks[cid] = true
				fallbackCount++
			}
		}
	}
	_ = debug.LogEvent("review_round_finished", map[string]any{
		"issue_count": len(finalIssues), "conflict_count": len(groups),
		"unresolved_conflict_count": len(residual), "fallback_agent_count": fallbackCount,
	})
	return normalized, finalIssues, nil
}

func residualConflictsRows(groups []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]any{
			"conflict_id": g["conflict_id"], "consistency_key": g["consistency_key"], "issue_ids": g["issue_ids"],
		})
	}
	return out
}

func conflictRecords(groups []map[string]any, arbitrations []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for i, group := range groups {
		var arbitration map[string]any
		if i < len(arbitrations) {
			arbitration = arbitrations[i]
		}
		var proposals []map[string]any
		if issues, ok := group["issues"].([]map[string]any); ok {
			for _, issue := range issues {
				ch, _ := StrictInt(issue["chapter"])
				idx, _ := StrictInt(issue["index"])
				var cons map[string]any
				if c, ok := issue["consistency"].(map[string]any); ok {
					cons = c
				}
				proposed := ""
				if cons != nil {
					proposed, _ = cons["proposed_value"].(string)
				}
				proposals = append(proposals, map[string]any{
					"issue_id": issue["issue_id"], "chapter": ch, "index": idx, "proposed_value": proposed,
				})
			}
		}
		out = append(out, map[string]any{
			"conflict_id": group["conflict_id"], "consistency_key": group["consistency_key"],
			"issue_ids": group["issue_ids"], "proposals": proposals, "arbitration": arbitration,
		})
	}
	return out
}

// reviewChapter 块级审校（拆半重试 + Agent Loop；此处串行实现，保持结果确定性）。
func reviewChapter(deps SessionDeps, debug *ReviewRunStore, evidence *BookEvidenceIndex,
	chapterIndex int, textSegs []*ingest.Segment, terms []agents.GlossaryTermLite,
	reviewRound int, overrides map[[2]int]string, onChunkFinished func(int),
) ([]map[string]any, error) {
	budget := deps.Config.Segment.MaxCharsPerBatch * 3
	chunks := packContiguous(textSegs, budget)
	reviewer := NewReviewer(deps.Client, deps.Config)
	var results [][]map[string]any
	base := 0
	for _, chunk := range chunks {
		mapped, err := reviewAdaptive(reviewer, debug, evidence, deps, chapterIndex, base, chunk, terms, reviewRound, overrides, 1)
		if err != nil {
			return nil, err
		}
		results = append(results, mapped)
		onChunkFinished(len(chunk))
		base += len(chunk)
	}
	var out []map[string]any
	for _, chunkIssues := range results {
		out = append(out, chunkIssues...)
	}
	return out, nil
}

func reviewAdaptive(reviewer *Reviewer, debug *ReviewRunStore, evidence *BookEvidenceIndex,
	deps SessionDeps, chapterIndex, chunkBase int, chunk []*ingest.Segment,
	terms []agents.GlossaryTermLite, reviewRound int, overrides map[[2]int]string, attempt int,
) ([]map[string]any, error) {
	mapped, err := reviewOnce(reviewer, debug, evidence, deps, chapterIndex, chunkBase, chunk, terms, reviewRound, overrides, attempt)
	if err == nil {
		return mapped, nil
	}
	if _, isProtocol := err.(*ReviewOutputError); !isProtocol {
		return nil, err // 服务异常上抛
	}
	if len(chunk) > 1 {
		mid := len(chunk) / 2
		left, errL := reviewAdaptive(reviewer, debug, evidence, deps, chapterIndex, chunkBase, chunk[:mid], terms, reviewRound, overrides, 1)
		if errL != nil {
			return nil, errL
		}
		right, errR := reviewAdaptive(reviewer, debug, evidence, deps, chapterIndex, chunkBase+mid, chunk[mid:], terms, reviewRound, overrides, 1)
		if errR != nil {
			return nil, errR
		}
		return append(left, right...), nil
	}
	retries := deps.Config.Pipeline.ReviewOutputRetries
	var lastErr error
	for retry := 1; retry <= retries; retry++ {
		mapped, err := reviewOnce(reviewer, debug, evidence, deps, chapterIndex, chunkBase, chunk, terms, reviewRound, overrides, retry+1)
		if err == nil {
			return mapped, nil
		}
		if _, isProtocol := err.(*ReviewOutputError); !isProtocol {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func reviewOnce(reviewer *Reviewer, debug *ReviewRunStore, evidence *BookEvidenceIndex,
	deps SessionDeps, chapterIndex, chunkBase int, chunk []*ingest.Segment,
	terms []agents.GlossaryTermLite, reviewRound int, overrides map[[2]int]string, attempt int,
) ([]map[string]any, error) {
	srcs := make([]string, len(chunk))
	tgts := make([]string, len(chunk))
	for i, seg := range chunk {
		srcs[i] = seg.Source
		target := seg.TargetText()
		if chapterIndex >= 0 {
			if override, ok := overrides[[2]int{chapterIndex, chunkBase + i}]; ok {
				target = override
			}
		}
		tgts[i] = target
	}
	roundPrefix := ""
	if reviewRound > 0 {
		roundPrefix = fmt.Sprintf("r%d-", reviewRound)
	}
	initialID := fmt.Sprintf("%sinitial-ch%d-base%d-n%d-attempt%d", roundPrefix, chapterIndex, chunkBase, len(chunk), attempt)
	var traceRows []map[string]any
	trace := TraceFn(nil)
	if debug != nil {
		trace = func(event string, data map[string]any) {
			traceRows = append(traceRows, map[string]any{"event": event, "data": data})
		}
	}
	result, err := reviewer.ReviewResult(srcs, tgts, terms, trace)
	if debug != nil {
		traceDoc := map[string]any{
			"initial_id": initialID, "stage": "Reviewer", "status": "finished",
			"turns": traceRows,
		}
		if err != nil {
			traceDoc["status"] = "failed"
			traceDoc["error"] = err.Error()
		}
		_ = debug.WriteJSON("initial/"+safeID(initialID)+".json", traceDoc)
	}
	if err != nil {
		return nil, err
	}
	localIssues := make([]map[string]any, 0, len(result.Issues))
	for _, issue := range result.Issues {
		idx, ok := StrictInt(issue["index"])
		if !ok || idx < 0 || idx >= len(chunk) {
			return nil, reviewErr("invalid_issue_index")
		}
		issue["index"] = idx
		localIssues = append(localIssues, issue)
	}
	if debug != nil {
		debug.RecordInitialIssues(chapterIndex, chunkBase, localIssues)
	}
	// 块级 Agent Loop
	fallbackReason := ""
	if len(localIssues) > 0 && evidence != nil && debug != nil && deps.Config.Pipeline.ReviewAgentLoop && chapterIndex >= 0 {
		loop := NewReviewAgentLoop(deps.Client, deps.Config, evidence, debug)
		rr := reviewRound
		outcome := loop.ReviewChunk(chapterIndex, chunkBase, srcs, tgts, localIssues, &rr)
		localIssues = outcome.Issues
		debug.RecordDismissed(chapterIndex, chunkBase, outcome.Dismissed)
		fallbackReason = outcome.FallbackReason
	}
	chunkID := fmt.Sprintf("%sch%d-base%d-n%d", roundPrefix, chapterIndex, chunkBase, len(chunk))
	mapped := make([]map[string]any, 0, len(localIssues))
	for _, issue := range localIssues {
		idx, ok := StrictInt(issue["index"])
		if !ok || idx < 0 || idx >= len(chunk) {
			continue
		}
		issue["index"] = chunkBase + idx
		issue["_chunk_id"] = chunkID
		if fallbackReason != "" {
			issue["fallback_reason"] = fallbackReason
		}
		mapped = append(mapped, issue)
	}
	return mapped, nil
}

// proposeReviewPatches 影子修订提议。
func proposeReviewPatches(deps SessionDeps, debug *ReviewRunStore, evidence *BookEvidenceIndex,
	terms []*glossary.Term, analysis map[string]any, issues []map[string]any,
	reviewRound, fixRound int, progress Progress,
) ([]*ProvisionalPatch, []map[string]any) {
	type job struct {
		chapter int
		index   int
		issues  []map[string]any
	}
	grouped := map[[2]int]*job{}
	var order [][2]int
	var skipped []map[string]any
	for _, issue := range issues {
		ch, okCh := StrictInt(issue["chapter"])
		idx, okIdx := StrictInt(issue["index"])
		id, okID := issue["issue_id"].(string)
		if !okCh || !okIdx || !okID {
			skipped = append(skipped, map[string]any{"status": "skipped", "reason": "invalid_issue_location"})
			continue
		}
		if arbitration, ok := issue["arbitration"].(map[string]any); ok {
			if status, _ := arbitration["status"].(string); status == "unresolved" {
				skipped = append(skipped, map[string]any{
					"status": "skipped", "reason": "unresolved_consistency_conflict",
					"chapter": ch, "index": idx, "issue_id": id,
				})
				continue
			}
		}
		if deps.Config.Pipeline.ReviewAgentLoop {
			if b, _ := issue["agent_fallback"].(bool); b {
				skipped = append(skipped, map[string]any{
					"status": "skipped", "reason": "unverified_agent_fallback",
					"chapter": ch, "index": idx, "issue_id": id,
				})
				continue
			}
		}
		loc := [2]int{ch, idx}
		if grouped[loc] == nil {
			grouped[loc] = &job{chapter: ch, index: idx}
			order = append(order, loc)
		}
		grouped[loc].issues = append(grouped[loc].issues, issue)
	}
	if len(order) == 0 {
		return nil, skipped
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i][0] != order[j][0] {
			return order[i][0] < order[j][0]
		}
		return order[i][1] < order[j][1]
	})
	callProgress(progress, 0, len(order), fmt.Sprintf("影子修订 R%d", fixRound))
	fixer := NewReviewFixer(deps.Client, deps.Config)
	style := deps.Analyzer.StyleBrief(mapToOMap(analysis))
	bookSynopsis, _ := analysis["book_synopsis"].(string)
	var patches []*ProvisionalPatch
	var failures []map[string]any
	for _, loc := range order {
		j := grouped[loc]
		segment := evidence.SegmentRefLookup(j.chapter, j.index)
		if segment == nil {
			failures = append(failures, map[string]any{
				"status": "failed", "reason": "segment_not_found",
				"chapter": j.chapter, "index": j.index,
			})
			continue
		}
		context := evidence.SegmentContext(map[string]any{
			"chapter": j.chapter, "index": j.index, "before": 4, "after": 4,
		})
		var nearby [][2]string
		contextSource := ""
		if ok, _ := context["ok"].(bool); ok {
			if segs, ok := context["segments"].([]map[string]any); ok {
				for _, s := range segs {
					ref, _ := s["ref"].(string)
					src, _ := s["source"].(string)
					tgt, _ := s["target"].(string)
					if ref != segment.Ref() {
						nearby = append(nearby, [2]string{src, tgt})
					}
					contextSource += src
				}
			}
		}
		if contextSource == "" {
			contextSource = segment.Source
		}
		relevant := glossary.TermsIn(terms, contextSource)
		patchValue, err := fixer.Propose(reviewRound, segment.Ref(), j.chapter, j.index,
			segment.Source, segment.Target, j.issues, FixerOptions{
				Style: style, BookSynopsis: bookSynopsis,
				RelevantGlossary: toLites(relevant), NearbyPairs: nearby,
			})
		patch := &patchValue
		if err != nil {
			reason := err.Error()
			if _, ok := err.(*ReviewFixerProtocolError); !ok {
				reason = errorTypeName(err) + ": " + err.Error()
			}
			failures = append(failures, map[string]any{
				"status": "failed", "reason": reason,
				"chapter": j.chapter, "index": j.index, "segment_ref": segment.Ref(),
			})
			continue
		}
		patches = append(patches, patch)
	}
	return patches, append(skipped, failures...)
}

type fixRoundOptions = FixerOptions

func packContiguous(segs []*ingest.Segment, budget int) [][]*ingest.Segment {
	var out [][]*ingest.Segment
	var cur []*ingest.Segment
	curLen := 0
	for _, seg := range segs {
		l := len([]rune(seg.Source))
		if len(cur) > 0 && curLen+l > budget {
			out = append(out, cur)
			cur = nil
			curLen = 0
		}
		cur = append(cur, seg)
		curLen += l
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func totalSegments(chapters []*ingest.Chapter) int {
	n := 0
	for _, ch := range chapters {
		n += len(ch.TextSegments())
	}
	return n
}

func toLites(terms []*glossary.Term) []agents.GlossaryTermLite {
	lites := make([]agents.GlossaryTermLite, len(terms))
	for i, t := range terms {
		lites[i] = agents.GlossaryTermLite{
			Source: t.Source, Target: t.Target, Reading: t.Reading,
			Type: t.Type, Gender: t.Gender, Aliases: t.Aliases,
		}
	}
	return lites
}

func mapToOMap(m map[string]any) *jsonx.OMap {
	om := jsonx.NewOMap()
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		om.Set(k, m[k])
	}
	return om
}

func callProgress(p Progress, done, total int, label string) {
	if p != nil {
		p(done, total, label)
	}
}

var _ = sync.Mutex{}
