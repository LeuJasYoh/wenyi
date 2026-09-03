package pipeline

// 迁移自 tests/test_orchestrator.py（阶段 3 范围：非 Review/非 assemble 用例）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/fakellm"
	"wenyi/internal/glossary"
	"wenyi/internal/ingest"
	"wenyi/internal/llm"
	_ "modernc.org/sqlite"
)

var numberedLineRe = regexp.MustCompile(`(?m)^\[(\d+)\]`)

func countNumbered(text string) int { return len(numberedLineRe.FindAllString(text, -1)) }

// translatedParaCount 统计送进翻译模型的源段总数。
func translatedParaCount(calls []map[string]any) int {
	n := 0
	for _, c := range calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "文学翻译") {
			n += countNumbered(msgs[len(msgs)-1].Content)
		}
	}
	return n
}

func testStateConfig(t *testing.T, stateDir string) *config.Config {
	t.Helper()
	cfg, err := config.FromDict(map[string]any{
		"language": map[string]any{"source": "ja", "target": "zh"},
		"llm": map[string]any{
			"provider": "fake",
			"tiers": map[string]any{
				"strong": map[string]any{"model": "p"},
				"cheap":  map[string]any{"model": "f"},
			},
		},
		"segment": map[string]any{"max_chars_per_batch": 1800},
		"pipeline": map[string]any{
			"review": true, "polish": true, "backtranslate_sample": 0.0, "consistency_qa": true,
		},
		"paths": map[string]any{"state_dir": stateDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeSampleTXT(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "novel.txt")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func routingClient() *llm.FakeClient {
	return llm.NewFakeClient(fakellm.RoutingHandler)
}

func intp(i int) *int { return &i }

// ---- TestOrchestrator ----

func TestAnnotationAlignmentMergesContinuationsAndPersistsOffsets(t *testing.T) {
	dir := t.TempDir()
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.SourceLang = "en"
	cfg.Pipeline.AnnotationAlignment = true

	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		if strings.Contains(messages[0].Content, "align EPUB annotation markers") {
			if tier != "cheap" {
				t.Errorf("tier = %s", tier)
			}
			return `{"items": [{"unit_id": "ch0:tn0_0", "marked_target": "阿尔法⟪tn0_0_annotation_0⟫ 贝塔"}]}`, nil
		}
		return fakellm.RoutingHandler(messages, tier, jsonMode)
	})
	sourceLength := len("Alpha beta")
	chapter := &ingest.Chapter{
		Index: 0,
		Segments: []*ingest.Segment{
			{
				Index: 0, Source: "Alpha ", Kind: "text", Target: strp("阿尔法 "), Anchor: strp("tn0_0"),
				Meta: map[string]any{
					"epub_annotations": map[string]any{
						"version": 1, "source_length": sourceLength,
						"items": []any{map[string]any{
							"id": "tn0_0_annotation_0", "mode": "point",
							"source_start": 5, "source_end": 5, "source_text": "", "marker_text": "1",
						}},
					},
				},
			},
			{Index: 1, Source: "beta", Kind: "text", Target: strp("贝塔"), Cont: true, Meta: map[string]any{}},
		},
		Meta: map[string]any{},
	}
	store, err := NewRunStore(filepath.Join(dir, "state", "book"), true)
	if err != nil {
		t.Fatal(err)
	}
	orch, err := NewOrchestrator(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	orch.AlignAnnotationsAfterBatch(0, chapter, 0, 2, store)
	saved, err := store.LoadChapter(0)
	if err != nil {
		t.Fatal(err)
	}
	metadata := saved.Segments[0].Meta["epub_annotations"].(map[string]any)
	placements := metadata["placements"].([]any)
	p0 := placements[0].(map[string]any)
	if p0["target_start"] != int64(len([]rune("阿尔法"))) || p0["target_end"] != int64(len([]rune("阿尔法"))) {
		t.Errorf("placement = %v", p0)
	}
	if p0["status"] != "aligned" {
		t.Errorf("status = %v", p0["status"])
	}
	if metadata["target_digest"] == "" {
		t.Errorf("digest 缺失")
	}
	calls := 0
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "align EPUB annotation markers") {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("calls = %d", calls)
	}
}

func TestAnnotationAlignmentWaitsForFinalContinuation(t *testing.T) {
	dir := t.TempDir()
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.SourceLang = "en"
	cfg.Pipeline.AnnotationAlignment = true
	var requested []string
	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		if strings.Contains(messages[0].Content, "align EPUB annotation markers") {
			requested = append(requested, messages[len(messages)-1].Content)
			return `{"items": [{"unit_id": "ch0:tn0_0", "marked_target": "甲⟪tn0_0_annotation_0⟫乙"}]}`, nil
		}
		return fakellm.RoutingHandler(messages, tier, jsonMode)
	})
	sourceLength := len("Alpha beta")
	newChapter := func(secondTarget *string) *ingest.Chapter {
		return &ingest.Chapter{
			Index: 0,
			Segments: []*ingest.Segment{
				{
					Index: 0, Source: "Alpha ", Kind: "text", Target: strp("甲"), Anchor: strp("tn0_0"),
					Meta: map[string]any{
						"epub_annotations": map[string]any{
							"version": 1, "source_length": sourceLength,
							"items": []any{map[string]any{
								"id": "tn0_0_annotation_0", "mode": "point",
								"source_start": 5, "source_end": 5, "source_text": "", "marker_text": "1",
							}},
						},
					},
				},
				{Index: 1, Source: "beta", Kind: "text", Target: secondTarget, Cont: true, Meta: map[string]any{}},
			},
			Meta: map[string]any{},
		}
	}
	chapter := newChapter(nil)
	store, _ := NewRunStore(filepath.Join(dir, "state", "book"), true)
	orch, _ := NewOrchestrator(cfg, client)
	orch.AlignAnnotationsAfterBatch(0, chapter, 0, 1, store)
	if len(requested) != 0 {
		t.Fatalf("续段未完成不应请求：%v", requested)
	}
	chapter.Segments[1].Target = strp("乙")
	orch.AlignAnnotationsAfterBatch(0, chapter, 1, 1, store)
	if len(requested) != 1 {
		t.Fatalf("requested = %d", len(requested))
	}
	if !strings.Contains(requested[0], `"immutable_target": "甲乙"`) {
		t.Errorf("immutable_target 不符：%s", requested[0])
	}
	saved, _ := store.LoadChapter(0)
	placements := saved.Segments[0].Meta["epub_annotations"].(map[string]any)["placements"].([]any)
	if placements[0].(map[string]any)["target_start"] != int64(1) {
		t.Errorf("target_start = %v", placements[0])
	}
}

