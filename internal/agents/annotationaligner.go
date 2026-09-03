package agents

// annotation_aligner.go EPUB 注释对齐（主规格 §10 + 分册 05 §10）。
// 目标文本不可变；模型只插入合成标记 ⟪id⟫ / ⟪id:S⟫ / ⟪id:E⟫；
// 校验失败回退确定性位置（比例映射 / 段尾），绝不改译文。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// AnnotationAlignmentError 错误消息是固定短码（供上层诊断与测试断言）。
type AnnotationAlignmentError struct{ Code string }

func (e *AnnotationAlignmentError) Error() string { return e.Code }

func annErr(code string) *AnnotationAlignmentError { return &AnnotationAlignmentError{Code: code} }

var markerRe = regexp.MustCompile(`⟪([^⟪⟫]+)⟫`)

const alignerSystemPrompt = `You align EPUB annotation markers between a source passage and its completed translation.
The target text is immutable. You may ONLY insert the supplied markers into it:
- never add, remove, replace, reorder, normalize, or re-punctuate any target character;
- preserve every whitespace and line-break character exactly;
- insert every supplied marker exactly once and do not invent markers;
- a range marker :S must occur before its matching :E marker;
- ranges must be nested or disjoint, never crossing.

Return JSON only. Preserve every input unit_id exactly and return one output item for every input unit:
{"items":[{"unit_id":"the unchanged unit id","marked_target":"the exact target with markers inserted"}]}`

// AnnotationUnit 一个逻辑 EPUB 文本块及其注释。
type AnnotationUnit struct {
	UnitID string
	Source string
	Target string
	Items  []map[string]any
}

// AnnotationAlignment 已验证的目标偏移。
type AnnotationAlignment struct {
	UnitID       string
	TargetDigest string
	Placements   []map[string]any
	UsedFallback bool
}

// TargetDigest sha256 hex（检测过期对齐元数据）。
func TargetDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func itemID(item map[string]any) (string, error) {
	v, _ := item["id"]
	s, ok := v.(string)
	if !ok || s == "" || strings.Contains(s, "⟪") || strings.Contains(s, "⟫") {
		return "", annErr("invalid_annotation_id")
	}
	return s, nil
}

func itemMode(item map[string]any) (string, error) {
	v, _ := item["mode"]
	s, ok := v.(string)
	if !ok || (s != "point" && s != "range") {
		return "", annErr("invalid_annotation_mode")
	}
	return s, nil
}

func sourceOffset(item map[string]any, field string, sourceLength int) (int, error) {
	v, _ := item[field]
	i, ok := v.(int)
	if !ok {
		// bool 视为非法
		if _, isBool := v.(bool); isBool {
			return 0, annErr("invalid_" + field)
		}
		if f, isFloat := v.(float64); isFloat && f == float64(int(f)) {
			i = int(f)
		} else {
			return 0, annErr("invalid_" + field)
		}
	}
	if i < 0 || i > sourceLength {
		return 0, annErr(fmt.Sprintf("out_of_bounds_%s", field))
	}
	return i, nil
}

// MarkerTokens point → (⟪id⟫)；range → (⟪id:S⟫, ⟪id:E⟫)。
func MarkerTokens(item map[string]any) ([]string, error) {
	id, err := itemID(item)
	if err != nil {
		return nil, err
	}
	mode, err := itemMode(item)
	if err != nil {
		return nil, err
	}
	if mode == "point" {
		return []string{"⟪" + id + "⟫"}, nil
	}
	return []string{"⟪" + id + ":S⟫", "⟪" + id + ":E⟫"}, nil
}

