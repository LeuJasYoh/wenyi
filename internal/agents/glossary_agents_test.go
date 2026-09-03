package agents

import (
	"path/filepath"
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
	_ "modernc.org/sqlite"
)

// 迁移自 tests/test_glossary_agents.py。

func testCfg(t *testing.T) *config.Config {
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
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func omap(pairs ...any) *jsonx.OMap {
	om := jsonx.NewOMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		om.Set(pairs[i].(string), pairs[i+1])
	}
	return om
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTestGlossary(t *testing.T) *glossary.Store {
	t.Helper()
	store, err := glossary.OpenStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ---- TestAnalyzer ----

func TestAnalyzerAnalyzeAndSeed(t *testing.T) {
	analysis := omap(
		"genre", "校园",
		"tone", "冷峻第三人称",
		"style_guide", "保持克制",
		"characters", []any{omap(
			"source", "綾小路", "target", "绫小路", "gender", "男",
			"reading", "あやのこうじ", "note", "第一人称用俺",
		)},
		"terms", []any{omap("source", "高度育成高校", "target", "高度育成高中", "type", "组织")},
	)
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return marshal(t, analysis), nil
	})
	a := NewAnalyzer(client, testCfg(t))
	result, err := a.Analyze("……样章……")
	if err != nil {
		t.Fatal(err)
	}
	if genre, _ := result.Get("genre"); genre != "校园" {
		t.Errorf("genre = %v", genre)
	}
	store := newTestGlossary(t)
	n, err := a.SeedGlossary(store, result)
	if err != nil || n != 2 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	character, _ := store.GetTerm("綾小路")
	organization, _ := store.GetTerm("高度育成高校")
	if character == nil || organization == nil {
		t.Fatal("术语未种入")
	}
	if character.Gender != "男" {
		t.Errorf("gender = %v", character.Gender)
	}
	if organization.Type != "组织" {
		t.Errorf("type = %v", organization.Type)
	}
	brief := a.StyleBrief(result)
	if !strings.Contains(brief, "绫小路") {
		t.Errorf("brief = %q", brief)
	}
}

func TestAnalyzerMalformedCollectionItemsAreFiltered(t *testing.T) {
	analysis := omap(
		"genre", omap("unexpected", true),
		"characters", []any{"bad", omap("source", "綾小路", "target", "绫小路")},
		"terms", []any{int64(1), omap("source", "学校", "target", "学校", "type", omap("bad", 1))},
	)
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return marshal(t, analysis), nil
	})
	analyzer := NewAnalyzer(client, testCfg(t))
	result, err := analyzer.Analyze("……样章……")
	if err != nil {
		t.Fatal(err)
	}
	if genre, _ := result.Get("genre"); genre != "" {
		t.Errorf("genre = %v", genre)
	}
	chars, _ := result.Get("characters")
	if len(chars.([]any)) != 1 {
		t.Errorf("characters = %v", chars)
	}
	terms, _ := result.Get("terms")
	if len(terms.([]any)) != 1 {
		t.Errorf("terms = %v", terms)
	}
	store := newTestGlossary(t)
	n, err := analyzer.SeedGlossary(store, result)
	if err != nil || n != 2 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	school, _ := store.GetTerm("学校")
	if school == nil {
		t.Fatal("学校未入库")
	}
	if school.Type != "术语" {
		t.Errorf("type = %v", school.Type)
	}
}

// ---- TestExtractor ----

func TestExtractorExistingContextOnlyIncludesTermsRepeatedInSourceCorpus(t *testing.T) {
	var promptsSeen []string
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		promptsSeen = append(promptsSeen, m[len(m)-1].Content)
		return marshal(t, omap("terms", []any{})), nil
	})
	extractor := NewExtractor(client, testCfg(t))
	store := newTestGlossary(t)
	_, _ = store.UpsertTerm(&glossary.Term{Source: "唯一术语", Target: "唯一译法", Status: "ok"}, nil)
	_, _ = store.UpsertTerm(&glossary.Term{Source: "重复术语", Target: "重复译法", Status: "ok"}, nil)

	_, err := extractor.ExtractAndStore(store, "本批原文。", "本批译文。", 0, nil, nil,
		"唯一术语只出现一次。重复术语出现，然后重复术语再次出现。")
	if err != nil {
		t.Fatal(err)
	}
	terms, _ := store.AllTerms()
	if len(terms) != 2 {
		t.Errorf("terms = %d", len(terms))
	}
	if len(promptsSeen) != 1 {
		t.Fatalf("prompts = %d", len(promptsSeen))
	}
	if strings.Contains(promptsSeen[0], "唯一术语 → 唯一译法") {
		t.Errorf("低频术语不应注入 prompt")
	}
	if !strings.Contains(promptsSeen[0], "重复术语 → 重复译法") {
		t.Errorf("高频术语应注入 prompt")
	}
}

func TestExtractorExtractAndStore(t *testing.T) {
	terms := omap("terms", []any{
		omap("source", "堀北", "target", "堀北", "type", "人物", "gender", "女", "aliases", []any{"堀北さん"}),
		omap("source", "屋上", "target", "天台", "type", "地名", "gender", "未知"),
	})
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return marshal(t, terms), nil
	})
	ext := NewExtractor(client, testCfg(t))
	store := newTestGlossary(t)
	summary, err := ext.ExtractAndStore(store, "原文", "译文", 1, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if summary["inserted"] != 2 {
		t.Errorf("summary = %v", summary)
	}
	horikita, _ := store.GetTerm("堀北")
	if horikita == nil {
		t.Fatal("堀北未入库")
	}
	if horikita.Gender != "女" || len(horikita.Aliases) != 1 || horikita.Aliases[0] != "堀北さん" {
		t.Errorf("horikita = %+v", horikita)
	}
	if horikita.FirstChapter == nil || *horikita.FirstChapter != 1 {
		t.Errorf("first_chapter = %v", horikita.FirstChapter)
	}
	rooftop, _ := store.GetTerm("屋上")
	if rooftop == nil {
		t.Fatal("屋上未入库")
	}
	if rooftop.Gender != "" {
		t.Errorf("未知 应被规整为空：%v", rooftop.Gender)
	}
}

