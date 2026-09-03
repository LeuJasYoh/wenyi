package review

// review_fixer.go 对应 agents/review_fixer.py（主规格 §12.5 + 分册 06 §2）。
// 只读单段临时替换候选：从不写正式正文，落地前由会话按 before_hash 乐观校验。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// fixerOutputFields 模型输出必须恰好五键。
var fixerOutputFields = map[string]bool{
	"segment_ref": true, "before_hash": true, "issue_ids": true, "replacement": true, "complete": true,
}

// ReviewFixerProtocolError 消息即 reason 短码（Python 纯 ValueError 子类）。
type ReviewFixerProtocolError struct{ Reason string }

func (e *ReviewFixerProtocolError) Error() string { return e.Reason }

func fixerErr(reason string) *ReviewFixerProtocolError { return &ReviewFixerProtocolError{Reason: reason} }

// ProvisionalPatch 冻结值对象（主规格 §12.5）。
type ProvisionalPatch struct {
	PatchID     string
	Round       int
	SegmentRef  string
	Chapter     int
	Index       int
	BeforeHash  string
	Before      string
	After       string
	IssueIDs    []string
	Status      string // 恒 "provisional"
}

// AsDict 稳定序列化表示。
func (p *ProvisionalPatch) AsDict() map[string]any {
	return map[string]any{
		"patch_id":    p.PatchID,
		"round":       p.Round,
		"segment_ref": p.SegmentRef,
		"chapter":     p.Chapter,
		"index":       p.Index,
		"before_hash": p.BeforeHash,
		"before":      p.Before,
		"after":       p.After,
		"issue_ids":   append([]string(nil), p.IssueIDs...),
		"status":      p.Status,
	}
}

// TargetHash sha256（strip 前）——落地前乐观校验用。
func TargetHash(target string) string {
	sum := sha256.Sum256([]byte(target))
	return hex.EncodeToString(sum[:])
}

// PatchID 对应 _patch_id：round 两位补零 + 载荷（sort_keys 紧凑 JSON）哈希前 16 位。
func PatchID(roundNumber int, segmentRef, beforeHash, after string, issueIDs []string) string {
	payload := CompactJSON(map[string]any{
		"round":       roundNumber,
		"segment_ref": segmentRef,
		"before_hash": beforeHash,
		"after":       after,
		"issue_ids":   issueIDs,
	})
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("patch-r%02d-%s", roundNumber, hex.EncodeToString(sum[:])[:16])
}

// FixerOptions propose 的可选上下文。
type FixerOptions struct {
	Style          string
	BookSynopsis   string
	ChapterDigest  string
	RelevantGlossary []agents.GlossaryTermLite
	GlossaryText   *string // 预本地化只读文本（对应 str 分支）
	NearbyPairs    [][2]string
	Trace          TraceFn
}

// ReviewFixer tier=config.pipeline.review_agent_tier，stage="ReviewFixer"。
type ReviewFixer struct {
	agents.Agent
}

func NewReviewFixer(client llm.LLMClient, cfg *config.Config) *ReviewFixer {
	f := &ReviewFixer{}
	f.Agent = agents.NewAgent(client, cfg, "ReviewFixer")
	return f
}

func orNoneText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return "（无）"
	}
	return t
}

func nearbyText(pairs [][2]string) string {
	if len(pairs) == 0 {
		return "（无）"
	}
	var lines []string
	for i, pair := range pairs {
		lines = append(lines, fmt.Sprintf("[上下文 %d] 原文：%s\n    译文：%s", i, pair[0], pair[1]))
	}
	return strings.Join(lines, "\n")
}

