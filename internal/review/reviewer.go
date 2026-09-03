package review

// reviewer.go 对应 agents/reviewer.py 的 Reviewer（主规格 §12.4 + 分册 06 §1）。

import (
	"fmt"
	"strconv"
	"strings"

	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// ReviewOutputError 只对"缩小输入可恢复"的结构化输出错误抛出；服务异常直接重抛。
type ReviewOutputError struct{ Reason string }

func (e *ReviewOutputError) Error() string { return "审校输出协议错误：" + e.Reason }

func reviewErr(reason string) *ReviewOutputError { return &ReviewOutputError{Reason: reason} }

// ReviewResult 一次审校调用的结构化结果（repaired 来自宽松 JSON 修复）。
type ReviewResult struct {
	Issues   []map[string]any
	Repaired bool
}

// TraceFn 审校 trace 回调（event → data 合并写盘）。
type TraceFn func(event string, data map[string]any)

// Reviewer 廉价初审（tier=cheap，stage="Reviewer"）。
type Reviewer struct {
	agents.Agent
}

func NewReviewer(client llm.LLMClient, cfg *config.Config) *Reviewer {
	r := &Reviewer{}
	r.Agent = agents.NewAgent(client, cfg, "Reviewer")
	return r
}

// ReviewResult 渲染 reviewer prompt → cheap 档 json 调用 → 严格按序校验（键序用 OMap）。
func (r *Reviewer) ReviewResult(sources, targets []string, glossaryTerms []agents.GlossaryTermLite, trace TraceFn) (*ReviewResult, error) {
	if len(sources) == 0 {
		return &ReviewResult{Issues: []map[string]any{}}, nil
	}
	if glossaryTerms == nil {
		glossaryTerms = []agents.GlossaryTermLite{}
	}
	system := agents.Render("reviewer_system", agents.RenderOptions{Src: r.Src, Tgt: r.Tgt})
	user := agents.Render("reviewer_user", agents.RenderOptions{
		Src: r.Src, Tgt: r.Tgt,
		Vars: map[string]string{
			"glossary": agents.RenderGlossary(glossaryTerms),
			"n":        strconv.Itoa(len(sources)),
			"pairs":    agents.NumberedPairs(sources, targets),
		},
	})
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	if trace != nil {
		trace("request", map[string]any{"messages": messagesToRows(messages)})
	}
	text, err := r.Client.Complete(messages, "cheap", true, nil, r.StageName)
	if err != nil {
		if trace != nil {
			trace("error", map[string]any{"error_type": errorTypeName(err), "error": err.Error()})
		}
		return nil, err // 服务异常不降级
	}
	if trace != nil {
		trace("response", map[string]any{"raw_response": text})
	}
	value, repaired, perr := llm.ParseJSONResult(text)
	if perr != nil {
		return nil, reviewErr("malformed_json")
	}
	if trace != nil {
		trace("parsed", map[string]any{"value": value, "json_repaired": repaired})
	}
	data, ok := value.(*jsonx.OMap)
	if !ok {
		return nil, reviewErr("response_not_object")
	}
	last := data.LastKeys(2)
	if len(last) != 2 || last[0] != "reviewed_segments" || last[1] != "complete" {
		return nil, reviewErr("completion_footer_not_last")
	}
	reviewedRaw, _ := data.Get("reviewed_segments")
	reviewed, ok := StrictInt(reviewedRaw)
	if !ok || reviewed != len(sources) {
		return nil, reviewErr("reviewed_segments_mismatch")
	}
	completeRaw, _ := data.Get("complete")
	if complete, isBool := completeRaw.(bool); !isBool || !complete {
		return nil, reviewErr("completion_marker_missing")
	}
	issuesRaw, _ := data.Get("issues")
	items, ok := issuesRaw.([]any)
	if !ok {
		return nil, reviewErr("issues_not_list")
	}
	issueMaps := make([]*jsonx.OMap, 0, len(items))
	for _, item := range items {
		om, isObj := item.(*jsonx.OMap)
		if !isObj {
			return nil, reviewErr("issue_not_object")
		}
		issueMaps = append(issueMaps, om)
	}
	validated, err := ValidateReviewIssues(issueMaps, len(sources))
	if err != nil {
		return nil, err
	}
	return &ReviewResult{Issues: validated, Repaired: repaired}, nil
}

func messagesToRows(messages []llm.Message) []map[string]any {
	rows := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		rows = append(rows, map[string]any{"role": m.Role, "content": m.Content})
	}
	return rows
}

var reviewerIssueTypes = map[string]bool{
	"missing": true, "added": true, "mistranslation": true, "terminology": true, "pronoun": true,
}

// ValidateReviewIssues 对应 _validate_issues：宁可抛协议错误也不静默当成无问题；
// 返回清洗后的四键 {index,type,detail,suggestion}。
func ValidateReviewIssues(issues []*jsonx.OMap, segmentCount int) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(issues))
	for _, item := range issues {
		idxRaw, _ := item.Get("index")
		idx, ok := LooseInt(idxRaw)
		if !ok || idx < 0 || idx >= segmentCount {
			return nil, reviewErr("invalid_issue_index")
		}
		typeRaw, _ := item.Get("type")
		issueType, isStr := typeRaw.(string)
		if !isStr || !reviewerIssueTypes[issueType] {
			return nil, reviewErr("invalid_issue_type")
		}
		detailRaw, _ := item.Get("detail")
		suggestionRaw, _ := item.Get("suggestion")
		detail, dOk := detailRaw.(string)
		if !dOk || strings.TrimSpace(detail) == "" {
			return nil, reviewErr("invalid_issue_detail")
		}
		suggestion, sOk := suggestionRaw.(string)
		if !sOk || strings.TrimSpace(suggestion) == "" {
			return nil, reviewErr("invalid_issue_suggestion")
		}
		out = append(out, map[string]any{
			"index":      idx,
			"type":       issueType,
			"detail":     strings.TrimSpace(detail),
			"suggestion": strings.TrimSpace(suggestion),
		})
	}
	return out, nil
}

// Review 便捷包装（测试用）：review_result().issues。
func (r *Reviewer) Review(sources, targets []string, glossaryTerms []agents.GlossaryTermLite) ([]map[string]any, error) {
	res, err := r.ReviewResult(sources, targets, glossaryTerms, nil)
	if err != nil {
		return nil, err
	}
	return res.Issues, nil
}

// errorTypeName 取错误的"类名"（模拟 Python type(error).__name__，供 result.json error.type 与 trace）。
func errorTypeName(err error) string {
	name := fmt.Sprintf("%T", err)
	name = strings.TrimPrefix(name, "*")
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return name
}