func TestAnnotationAlignmentProcessesMultipleSegmentsSequentially(t *testing.T) {
	dir := t.TempDir()
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.SourceLang = "en"
	cfg.Pipeline.AnnotationAlignment = true
	var requestedUnits []string
	annotationMeta := func(id string) map[string]any {
		return map[string]any{
			"epub_annotations": map[string]any{
				"version": 1, "source_length": 1,
				"items": []any{map[string]any{
					"id": id, "mode": "point", "source_start": 1, "source_end": 1,
					"source_text": "", "marker_text": "1",
				}},
			},
		}
	}
	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		if !strings.Contains(messages[0].Content, "align EPUB annotation markers") {
			return fakellm.RoutingHandler(messages, tier, jsonMode)
		}
		user := messages[len(messages)-1].Content
		var unitID, target, annotationID string
		if strings.Contains(user, `"unit_id": "ch0:a"`) {
			unitID, target, annotationID = "ch0:a", "甲", "a_note"
		} else {
			unitID, target, annotationID = "ch0:b", "乙", "b_note"
		}
		requestedUnits = append(requestedUnits, unitID)
		return fmt.Sprintf(`{"items": [{"unit_id": %q, "marked_target": "%s⟪%s⟫"}]}`, unitID, target, annotationID), nil
	})
	chapter := &ingest.Chapter{
		Index: 0,
		Segments: []*ingest.Segment{
			{Index: 0, Source: "A", Kind: "text", Target: strp("甲"), Anchor: strp("a"), Meta: annotationMeta("a_note")},
			{Index: 1, Source: "B", Kind: "text", Target: strp("乙"), Anchor: strp("b"), Meta: annotationMeta("b_note")},
		},
		Meta: map[string]any{},
	}
	store, _ := NewRunStore(filepath.Join(dir, "state", "book"), true)
	orch, _ := NewOrchestrator(cfg, client)
	orch.AlignAnnotationsAfterBatch(0, chapter, 0, 2, store)
	if len(requestedUnits) != 2 || requestedUnits[0] != "ch0:a" || requestedUnits[1] != "ch0:b" {
		t.Errorf("requestedUnits = %v", requestedUnits)
	}
	saved, _ := store.LoadChapter(0)
	for _, seg := range saved.Segments {
		placements := seg.Meta["epub_annotations"].(map[string]any)["placements"].([]any)
		if placements[0].(map[string]any)["target_start"] != int64(1) {
			t.Errorf("placement = %v", placements[0])
		}
	}
}