// cleanFixerIssues 对应 _issues：问题归属与指导字段校验，生成提示载荷。
func cleanFixerIssues(issues []map[string]any, chapter, index int) ([]string, []map[string]any, error) {
	if len(issues) == 0 {
		return nil, nil, fixerErr("issues_required")
	}
	seen := map[string]bool{}
	var ids []string
	var payload []map[string]any
	for _, issue := range issues {
		if issue == nil {
			return nil, nil, fixerErr("invalid_issue")
		}
		idRaw, ok1 := issue["issue_id"].(string)
		detailRaw, ok2 := issue["detail"].(string)
		suggestionRaw, ok3 := issue["suggestion"].(string)
		if !ok1 || !ok2 || !ok3 ||
			strings.TrimSpace(idRaw) == "" || strings.TrimSpace(detailRaw) == "" || strings.TrimSpace(suggestionRaw) == "" {
			return nil, nil, fixerErr("invalid_issue")
		}
		id := strings.TrimSpace(idRaw)
		if seen[id] {
			return nil, nil, fixerErr("duplicate_issue_id")
		}
		seen[id] = true
		if v, ok := issue["chapter"]; ok {
			if c, valid := StrictInt(v); !valid || c != chapter {
				return nil, nil, fixerErr("issue_location_mismatch")
			}
		}
		if v, ok := issue["index"]; ok {
			if i, valid := StrictInt(v); !valid || i != index {
				return nil, nil, fixerErr("issue_location_mismatch")
			}
		}
		ids = append(ids, id)
		issueType := ""
		if t, ok := issue["type"].(string); ok {
			issueType = t
		}
		item := map[string]any{
			"issue_id":   id,
			"type":       issueType,
			"detail":     strings.TrimSpace(detailRaw),
			"suggestion": strings.TrimSpace(suggestionRaw),
		}
		// 键存在即透传（不校验内部结构）
		for _, field := range []string{"consistency", "arbitration", "evidence_refs"} {
			if v, ok := issue[field]; ok {
				item[field] = v
			}
		}
		payload = append(payload, item)
	}
	return ids, payload, nil
}