func TestExtractorMalformedOptionalFieldsFallBackSafely(t *testing.T) {
	terms := omap("terms", []any{
		omap("source", "term", "target", "术语", "type", omap("bad", 1), "gender", []any{"bad"}, "aliases", int64(1), "note", omap("bad", 1)),
	})
	extractor := NewExtractor(llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return marshal(t, terms), nil
	}), testCfg(t))
	result, err := extractor.Extract("term", "术语", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("result = %v", result)
	}
	if result[0].Type != "术语" || result[0].Gender != "" || len(result[0].Aliases) != 0 || result[0].Note != "" {
		t.Errorf("result[0] = %+v", result[0])
	}
}

func TestExtractorMultipleNewTermsUseTargetsFromFirstTranslatedOccurrences(t *testing.T) {
	var calls []string
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		system := m[0].Content
		calls = append(calls, system)
		if strings.Contains(system, "术语一致性校准器") {
			user := m[len(m)-1].Content
			for _, want := range []string{"綾小路第一次走进教室。", "绫小路第一次走进教室。", "堀北站在窗边。", `"proposed_target": "掘北"`} {
				if !strings.Contains(user, want) {
					t.Errorf("校准 prompt 缺少 %q", want)
				}
			}
			return marshal(t, omap("terms", []any{
				omap("source", "綾小路", "target", "绫小路"),
				omap("source", "堀北", "target", "堀北"),
			})), nil
		}
		return marshal(t, omap("terms", []any{
			omap("source", "綾小路", "target", "凌小路", "type", "人物"),
			omap("source", "堀北", "target", "掘北", "type", "人物"),
		})), nil
	})
	extractor := NewExtractor(client, testCfg(t))
	history := []TranslatedSegmentEvidence{
		{Chapter: 0, Segment: 3, Source: "綾小路第一次走进教室。", Target: "绫小路第一次走进教室。"},
		{Chapter: 0, Segment: 4, Source: "堀北站在窗边。", Target: "堀北站在窗边。"},
	}
	store := newTestGlossary(t)
	before := [2]int{2, 0}
	summary, err := extractor.ExtractAndStore(store, "后来綾小路和堀北再次出现。", "后来凌小路和掘北再次出现。", 2, history, &before, "")
	if err != nil {
		t.Fatal(err)
	}
	ayanokoji, _ := store.GetTerm("綾小路")
	horikita, _ := store.GetTerm("堀北")
	if ayanokoji == nil || horikita == nil {
		t.Fatal("术语未入库")
	}
	if ayanokoji.Target != "绫小路" || horikita.Target != "堀北" {
		t.Errorf("targets = %q %q", ayanokoji.Target, horikita.Target)
	}
	if ayanokoji.FirstChapter == nil || *ayanokoji.FirstChapter != 0 || horikita.FirstChapter == nil || *horikita.FirstChapter != 0 {
		t.Errorf("first_chapter = %v %v", ayanokoji.FirstChapter, horikita.FirstChapter)
	}
	if summary["history_matched"] != 2 || summary["history_aligned"] != 2 || summary["history_unresolved"] != 0 {
		t.Errorf("summary = %v", summary)
	}
	if len(calls) != 2 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestExtractorNewTermWithoutPriorOccurrenceIsInsertedDirectly(t *testing.T) {
	terms := omap("terms", []any{omap("source", "綾小路", "target", "绫小路", "type", "人物")})
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return marshal(t, terms), nil
	})
	extractor := NewExtractor(client, testCfg(t))
	store := newTestGlossary(t)
	before := [2]int{0, 0}
	summary, err := extractor.ExtractAndStore(store, "綾小路第一次出现。", "绫小路第一次出现。", 0, nil, &before, "")
	if err != nil {
		t.Fatal(err)
	}
	term, _ := store.GetTerm("綾小路")
	if term == nil {
		t.Fatal("术语未入库")
	}
	if summary["history_matched"] != 0 {
		t.Errorf("summary = %v", summary)
	}
	if len(client.Calls) != 1 {
		t.Errorf("calls = %d", len(client.Calls))
	}
}

func TestExtractorUnresolvedHistoricalTermIsNotLockedToLaterTranslation(t *testing.T) {
	responses := []string{
		marshal2(`{"terms": [{"source": "綾小路", "target": "凌小路"}]}`),
		marshal2(`{"terms": [{"source": "綾小路", "target": ""}]}`),
	}
	idx := 0
	extractor := NewExtractor(llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		r := responses[idx]
		idx++
		return r, nil
	}), testCfg(t))
	history := []TranslatedSegmentEvidence{
		{Chapter: 0, Segment: 0, Source: "綾小路がいた。", Target: "他在那里。"},
	}
	store := newTestGlossary(t)
	before := [2]int{1, 0}
	summary, err := extractor.ExtractAndStore(store, "綾小路が戻った。", "凌小路回来了。", 1, history, &before, "")
	if err != nil {
		t.Fatal(err)
	}
	term, _ := store.GetTerm("綾小路")
	if term != nil {
		t.Errorf("未决术语不应入库：%+v", term)
	}
	if summary["history_unresolved"] != 1 {
		t.Errorf("summary = %v", summary)
	}
}

func marshal2(s string) string { return s }