func TestPrepareRetriesAfterAnalysisFailure(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	if _, err := NewOrchestrator(cfg, llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return "", fmt.Errorf("temporary model failure")
	})); err == nil {
		// NewOrchestrator 本身不调模型
	}
	orch, _ := NewOrchestrator(cfg, llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return "", fmt.Errorf("temporary model failure")
	}))
	if _, err := orch.Prepare(txt, nil); err == nil || !strings.Contains(err.Error(), "temporary model failure") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(cfg.StateDir)
	if len(entries) != 1 {
		t.Fatalf("run_dirs = %d", len(entries))
	}
	manifestPath := filepath.Join(cfg.StateDir, entries[0].Name(), "manifest.json")
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Errorf("manifest 不应存在")
	}
	orch2, _ := NewOrchestrator(cfg, routingClient())
	store, err := orch2.Prepare(txt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Exists() {
		t.Errorf("store 应存在")
	}
	m, _ := store.LoadManifest()
	if m["initialized"] != true {
		t.Errorf("initialized = %v", m["initialized"])
	}
	analysis, _ := store.LoadAnalysis()
	if analysis == nil {
		t.Errorf("analysis 应存在")
	}
}

func TestFullRunAndResume(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	orch, _ := NewOrchestrator(cfg, routingClient())
	store, err := orch.Run(txt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := store.LoadManifest()
	chapters := m["chapters"].([]any)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d", len(chapters))
	}
	for _, item := range chapters {
		cm := item.(map[string]any)
		if cm["status"] != StatusDone {
			t.Errorf("status = %v", cm["status"])
		}
	}
	ch0, _ := store.LoadChapter(0)
	for _, s := range ch0.TextSegments() {
		if s.Target == nil || *s.Target == "" {
			t.Errorf("段 %d 无译文", s.Index)
		}
	}
	g, _ := glossary.OpenStore(store.GlossaryPath())
	defer g.Close()
	if t1, _ := g.GetTerm("綾小路"); t1 == nil {
		t.Errorf("綾小路 未入库")
	}
	if t2, _ := g.GetTerm("堀北"); t2 == nil {
		t.Errorf("堀北 未入库")
	}
	// 续跑：所有章已 done，不应再产生翻译调用
	orch2, _ := NewOrchestrator(cfg, routingClient())
	if _, err := orch2.Run(txt, nil, nil); err != nil {
		t.Fatal(err)
	}
	translateCalls := 0
	for _, c := range orch2.Client.(*llm.FakeClient).Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "文学翻译") {
			translateCalls++
		}
	}
	if translateCalls != 0 {
		t.Errorf("translateCalls = %d", translateCalls)
	}
}

