package agents

import (
	"regexp"
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// 迁移自 tests/test_annotation_aligner.py。

func alignerCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.FromDict(map[string]any{
		"language": map[string]any{"source": "en", "target": "zh"},
		"llm": map[string]any{
			"provider": "fake",
			"tiers": map[string]any{
				"strong": map[string]any{"model": "strong"},
				"cheap":  map[string]any{"model": "cheap"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func pointUnit(target string) AnnotationUnit {
	return AnnotationUnit{
		UnitID: "tn1_0",
		Source: "Hello world",
		Target: target,
		Items: []map[string]any{{
			"id": "tn1_0_annotation_0", "mode": "point",
			"source_start": 5, "source_end": 5,
			"source_text": "", "marker_text": "2",
		}},
	}
}

func rangeUnit() AnnotationUnit {
	return AnnotationUnit{
		UnitID: "tn1_1",
		Source: "a long tunnel appeared",
		Target: "一条长长的隧道出现了",
		Items: []map[string]any{{
			"id": "tn1_1_annotation_0", "mode": "range",
			"source_start": 2, "source_end": 13,
			"source_text": "long tunnel", "marker_text": "〔＊1〕",
		}},
	}
}

func TestBuildMarkedSourceForPointAndRange(t *testing.T) {
	got, err := BuildMarkedSource(pointUnit("你好世界"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello⟪tn1_0_annotation_0⟫ world" {
		t.Errorf("point = %q", got)
	}
	got, err = BuildMarkedSource(rangeUnit())
	if err != nil {
		t.Fatal(err)
	}
	if got != "a ⟪tn1_1_annotation_0:S⟫long tunnel⟪tn1_1_annotation_0:E⟫ appeared" {
		t.Errorf("range = %q", got)
	}
}

func TestExtractsExactPointAndRangeOffsets(t *testing.T) {
	point, err := ValidateMarkedTarget(pointUnit("你好世界"), "你好⟪tn1_0_annotation_0⟫世界")
	if err != nil {
		t.Fatal(err)
	}
	if point[0]["target_start"] != 2 || point[0]["target_end"] != 2 {
		t.Errorf("point = %v", point[0])
	}
	rp, err := ValidateMarkedTarget(rangeUnit(), "一条⟪tn1_1_annotation_0:S⟫长长的隧道⟪tn1_1_annotation_0:E⟫出现了")
	if err != nil {
		t.Fatal(err)
	}
	if rp[0]["target_start"] != 2 || rp[0]["target_end"] != 7 {
		t.Errorf("range = %v", rp[0])
	}
}

func TestRejectsAnyTargetEdit(t *testing.T) {
	_, err := ValidateMarkedTarget(pointUnit("你好世界"), "您好⟪tn1_0_annotation_0⟫世界")
	if err == nil || !strings.Contains(err.Error(), "target_was_modified") {
		t.Errorf("err = %v", err)
	}
}

func TestRejectsMissingUnknownAndDuplicateMarkers(t *testing.T) {
	unit := pointUnit("你好世界")
	for _, output := range []string{
		"你好世界",
		"你好⟪unknown⟫世界",
		"你⟪tn1_0_annotation_0⟫好⟪tn1_0_annotation_0⟫世界",
	} {
		if _, err := ValidateMarkedTarget(unit, output); err == nil {
			t.Errorf("output %q 应被拒绝", output)
		}
	}
}

func TestRejectsCrossingRanges(t *testing.T) {
	unit := AnnotationUnit{
		UnitID: "nested",
		Source: "abcd",
		Target: "甲乙丙丁",
		Items: []map[string]any{
			{"id": "outer", "mode": "range", "source_start": 0, "source_end": 4},
			{"id": "inner", "mode": "range", "source_start": 1, "source_end": 3},
		},
	}
	crossing := "⟪outer:S⟫甲⟪inner:S⟫乙⟪outer:E⟫丙⟪inner:E⟫丁"
	_, err := ValidateMarkedTarget(unit, crossing)
	if err == nil || !strings.Contains(err.Error(), "crossing_or_reversed_range") {
		t.Errorf("err = %v", err)
	}
}

// ---- AnnotationAligner ----

func TestAlignerCallsCheapTierAndReturnsValidatedAlignment(t *testing.T) {
	unit := pointUnit("你好世界")
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"items": [{"unit_id": "tn1_0", "marked_target": "你好⟪tn1_0_annotation_0⟫世界"}]}`, nil
	})
	result := NewAnnotationAligner(client, alignerCfg(t)).AlignUnit(unit)
	if result.UsedFallback {
		t.Errorf("不应回退")
	}
	if result.TargetDigest != TargetDigest(unit.Target) {
		t.Errorf("digest 不符")
	}
	if result.Placements[0]["target_start"] != 2 {
		t.Errorf("placement = %v", result.Placements[0])
	}
	call := client.Calls[0]
	if call["tier"] != "cheap" || call["stage"] != "AnnotationAligner" || call["json_mode"] != true {
		t.Errorf("call = %v", call)
	}
}

func TestAlignerModelOrJSONFailureReturnsDeterministicFallback(t *testing.T) {
	unit := pointUnit("你好世界")
	clients := []*llm.FakeClient{
		llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
			return "not json", nil
		}),
		llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
			return `{"items": [{"unit_id": "tn1_0", "marked_target": "译文被修改"}]}`, nil
		}),
	}
	var results []AnnotationAlignment
	for _, c := range clients {
		results = append(results, NewAnnotationAligner(c, alignerCfg(t)).AlignUnit(unit))
	}
	if results[0].TargetDigest != results[1].TargetDigest ||
		len(results[0].Placements) != len(results[1].Placements) ||
		results[0].Placements[0]["target_start"] != results[1].Placements[0]["target_start"] {
		t.Errorf("两次回退结果应一致：%v vs %v", results[0], results[1])
	}
	if !results[0].UsedFallback {
		t.Errorf("应回退")
	}
	if results[0].Placements[0]["status"] != "fallback" || results[0].Placements[0]["method"] != "proportional_source_offset" {
		t.Errorf("placement = %v", results[0].Placements[0])
	}
	if results[0].Placements[0]["target_start"] != 2 {
		t.Errorf("target_start = %v", results[0].Placements[0]["target_start"])
	}
}

func TestAlignerRangeFallbackIsZeroWidthAtParagraphEnd(t *testing.T) {
	unit := rangeUnit()
	result := NewAnnotationAligner(llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return "", nil
	}), alignerCfg(t)).AlignUnit(unit)
	placement := result.Placements[0]
	if !result.UsedFallback || placement["method"] != "paragraph_end" {
		t.Errorf("placement = %v", placement)
	}
	targetLen := len([]rune(unit.Target))
	if placement["target_start"] != targetLen || placement["target_end"] != targetLen {
		t.Errorf("placement = %v", placement)
	}
}

func TestAlignerEmptyTargetFallbackStaysAtZero(t *testing.T) {
	unit := pointUnit("")
	result := NewAnnotationAligner(llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return "", nil
	}), alignerCfg(t)).AlignUnit(unit)
	if result.TargetDigest != TargetDigest("") {
		t.Errorf("digest 不符")
	}
	if result.Placements[0]["target_start"] != 0 || result.Placements[0]["target_end"] != 0 {
		t.Errorf("placement = %v", result.Placements[0])
	}
}

var inputJSONSplit = regexp.MustCompile(`(?s)^.*INPUT JSON:\n`)

func TestAlignerAlignsMultipleUnitsInOneCallAndPreservesOrder(t *testing.T) {
	point := pointUnit("你好世界")
	ru := rangeUnit()
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		payload := inputJSONSplit.ReplaceAllString(m[len(m)-1].Content, "")
		req, err := jsonx.Parse(payload)
		if err != nil {
			t.Fatalf("payload 解析失败: %v", err)
		}
		itemsV, _ := req.(*jsonx.OMap).Get("items")
		var ids []string
		for _, item := range itemsV.([]any) {
			uid, _ := item.(*jsonx.OMap).Get("unit_id")
			ids = append(ids, uid.(string))
		}
		if len(ids) != 2 || ids[0] != point.UnitID || ids[1] != ru.UnitID {
			t.Errorf("请求顺序 = %v", ids)
		}
		return `{"items": [
			{"unit_id": "tn1_1", "marked_target": "一条⟪tn1_1_annotation_0:S⟫长长的隧道⟪tn1_1_annotation_0:E⟫出现了"},
			{"unit_id": "tn1_0", "marked_target": "你好⟪tn1_0_annotation_0⟫世界"}
		]}`, nil
	})
	results := NewAnnotationAligner(client, alignerCfg(t)).AlignUnits([]AnnotationUnit{point, ru})
	if len(client.Calls) != 1 {
		t.Errorf("calls = %d", len(client.Calls))
	}
	if results[0].UnitID != point.UnitID || results[1].UnitID != ru.UnitID {
		t.Errorf("顺序被打乱")
	}
	for _, r := range results {
		if r.UsedFallback {
			t.Errorf("不应回退：%v", r)
		}
	}
	if results[0].Placements[0]["target_start"] != 2 || results[1].Placements[0]["target_start"] != 2 {
		t.Errorf("placements 不符")
	}
	// max_tokens 启发式
	expectedMax := maxInt(1024, minInt(8192, (len([]rune(point.Target))+len([]rune(ru.Target)))*2+2*64+512))
	if client.Calls[0]["max_tokens"] != expectedMax {
		t.Errorf("max_tokens = %v, want %d", client.Calls[0]["max_tokens"], expectedMax)
	}
}

func TestAlignerBadBatchItemsFallBackIndividually(t *testing.T) {
	valid := pointUnit("你好世界")
	missing := AnnotationUnit{
		UnitID: "missing", Source: "A note", Target: "缺失",
		Items: []map[string]any{{"id": "missing_note", "mode": "point", "source_start": 1, "source_end": 1}},
	}
	duplicate := AnnotationUnit{
		UnitID: "duplicate", Source: "B note", Target: "重复",
		Items: []map[string]any{{"id": "duplicate_note", "mode": "point", "source_start": 1, "source_end": 1}},
	}
	invalid := AnnotationUnit{
		UnitID: "invalid", Source: "C note", Target: "不可修改",
		Items: []map[string]any{{"id": "invalid_note", "mode": "point", "source_start": 1, "source_end": 1}},
	}
	response := `{"items": [
		{"unit_id": "tn1_0", "marked_target": "你好⟪tn1_0_annotation_0⟫世界"},
		{"unit_id": "duplicate", "marked_target": "重⟪duplicate_note⟫复"},
		{"unit_id": "duplicate", "marked_target": "重复⟪duplicate_note⟫"},
		{"unit_id": "invalid", "marked_target": "已被模型修改"},
		{"unit_id": "unknown", "marked_target": "ignored"}
	]}`
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return response, nil
	})
	results := NewAnnotationAligner(client, alignerCfg(t)).AlignUnits([]AnnotationUnit{valid, missing, duplicate, invalid})
	if len(client.Calls) != 1 {
		t.Errorf("calls = %d", len(client.Calls))
	}
	if results[0].UsedFallback {
		t.Errorf("valid 块不应回退")
	}
	for _, r := range results[1:] {
		if !r.UsedFallback {
			t.Errorf("%s 应回退", r.UnitID)
		}
	}
}

func TestAlignerOuterJSONFailureFallsBackWholeBatch(t *testing.T) {
	units := []AnnotationUnit{pointUnit("你好世界"), rangeUnit()}
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return "not json", nil
	})
	results := NewAnnotationAligner(client, alignerCfg(t)).AlignUnits(units)
	if len(client.Calls) != 1 {
		t.Errorf("calls = %d", len(client.Calls))
	}
	for _, r := range results {
		if !r.UsedFallback {
			t.Errorf("%s 应回退", r.UnitID)
		}
	}
}

func TestAlignerDuplicateInputUnitIDsDoNotCallModel(t *testing.T) {
	unit := pointUnit("你好世界")
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return "should not be called", nil
	})
	results := NewAnnotationAligner(client, alignerCfg(t)).AlignUnits([]AnnotationUnit{unit, unit})
	if len(client.Calls) != 0 {
		t.Errorf("calls = %d", len(client.Calls))
	}
	if len(results) != 2 {
		t.Fatalf("results = %d", len(results))
	}
	for _, r := range results {
		if !r.UsedFallback {
			t.Errorf("重复 id 应回退")
		}
	}
}