// Propose 输入校验 → 渲染 → 调用 → 输出协议校验 → ProvisionalPatch。
func (f *ReviewFixer) Propose(roundNumber int, segmentRef string, chapter, index int,
	source, currentTarget string, issues []map[string]any, opts FixerOptions) (ProvisionalPatch, error) {
	// 1. 输入校验（顺序）
	if roundNumber < 1 {
		return ProvisionalPatch{}, fixerErr("invalid_round")
	}
	ref := strings.TrimSpace(segmentRef)
	if segmentRef == "" || ref == "" {
		return ProvisionalPatch{}, fixerErr("invalid_segment_ref")
	}
	if chapter < 0 {
		return ProvisionalPatch{}, fixerErr("invalid_chapter")
	}
	if index < 0 {
		return ProvisionalPatch{}, fixerErr("invalid_index")
	}
	if strings.TrimSpace(source) == "" {
		return ProvisionalPatch{}, fixerErr("empty_source")
	}
	if strings.TrimSpace(currentTarget) == "" {
		return ProvisionalPatch{}, fixerErr("empty_current_target")
	}
	// 2. 问题清洗
	issueIDs, issuePayload, err := cleanFixerIssues(issues, chapter, index)
	if err != nil {
		return ProvisionalPatch{}, err
	}
	beforeHash := TargetHash(currentTarget)
	// 3. 渲染 system（显式传 lang_guidance 带敬称策略）
	system := agents.Render("review_fixer_system", agents.RenderOptions{
		Src: f.Src, Tgt: f.Tgt,
		Overrides: map[string]string{
			"lang_guidance": agents.TranslateGuidance(f.Src, f.Config.HonorificStrategy),
		},
	})
	// 4. 渲染 user
	glossary := ""
	if opts.GlossaryText != nil {
		glossary = orNoneText(*opts.GlossaryText)
	} else {
		glossary = agents.RenderGlossary(opts.RelevantGlossary)
	}
	nearby := nearbyText(opts.NearbyPairs)
	user := agents.Render("review_fixer_user", agents.RenderOptions{
		Src: f.Src, Tgt: f.Tgt,
		Vars: map[string]string{
			"style":          orNoneText(opts.Style),
			"book_synopsis":  orNoneText(opts.BookSynopsis),
			"chapter_digest": orNoneText(opts.ChapterDigest),
			"glossary":       glossary,
			"nearby_pairs":   nearby,
			"issues_json":    Indent2JSON(issuePayload),
			"segment_ref":    ref,
			"before_hash":    beforeHash,
			"issue_ids_json": CompactJSON(issueIDs),
			"source":         source,
			"current_target": currentTarget,
		},
	})
	messages := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	if opts.Trace != nil {
		opts.Trace("request", map[string]any{"messages": messagesToRows(messages)})
	}
	raw, err := f.Client.Complete(messages, f.Config.Pipeline.ReviewAgentTier, true, nil, f.StageName)
	if err != nil {
		if opts.Trace != nil {
			opts.Trace("error", map[string]any{"error_type": errorTypeName(err), "error": err.Error()})
		}
		return ProvisionalPatch{}, err
	}
	if opts.Trace != nil {
		opts.Trace("response", map[string]any{"raw_response": raw})
	}
	value, repaired, perr := llm.ParseJSONResult(raw)
	if perr != nil {
		return ProvisionalPatch{}, fixerErr("malformed_json")
	}
	if opts.Trace != nil {
		opts.Trace("parsed", map[string]any{"value": value, "json_repaired": repaired})
	}
	data, ok := value.(*jsonx.OMap)
	if !ok {
		return ProvisionalPatch{}, fixerErr("response_not_object")
	}
	// 5. 输出协议校验（顺序严格）
	if data.Len() != len(fixerOutputFields) {
		return ProvisionalPatch{}, fixerErr("unexpected_fields")
	}
	for _, k := range data.Keys() {
		if !fixerOutputFields[k] {
			return ProvisionalPatch{}, fixerErr("unexpected_fields")
		}
	}
	if data.Len() == 0 || data.LastKeys(1)[0] != "complete" {
		return ProvisionalPatch{}, fixerErr("completion_marker_not_last")
	}
	completeRaw, _ := data.Get("complete")
	if complete, isBool := completeRaw.(bool); !isBool || !complete {
		return ProvisionalPatch{}, fixerErr("completion_marker_missing")
	}
	returnedRef, _ := data.Get("segment_ref")
	returnedRefStr, _ := returnedRef.(string)
	if returnedRefStr != ref {
		return ProvisionalPatch{}, fixerErr("segment_ref_mismatch")
	}
	returnedHash, _ := data.Get("before_hash")
	returnedHashStr, _ := returnedHash.(string)
	if returnedHashStr != beforeHash {
		return ProvisionalPatch{}, fixerErr("before_hash_mismatch")
	}
	returnedIDsRaw, _ := data.Get("issue_ids")
	returnedIDsAny, isList := returnedIDsRaw.([]any)
	if !isList {
		return ProvisionalPatch{}, fixerErr("invalid_issue_ids")
	}
	returnedIDs := make([]string, 0, len(returnedIDsAny))
	returnedSet := map[string]bool{}
	for _, v := range returnedIDsAny {
		s, isStr := v.(string)
		if !isStr || returnedSet[s] {
			return ProvisionalPatch{}, fixerErr("invalid_issue_ids")
		}
		returnedSet[s] = true
		returnedIDs = append(returnedIDs, s)
	}
	inputSet := map[string]bool{}
	for _, id := range issueIDs {
		inputSet[id] = true
	}
	if len(returnedSet) != len(inputSet) {
		return ProvisionalPatch{}, fixerErr("issue_ids_mismatch")
	}
	for id := range returnedSet {
		if !inputSet[id] {
			return ProvisionalPatch{}, fixerErr("issue_ids_mismatch")
		}
	}
	replacementRaw, _ := data.Get("replacement")
	replacement, isStr := replacementRaw.(string)
	if !isStr || strings.TrimSpace(replacement) == "" {
		return ProvisionalPatch{}, fixerErr("empty_replacement")
	}
	replacement = strings.TrimSpace(replacement)
	if replacement == strings.TrimSpace(currentTarget) {
		return ProvisionalPatch{}, fixerErr("unchanged_replacement")
	}
	// 6. 构造补丁（issue_ids 用输入顺序）
	return ProvisionalPatch{
		PatchID:    PatchID(roundNumber, ref, beforeHash, replacement, issueIDs),
		Round:      roundNumber,
		SegmentRef: ref,
		Chapter:    chapter,
		Index:      index,
		BeforeHash: beforeHash,
		Before:     currentTarget,
		After:      replacement,
		IssueIDs:   issueIDs,
		Status:     "provisional",
	}, nil
}