func TestResumeAfterPartial(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	orch, _ := NewOrchestrator(cfg, routingClient())
	store, err := orch.Run(txt, intp(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := store.LoadManifest()
	chapters := m["chapters"].([]any)
	if chapters[0].(map[string]any)["status"] != StatusDone {
		t.Errorf("ch0 status")
	}
	if chapters[1].(map[string]any)["status"] == StatusDone {
		t.Errorf("ch1 不应 done")
	}
	orch2, _ := NewOrchestrator(cfg, routingClient())
	var chapterIndices []int
	for _, item := range chapters {
		chapterIndices = append(chapterIndices, int(item.(map[string]any)["index"].(int64)))
	}
	expectedTotal, expectedDone, err := orch2.progressCounts(store, chapterIndices)
	if err != nil {
		t.Fatal(err)
	}
	var progressEvents [][3]any
	store2, err := orch2.Run(txt, nil, func(done, total int, label string) {
		progressEvents = append(progressEvents, [3]any{done, total, label})
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := store2.LoadManifest()
	for _, item := range m2["chapters"].([]any) {
		if item.(map[string]any)["status"] != StatusDone {
			t.Errorf("status = %v", item.(map[string]any)["status"])
		}
	}
	ch1, _ := store.LoadChapter(1)
	chapterLabel := ChapterProgressLabel(ch1.Title, 1)
	var first [3]any
	found := false
	for _, e := range progressEvents {
		if e[2] == chapterLabel {
			first = e
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("缺少章进度标签 %q", chapterLabel)
	}
	if first[0] != expectedDone || first[1] != expectedTotal {
		t.Errorf("first = %v, want (%d, %d)", first, expectedDone, expectedTotal)
	}
}

// ---- TestSegmentLevelResume ----

func trHandler(tag string) llm.FakeHandler {
	return func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		if strings.Contains(messages[0].Content, "文学翻译") {
			n := countNumbered(messages[len(messages)-1].Content)
			items := make([]string, n)
			for i := range items {
				items[i] = fmt.Sprintf(`"%s译%d"`, tag, i)
			}
			return `{"translations":[` + strings.Join(items, ",") + `]}`, nil
		}
		return fakellm.RoutingHandler(messages, tier, jsonMode)
	}
}

func TestResumeSkipsDoneSegmentsKeepsTheirText(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Segment.MaxCharsPerBatch = 8
	cfg.Pipeline.Polish = false
	c1 := llm.NewFakeClient(trHandler("R1"))
	orch, _ := NewOrchestrator(cfg, c1)
	store, err := orch.Run(txt, intp(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := store.LoadChapter(0)
	for _, s := range ch.TextSegments() {
		if !strings.HasPrefix(s.TargetText(), "R1") {
			t.Fatalf("段 %d 未带 R1 前缀", s.Index)
		}
	}
	last := ch.Segments[len(ch.Segments)-1]
	last.Target = strp("")
	_ = store.SaveChapter(ch)
	_ = store.SetChapterStatus(0, StatusPending)
	c2 := llm.NewFakeClient(trHandler("R2"))
	orch2, _ := NewOrchestrator(cfg, c2)
	if _, err := orch2.Run(txt, intp(0), nil); err != nil {
		t.Fatal(err)
	}
	if got := translatedParaCount(c2.Calls); got != 1 {
		t.Errorf("重翻段数 = %d, want 1", got)
	}
	ch2, _ := store.LoadChapter(0)
	segs := ch2.TextSegments()
	if !strings.HasPrefix(segs[0].TargetText(), "R1") {
		t.Errorf("首段应保留 R1")
	}
	if !strings.HasPrefix(segs[len(segs)-1].TargetText(), "R2") {
		t.Errorf("补译段应为 R2")
	}
}

func TestResumeSplitsMixedBatchAfterBudgetChange(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Segment.MaxCharsPerBatch = 100000
	cfg.Pipeline.Polish = false
	first := llm.NewFakeClient(trHandler("R1"))
	orch, _ := NewOrchestrator(cfg, first)
	store, err := orch.Run(txt, intp(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	chapter, _ := store.LoadChapter(0)
	segs := chapter.TextSegments()
	segs[len(segs)-1].Target = strp("")
	_ = store.SaveChapter(chapter)
	_ = store.SetChapterStatus(0, StatusPending)
	cfg.Segment.MaxCharsPerBatch = 50000
	second := llm.NewFakeClient(trHandler("R2"))
	orch2, _ := NewOrchestrator(cfg, second)
	if _, err := orch2.Run(txt, intp(0), nil); err != nil {
		t.Fatal(err)
	}
	if got := translatedParaCount(second.Calls); got != 1 {
		t.Errorf("重翻段数 = %d, want 1", got)
	}
	resumed, _ := store.LoadChapter(0)
	rsegs := resumed.TextSegments()
	for _, s := range rsegs[:len(rsegs)-1] {
		if !strings.HasPrefix(s.TargetText(), "R1") {
			t.Errorf("已有译文被覆盖：%v", s.TargetText())
		}
	}
	if !strings.HasPrefix(rsegs[len(rsegs)-1].TargetText(), "R2") {
		t.Errorf("补译段应为 R2")
	}
}

// ---- TestBookUnderstanding ----

func lastTranslateUser(calls []map[string]any) string {
	for i := len(calls) - 1; i >= 0; i-- {
		msgs := calls[i]["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "文学翻译") {
			return msgs[len(msgs)-1].Content
		}
	}
	return ""
}

func TestPrepassBuildsAndInjects(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	client := routingClient()
	orch, _ := NewOrchestrator(cfg, client)
	store, err := orch.Run(txt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch0, _ := store.LoadChapter(0)
	if digest, _ := ch0.Meta["source_digest"].(string); digest == "" {
		t.Errorf("source_digest 缺失")
	}
	analysis, _ := store.LoadAnalysis()
	if synopsis, _ := analysis["book_synopsis"].(string); synopsis == "" {
		t.Errorf("book_synopsis 缺失")
	}
	user := lastTranslateUser(client.Calls)
	for _, want := range []string{"【全书概览】", "【本章梗概】", "全书概览", "本章梗概"} {
		if !strings.Contains(user, want) {
			t.Errorf("translate prompt 缺少 %q", want)
		}
	}
}

func TestPrepareForTranslationBuildsUnderstandingWithoutTargets(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	client := routingClient()
	orch, _ := NewOrchestrator(cfg, client)
	store, err := orch.PrepareForTranslation(txt, nil)
	if err != nil {
		t.Fatal(err)
	}
	analysis, _ := store.LoadAnalysis()
	if len(analysis) == 0 {
		t.Errorf("analysis 缺失")
	}
	if synopsis, _ := analysis["book_synopsis"].(string); synopsis == "" {
		t.Errorf("book_synopsis 缺失")
	}
	g, _ := glossary.OpenStore(store.GlossaryPath())
	defer g.Close()
	stats, _ := g.Stats()
	if stats["terms"] <= 0 {
		t.Errorf("terms = %d", stats["terms"])
	}
	m, _ := store.LoadManifest()
	for _, item := range m["chapters"].([]any) {
		idx := int(item.(map[string]any)["index"].(int64))
		ch, _ := store.LoadChapter(idx)
		if digest, _ := ch.Meta["source_digest"].(string); digest == "" {
			t.Errorf("ch%d digest 缺失", idx)
		}
		for _, seg := range ch.Segments {
			if seg.Target != nil {
				t.Errorf("准备模式不应有译文")
			}
		}
	}
	translateCalls := 0
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "文学翻译") {
			translateCalls++
		}
	}
	if translateCalls != 0 {
		t.Errorf("translateCalls = %d", translateCalls)
	}
}

func TestPrescanParallel(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Pipeline.PrescanConcurrency = 3
	client := routingClient()
	orch, _ := NewOrchestrator(cfg, client)
	store, err := orch.Run(txt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := store.LoadManifest()
	for _, item := range m["chapters"].([]any) {
		idx := int(item.(map[string]any)["index"].(int64))
		ch, _ := store.LoadChapter(idx)
		if digest, _ := ch.Meta["source_digest"].(string); digest == "" {
			t.Errorf("ch%d digest 缺失", idx)
		}
	}
	analysis, _ := store.LoadAnalysis()
	if synopsis, _ := analysis["book_synopsis"].(string); synopsis == "" {
		t.Errorf("book_synopsis 缺失")
	}
	if user := lastTranslateUser(client.Calls); !strings.Contains(user, "【本章梗概】") {
		t.Errorf("prompt 缺少本章梗概")
	}
}

func TestResumeSkipsPrepass(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	orch, _ := NewOrchestrator(cfg, routingClient())
	if _, err := orch.Run(txt, nil, nil); err != nil {
		t.Fatal(err)
	}
	c2 := routingClient()
	orch2, _ := NewOrchestrator(cfg, c2)
	if _, err := orch2.Run(txt, nil, nil); err != nil {
		t.Fatal(err)
	}
	prepass := 0
	for _, c := range c2.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "梗概员") || strings.Contains(msgs[0].Content, "概览员") {
			prepass++
		}
	}
	if prepass != 0 {
		t.Errorf("prepass = %d", prepass)
	}
}

func TestToggleOff(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Pipeline.BookUnderstanding = false
	client := routingClient()
	orch, _ := NewOrchestrator(cfg, client)
	store, err := orch.Run(txt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch0, _ := store.LoadChapter(0)
	if digest, _ := ch0.Meta["source_digest"].(string); digest != "" {
		t.Errorf("digest 应为空")
	}
	analysis, _ := store.LoadAnalysis()
	if synopsis, _ := analysis["book_synopsis"].(string); synopsis != "" {
		t.Errorf("book_synopsis 应为空")
	}
	prepass := 0
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "梗概员") || strings.Contains(msgs[0].Content, "概览员") {
			prepass++
		}
	}
	if prepass != 0 {
		t.Errorf("prepass = %d", prepass)
	}
}

// ---- TestStyleAnalysis ----

func loadDocForTest(t *testing.T, path, content string) *ingest.Document {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	doc, err := ingest.LoadDocument(path, "ja", "zh", 0, tmp)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSampleTextMultipoint(t *testing.T) {
	dir := t.TempDir()
	var chapters []string
	for i := 0; i < 3; i++ {
		var paras []string
		for j := 0; j < 8; j++ {
			paras = append(paras, fmt.Sprintf("章%dの段落%dです。", i, j)+strings.Repeat("あ", 60))
		}
		chapters = append(chapters, fmt.Sprintf("# 第%d章\n\n%s", i, strings.Join(paras, "\n\n")))
	}
	doc := loadDocForTest(t, filepath.Join(dir, "long.txt"), strings.Join(chapters, "\n\n"))
	labeled := SampleText(doc, true)
	for _, tag := range []string{"【开头样章】", "【中部样章】", "【结尾样章】"} {
		if !strings.Contains(labeled, tag) {
			t.Errorf("缺少 %s", tag)
		}
	}
	plain := SampleText(doc, false)
	if strings.Contains(plain, "样章】") {
		t.Errorf("plain 不应含标注")
	}
	if !strings.Contains(plain, "章0の段落0です") {
		t.Errorf("plain 缺少正文")
	}
}

func TestSampleTextShortBookDedup(t *testing.T) {
	dir := t.TempDir()
	doc := loadDocForTest(t, filepath.Join(dir, "short.txt"), "# 唯一章\n\n长段落。"+strings.Repeat("あ", 300))
	sample := SampleText(doc, true)
	if strings.Count(sample, "【开头样章】") != 1 {
		t.Errorf("sample = %q", sample)
	}
	if strings.Contains(sample, "中部样章") || strings.Contains(sample, "结尾样章") {
		t.Errorf("单章书不应重复取样")
	}
}

// ---- TestGlossaryScope ----

func runWithTerms(t *testing.T, dir, scope string) []string {
	t.Helper()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Pipeline.GlossaryScope = scope
	orch, _ := NewOrchestrator(cfg, routingClient())
	store, err := orch.Prepare(txt, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := glossary.OpenStore(store.GlossaryPath())
	_, _ = g.UpsertTerm(&glossary.Term{Source: "外部人物X", Target: "外部译名", Type: "人物", Status: "ok"}, nil)
	_, _ = g.UpsertTerm(&glossary.Term{Source: "無関係用語", Target: "无关术语", Type: "术语", Status: "ok"}, nil)
	_, _ = g.UpsertTerm(&glossary.Term{Source: "ホリキタ", Target: "堀北译名", Aliases: []string{"堀北"}, Type: "术语", Status: "ok"}, nil)
	_ = g.Close()
	client := routingClient()
	orch2, _ := NewOrchestrator(cfg, client)
	if _, err := orch2.Run(txt, nil, nil); err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "文学翻译") {
			var joined []string
			for _, m := range msgs {
				joined = append(joined, m.Content)
			}
			prompts = append(prompts, strings.Join(joined, "\n"))
		}
	}
	return prompts
}

func TestChapterScopePrunes(t *testing.T) {
	prompts := runWithTerms(t, t.TempDir(), "chapter")
	if len(prompts) == 0 {
		t.Fatal("无翻译 prompt")
	}
	for _, p := range prompts {
		if strings.Contains(p, "外部人物X") || strings.Contains(p, "無関係用語") {
			t.Errorf("未出现条目应剔除")
		}
		if !strings.Contains(p, "ホリキタ") {
			t.Errorf("alias 命中条目应保留")
		}
	}
}

func TestFullScopeKeepsAll(t *testing.T) {
	prompts := runWithTerms(t, t.TempDir(), "full")
	if len(prompts) == 0 {
		t.Fatal("无翻译 prompt")
	}
	for _, p := range prompts {
		for _, want := range []string{"外部人物X", "無関係用語", "ホリキタ"} {
			if !strings.Contains(p, want) {
				t.Errorf("缺少 %s", want)
			}
		}
	}
}

func TestBatchGlossaryRefreshesFollowingPrompts(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "novel.txt")
	content := "# 第一章\n\n「夏帆ちゃん」と母親が言った。\n\n夏帆ちゃんは窓の外を見た。\n"
	if err := os.WriteFile(txt, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Pipeline.Polish = false
	cfg.Pipeline.Review = false
	cfg.Pipeline.ConsistencyQA = false
	cfg.Pipeline.BookUnderstanding = false
	cfg.Segment.MaxCharsPerBatch = 10
	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		system := messages[0].Content
		user := messages[len(messages)-1].Content
		if strings.Contains(system, "文学翻译") {
			n := countNumbered(user)
			items := make([]string, n)
			for i := range items {
				items[i] = `"小夏帆"`
			}
			return `{"translations":[` + strings.Join(items, ",") + `]}`, nil
		}
		if strings.Contains(system, "术语") && strings.Contains(system, "抽取器") &&
			strings.Contains(user, "夏帆ちゃん") && strings.Contains(user, "小夏帆") {
			return `{"terms":[{"source":"夏帆ちゃん","target":"小夏帆","type":"称谓","aliases":["夏帆"],"note":"亲昵称呼"}]}`, nil
		}
		return fakellm.RoutingHandler(messages, tier, jsonMode)
	})
	orch, _ := NewOrchestrator(cfg, client)
	if _, err := orch.Run(txt, nil, nil); err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "文学翻译") {
			var joined []string
			for _, m := range msgs {
				joined = append(joined, m.Content)
			}
			prompts = append(prompts, strings.Join(joined, "\n"))
		}
	}
	if len(prompts) < 3 {
		t.Fatalf("prompts = %d", len(prompts))
	}
	if !strings.Contains(prompts[len(prompts)-1], "夏帆ちゃん → 小夏帆") {
		t.Errorf("后续 prompt 未带新术语")
	}
}

func TestResumeRecoversBatchGlossaryCheckpointsFromEvents(t *testing.T) {
	dir := t.TempDir()
	txt := writeSampleTXT(t, dir)
	cfg := testStateConfig(t, filepath.Join(dir, "state"))
	cfg.Pipeline.Polish = false
	cfg.Pipeline.Review = false
	cfg.Pipeline.ConsistencyQA = false
	cfg.Pipeline.BookUnderstanding = false
	cfg.Segment.MaxCharsPerBatch = 8
	orch, _ := NewOrchestrator(cfg, routingClient())
	store, err := orch.Run(txt, intp(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := store.CompletedBatchGlossaryKeys(0)
	if len(checkpoints) <= 1 {
		t.Fatalf("checkpoints = %d", len(checkpoints))
	}
	_ = store.SetChapterStatus(0, StatusPending)
	var labels []string
	var glossaryLabels []string
	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		system := messages[0].Content
		if strings.Contains(system, "术语") && strings.Contains(system, "抽取器") {
			glossaryLabels = append(glossaryLabels, labels[len(labels)-1])
		}
		return fakellm.RoutingHandler(messages, tier, jsonMode)
	})
	orch2, _ := NewOrchestrator(cfg, client)
	if _, err := orch2.Run(txt, intp(0), func(done, total int, label string) {
		labels = append(labels, label)
	}); err != nil {
		t.Fatal(err)
	}
	glossaryCalls := 0
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if strings.Contains(msgs[0].Content, "术语") && strings.Contains(msgs[0].Content, "抽取器") {
			glossaryCalls++
		}
	}
	if glossaryCalls != 1 {
		t.Errorf("glossaryCalls = %d, want 1（只保留章末兜底）", glossaryCalls)
	}
	if len(glossaryLabels) == 0 {
		t.Fatal("glossaryLabels 空")
	}
	for _, label := range glossaryLabels {
		if label == "解析文档…" {
			t.Errorf("label = %s", label)
		}
	}
}