// BuildMarkedSource 在源文本插入合成标记。
func BuildMarkedSource(unit AnnotationUnit) (string, error) {
	sourceLength := len([]rune(unit.Source))
	insertions := map[int][][2]interface{}{} // offset → [(sortKey, marker)]
	seen := map[string]bool{}
	for ordinal, item := range unit.Items {
		id, err := itemID(item)
		if err != nil {
			return "", err
		}
		if seen[id] {
			return "", annErr("duplicate_annotation_id")
		}
		seen[id] = true
		mode, err := itemMode(item)
		if err != nil {
			return "", err
		}
		start, err := sourceOffset(item, "source_start", sourceLength)
		if err != nil {
			return "", err
		}
		end, err := sourceOffset(item, "source_end", sourceLength)
		if err != nil {
			return "", err
		}
		if mode == "point" {
			if end != start {
				return "", annErr("point_has_nonzero_range")
			}
			insertions[start] = append(insertions[start], [2]interface{}{10000 + ordinal, "⟪" + id + "⟫"})
		} else {
			if start > end {
				return "", annErr("reversed_source_range")
			}
			insertions[start] = append(insertions[start], [2]interface{}{20000 - end, "⟪" + id + ":S⟫"})
			insertions[end] = append(insertions[end], [2]interface{}{-20000 - start, "⟪" + id + ":E⟫"})
		}
	}
	var parts []string
	runes := []rune(unit.Source)
	for offset := 0; offset <= sourceLength; offset++ {
		markers := insertions[offset]
		sort.SliceStable(markers, func(i, j int) bool {
			return markers[i][0].(int) < markers[j][0].(int)
		})
		for _, m := range markers {
			parts = append(parts, m[1].(string))
		}
		if offset < sourceLength {
			parts = append(parts, string(runes[offset]))
		}
	}
	return strings.Join(parts, ""), nil
}

func expectedMarkers(unit AnnotationUnit) (map[string]string, []string, error) {
	payloadToID := map[string]string{}
	var expected []string
	seen := map[string]bool{}
	for _, item := range unit.Items {
		id, err := itemID(item)
		if err != nil {
			return nil, nil, err
		}
		if seen[id] {
			return nil, nil, annErr("duplicate_annotation_id")
		}
		seen[id] = true
		tokens, err := MarkerTokens(item)
		if err != nil {
			return nil, nil, err
		}
		for _, marker := range tokens {
			payload := marker[len("⟪") : len(marker)-len("⟫")]
			if _, exists := payloadToID[payload]; exists {
				return nil, nil, annErr("duplicate_marker_payload")
			}
			payloadToID[payload] = id
			expected = append(expected, payload)
		}
	}
	return payloadToID, expected, nil
}

// ValidateMarkedTarget 局部校验 + 偏移换算（核心）。
func ValidateMarkedTarget(unit AnnotationUnit, markedTarget any) ([]map[string]any, error) {
	s, ok := markedTarget.(string)
	if !ok {
		return nil, annErr("marked_target_not_string")
	}
	_, expected, err := expectedMarkers(unit)
	if err != nil {
		return nil, err
	}
	locMatches := markerRe.FindAllStringSubmatchIndex(s, -1)
	actual := make([]string, len(locMatches))
	for i, loc := range locMatches {
		actual[i] = s[loc[2]:loc[3]]
	}
	if len(actual) != len(expected) {
		return nil, annErr("marker_count_mismatch")
	}
	actualSet := map[string]bool{}
	for _, a := range actual {
		actualSet[a] = true
	}
	expectedSet := map[string]bool{}
	for _, e := range expected {
		expectedSet[e] = true
	}
	for _, a := range actual {
		if !expectedSet[a] {
			return nil, annErr("unknown_marker")
		}
	}
	for _, e := range expected {
		if !actualSet[e] {
			return nil, annErr("missing_marker")
		}
	}
	counts := map[string]int{}
	for _, a := range actual {
		counts[a]++
	}
	for _, e := range expected {
		if counts[e] != 1 {
			return nil, annErr("duplicate_marker")
		}
	}
	stripped := markerRe.ReplaceAllString(s, "")
	if stripped != unit.Target {
		return nil, annErr("target_was_modified")
	}
	// 偏移换算：标记相对位置 → 去掉前置标记后的目标字符偏移（码点）
	offsets := map[string]int{}
	removedLength := 0
	for _, loc := range locMatches {
		payload := s[loc[2]:loc[3]]
		offsets[payload] = len([]rune(s[:loc[0]])) - removedLength
		removedLength += len([]rune(s[loc[0]:loc[1]]))
	}
	// 范围嵌套校验
	var rangeStack []string
	rangeIDs := map[string]bool{}
	for _, item := range unit.Items {
		if mode, err := itemMode(item); err == nil && mode == "range" {
			if id, err := itemID(item); err == nil {
				rangeIDs[id] = true
			}
		}
	}
	for _, loc := range locMatches {
		payload := s[loc[2]:loc[3]]
		if strings.HasSuffix(payload, ":S") {
			id := payload[:len(payload)-2]
			if !rangeIDs[id] {
				return nil, annErr("unexpected_range_start")
			}
			rangeStack = append(rangeStack, id)
		} else if strings.HasSuffix(payload, ":E") {
			id := payload[:len(payload)-2]
			if len(rangeStack) == 0 || rangeStack[len(rangeStack)-1] != id {
				return nil, annErr("crossing_or_reversed_range")
			}
			rangeStack = rangeStack[:len(rangeStack)-1]
		}
	}
	if len(rangeStack) > 0 {
		return nil, annErr("unclosed_range")
	}
	var placements []map[string]any
	for _, item := range unit.Items {
		id, err := itemID(item)
		if err != nil {
			return nil, err
		}
		mode, err := itemMode(item)
		if err != nil {
			return nil, err
		}
		var start, end int
		if mode == "point" {
			start = offsets[id]
			end = start
		} else {
			start = offsets[id+":S"]
			end = offsets[id+":E"]
			if start > end {
				return nil, annErr("reversed_target_range")
			}
		}
		placements = append(placements, map[string]any{
			"id": id, "mode": mode,
			"target_start": start, "target_end": end,
			"status": "aligned", "method": "llm_markers",
		})
	}
	return placements, nil
}