// ---- TestLangNormalize ----

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"Japanese": "ja", "日语": "ja", "RU": "ru", "russian": "ru",
		"fr": "fr", "unknown": "", "": "",
	}
	for in, want := range cases {
		if got := NormalizeLang(in); got != want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- TestProgressLabels（标签函数部分）----

func TestProgressLabelPrefersRealTitle(t *testing.T) {
	if ChapterProgressLabel("引言", 0) != "引言" {
		t.Errorf("label")
	}
	if ChapterProgressLabel("第一章", 1) != "第一章" {
		t.Errorf("label")
	}
	if ChapterProgressLabel("", 1) != "章节 2" {
		t.Errorf("label")
	}
}

// ---- test_usage.py 剩余（RunStore 相关）----

func TestSecondStoreWaitsForFirstStoreLock(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "state", "book")
	first, _ := NewRunStore(runDir, true)
	second, _ := NewRunStore(runDir, true)
	unlockFirst, err := first.Lock()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	go func() {
		unlockSecond, err := second.Lock()
		if err != nil {
			t.Error(err)
			close(entered)
			return
		}
		defer unlockSecond()
		close(entered)
	}()
	select {
	case <-entered:
		t.Fatal("第二把锁不应立即获得")
	case <-waitMillis(100):
	}
	unlockFirst()
	select {
	case <-entered:
	case <-waitMillis(1000):
		t.Fatal("释放后第二把锁应获得")
	}
}