// markerReStart 返回 marker 在 s 中的字节偏移（helper：FindAllStringSubmatchIndex 的等价用法）。
func markerReStart(s, marker string) int {
	return strings.Index(s, marker)
}

// FallbackAlignment 确定性回退（不依赖模型）。
func FallbackAlignment(unit AnnotationUnit) AnnotationAlignment {
	sourceLength := len([]rune(unit.Source))
	targetLength := len([]rune(unit.Target))
	var placements []map[string]any
	for _, item := range unit.Items {
		id, err := itemID(item)
		if err != nil {
			continue
		}
		mode, err := itemMode(item)
		if err != nil {
			continue
		}
		start, err := sourceOffset(item, "source_start", sourceLength)
		if err != nil {
			continue
		}
		var position int
		var method string
		if mode == "point" {
			if sourceLength != 0 {
				position = minInt(targetLength, (start*targetLength+sourceLength/2)/sourceLength)
			}
			method = "proportional_source_offset"
		} else {
			position = targetLength
			method = "paragraph_end"
		}
		placements = append(placements, map[string]any{
			"id": id, "mode": mode,
			"target_start": position, "target_end": position,
			"status": "fallback", "method": method,
		})
	}
	return AnnotationAlignment{
		UnitID: unit.UnitID, TargetDigest: TargetDigest(unit.Target),
		Placements: placements, UsedFallback: true,
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func emptyAlignment(unit AnnotationUnit) AnnotationAlignment {
	return AnnotationAlignment{
		UnitID: unit.UnitID, TargetDigest: TargetDigest(unit.Target,
		), Placements: []map[string]any{},
	}
}

// AnnotationAligner 注释对齐 Agent（tier=cheap）。
type AnnotationAligner struct {
	Agent
}

func NewAnnotationAligner(client llm.LLMClient, cfg *config.Config) *AnnotationAligner {
	a := &AnnotationAligner{}
	a.Agent = NewAgent(client, cfg, "AnnotationAligner")
	return a
}

// AlignUnits 多块一次模型调用；结果保持输入顺序；单块失败只回退该块；
// 传输/JSON/外层 schema 失败回退全部需要对齐的块。
func (a *AnnotationAligner) AlignUnits(units []AnnotationUnit) []AnnotationAlignment {
	if len(units) == 0 {
		return []AnnotationAlignment{}
	}
	validIDs := true
	seen := map[string]bool{}
	for _, u := range units {
		if u.UnitID == "" {
			validIDs = false
			break
		}
		if seen[u.UnitID] {
			validIDs = false
			break
		}
		seen[u.UnitID] = true
	}
	if !validIDs {
		out := make([]AnnotationAlignment, len(units))
		for i, u := range units {
			out[i] = FallbackAlignment(u)
		}
		return out
	}
	results := make([]*AnnotationAlignment, len(units))
	var requestItems []any
	var requestedIndices []int

	for i, u := range units {
		if len(u.Items) == 0 {
			results[i] = &[]AnnotationAlignment{emptyAlignment(u)}[0]
			continue
		}
		marked, err := BuildMarkedSource(u)
		if err != nil {
			fb := FallbackAlignment(u)
			results[i] = &fb
			continue
		}
		om := jsonx.NewOMap()
		om.Set("unit_id", u.UnitID)
		om.Set("source_with_markers", marked)
		om.Set("immutable_target", u.Target)
		requestItems = append(requestItems, om)
		requestedIndices = append(requestedIndices, i)
	}
	fillFallbacks := func() {
		for _, i := range requestedIndices {
			if results[i] == nil {
				fb := FallbackAlignment(units[i])
				results[i] = &fb
			}
		}
	}
	if len(requestItems) == 0 {
		fillFallbacks()
		return derefAll(results, units)
	}
	payload := marshalJSONItems(requestItems)
	aggregateTargetLength := 0
	for _, i := range requestedIndices {
		aggregateTargetLength += len([]rune(units[i].Target))
	}
	maxTokens := maxInt(1024, minInt(8192, aggregateTargetLength*2+len(requestedIndices)*64+512))
	user := "Insert the source markers at the corresponding positions in each immutable target. Return exactly one output item for every input item and no other content.\n\nINPUT JSON:\n" + payload
	data, err := a.AskJSONDefault(alignerSystemPrompt, user, "cheap", "", &maxTokens, map[string]any{})
	if err != nil {
		fillFallbacks()
		return derefAll(results, units)
	}
	om, ok := data.(*jsonx.OMap)
	if !ok {
		fillFallbacks()
		return derefAll(results, units)
	}
	itemsV, _ := om.Get("items")
	itemsArr, ok := itemsV.([]any)
	if !ok {
		fillFallbacks()
		return derefAll(results, units)
	}
	responsesByID := map[string][]*jsonx.OMap{}
	for _, i := range requestedIndices {
		responsesByID[units[i].UnitID] = nil
	}
	for _, item := range itemsArr {
		itemOm, ok := item.(*jsonx.OMap)
		if !ok {
			continue
		}
		uid, _ := itemOm.Get("unit_id")
		if s, ok := uid.(string); ok {
			if _, expected := responsesByID[s]; expected {
				responsesByID[s] = append(responsesByID[s], itemOm)
			}
		}
	}
	for _, i := range requestedIndices {
		u := units[i]
		candidates := responsesByID[u.UnitID]
		if len(candidates) != 1 {
			continue // 该块回退（缺失或重复响应）
		}
		markedTarget, _ := candidates[0].Get("marked_target")
		placements, err := ValidateMarkedTarget(u, markedTarget)
		if err != nil {
			continue // 单个畸形项不污染同批其它块
		}
		results[i] = &[]AnnotationAlignment{{
			UnitID: u.UnitID, TargetDigest: TargetDigest(u.Target), Placements: placements,
		}}[0]
	}
	fillFallbacks()
	return derefAll(results, units)
}

func derefAll(results []*AnnotationAlignment, units []AnnotationUnit) []AnnotationAlignment {
	out := make([]AnnotationAlignment, len(results))
	for i, r := range results {
		if r == nil {
			fb := FallbackAlignment(units[i])
			out[i] = fb
		} else {
			out[i] = *r
		}
	}
	return out
}

func marshalJSONItems(items any) string {
	b, err := jsonx.Marshal(map[string]any{"items": items})
	if err != nil {
		return ""
	}
	return string(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// AlignUnit 单块入口。
func (a *AnnotationAligner) AlignUnit(unit AnnotationUnit) AnnotationAlignment {
	return a.AlignUnits([]AnnotationUnit{unit})[0]
}

var _ = strconv.Itoa