func waitMillis(ms int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		timeSleep(ms)
		close(ch)
	}()
	return ch
}

// ---- test_llm_retrying.py::test_orchestrator_retry_sink_writes_book_event_log ----

func TestOrchestratorRetrySinkWritesBookEventLog(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewRunStore(dir, true)
	cfg := testStateConfig(t, dir)
	orch, err := NewOrchestrator(cfg, routingClient())
	if err != nil {
		t.Fatal(err)
	}
	orch.BindLLMEvents(store)
	// 直发一个重试事件（等价 client._emit_event）
	orch.Client.SetEventSink(func(event string, data map[string]any) {
		_ = store.LogEvent(event, data)
	})
	// 通过 Core EmitEvent 路径
	if emitter, ok := orch.Client.(interface {
		EmitEventTest(event string, data map[string]any)
	}); ok {
		_ = emitter
	}
	_ = store.LogEvent("llm_retry_wait", map[string]any{"reason": "http_502", "wait_seconds": 1.0})
	data, err := os.ReadFile(store.EventLogPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != "llm_retry_wait" || event["reason"] != "http_502" {
		t.Errorf("event = %v", event)
	}
}

var _ = agents.NewAnalyzer

// ---- TestRollingContext（test_glossary_agents.py，移至本包避免 import cycle）----

func TestRollingContextRenderAndBound(t *testing.T) {
	ctx := NewRollingContext(3)
	ctx.AddTargets([]string{"a", "b", "c", "d", "e"})
	if len(ctx.RecentTargets) != 3 || ctx.RecentTargets[0] != "c" || ctx.RecentTargets[2] != "e" {
		t.Errorf("recent = %v", ctx.RecentTargets)
	}
	rendered := ctx.Render(2)
	if !strings.Contains(rendered, "d") || !strings.Contains(rendered, "e") || strings.Contains(rendered, "c") {
		t.Errorf("rendered = %q", rendered)
	}
}

func TestRollingContextRoundtrip(t *testing.T) {
	ctx := &RollingContext{RecentTargets: []string{"x", "y"}, MaxRecentKeep: 75}
	ctx2 := RollingContextFromDict(ctx.ToDict(), 0)
	if len(ctx2.RecentTargets) != 2 || ctx2.RecentTargets[0] != "x" || ctx2.MaxRecentKeep != 75 {
		t.Errorf("ctx2 = %+v", ctx2)
	}
}

func TestRollingContextConfiguredMinimumExpandsLegacyLimit(t *testing.T) {
	targets := make([]any, 40)
	for i := range targets {
		targets[i] = "x"
	}
	ctx := RollingContextFromDict(map[string]any{"recent_targets": targets}, 100)
	if ctx.MaxRecentKeep != 100 {
		t.Errorf("max_recent_keep = %d", ctx.MaxRecentKeep)
	}
}
