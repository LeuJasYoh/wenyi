package glossary

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

// 迁移自 tests/test_glossary.py。

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func intp(i int) *int { return &i }

func TestInsertAndLookup(t *testing.T) {
	store := newTestStore(t)
	r, err := store.UpsertTerm(&Term{
		Source: "綾小路", Target: "绫小路", Type: TypePerson, Gender: "男",
		Aliases: []string{"綾小路くん"}, Reading: "あやのこうじ", Status: "ok",
	}, intp(0))
	if err != nil || r != "inserted" {
		t.Fatalf("r = %q err = %v", r, err)
	}
	term, err := store.GetTerm("綾小路")
	if err != nil || term == nil {
		t.Fatal(err)
	}
	if term.Target != "绫小路" || term.Gender != "男" {
		t.Errorf("term = %+v", term)
	}
}

func TestTermsInTextMatchesAlias(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.UpsertTerm(&Term{Source: "綾小路", Target: "绫小路", Aliases: []string{"綾小路くん"}, Status: "ok"}, nil)
	hits, err := store.TermsInText("「おはよう、綾小路くん」と堀北が言った。")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Source != "綾小路" {
		t.Errorf("hits = %+v", hits)
	}
}

func TestTermsInTextNormalizesCaseAndCharacterWidth(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.UpsertTerm(&Term{Source: "OpenAI", Target: "开放人工智能", Status: "ok"}, nil)
	_, _ = store.UpsertTerm(&Term{Source: "ＡＢＣ", Target: "ABC 组织", Status: "ok"}, nil)
	hits, err := store.TermsInText("openai 与 ABC")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, h := range hits {
		sources[h.Source] = true
	}
	if !sources["OpenAI"] || !sources["ＡＢＣ"] || len(sources) != 2 {
		t.Errorf("sources = %v", sources)
	}
}

func TestASCIISourceMatchRespectsWordBoundaries(t *testing.T) {
	if !SourceMatchesText("Ann", "Ann opened the door.") {
		t.Errorf("Ann 应命中")
	}
	if !SourceMatchesText("ANN", "ann opened the door.") {
		t.Errorf("ANN（大小写归一）应命中")
	}
	if SourceMatchesText("Ann", "Anna opened the door.") {
		t.Errorf("Ann 不应命中 Anna")
	}
}

func TestCyrillicSourceMatchRespectsWordBoundaries(t *testing.T) {
	if !SourceMatchesText("гад", "Этот гад снова пришёл.") {
		t.Errorf("гад 应命中")
	}
	if SourceMatchesText("гад", "Этот гадкий человек снова пришёл.") {
		t.Errorf("гад 不应命中 гадкий")
	}
}

func TestAppellationDoesNotMatchBareNameAlias(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.UpsertTerm(&Term{
		Source: "夏帆ちゃん", Target: "小夏帆", Type: TypeAppellation, Aliases: []string{"夏帆"}, Status: "ok",
	}, nil)
	hits, _ := store.TermsInText("夏帆は窓の外を見た。")
	if len(hits) != 0 {
		t.Errorf("裸名 alias 不应命中称谓：%v", hits)
	}
	hits, _ = store.TermsInText("「夏帆ちゃん」と母親が言った。")
	if len(hits) != 1 || hits[0].Source != "夏帆ちゃん" {
		t.Errorf("hits = %+v", hits)
	}
}

func TestRecurringTermsRequireTwoFullTextOccurrences(t *testing.T) {
	terms := []*Term{
		{Source: "唯一术语", Target: "Unique", Status: "ok"},
		{Source: "重复术语", Target: "Repeated", Status: "ok"},
		{Source: "AliasCanonical", Target: "Alias", Aliases: []string{"别名"}, Status: "ok"},
		{Source: "夏帆ちゃん", Target: "小夏帆", Type: TypeAppellation, Aliases: []string{"夏帆"}, Status: "ok"},
	}
	corpus := "唯一术语。重复术语再次成为重复术语。别名先来，别名再来。夏帆出现两次，夏帆。"
	recurring := RecurringTerms(terms, corpus, 2)
	sources := map[string]bool{}
	for _, t := range recurring {
		sources[t.Source] = true
	}
	if len(sources) != 2 || !sources["重复术语"] || !sources["AliasCanonical"] {
		t.Errorf("recurring = %v", sources)
	}

	overlappingAlias := &Term{Source: "夏帆", Target: "Kaho", Aliases: []string{"夏帆ちゃん"}, Status: "ok"}
	if out := RecurringTerms([]*Term{overlappingAlias}, "夏帆ちゃん只在全文出现一次。", 2); len(out) != 0 {
		t.Errorf("重叠 alias 应合并计数为 1：%v", out)
	}

	cyrillic := &Term{Source: "гад", Target: "畜生", Status: "ok"}
	if out := RecurringTerms([]*Term{cyrillic}, "Один гад ушёл, но гадкий человек остался гадким.", 2); len(out) != 0 {
		t.Errorf("边界排除：%v", out)
	}
	if out := RecurringTerms([]*Term{cyrillic}, "Один гад ушёл, затем другой гад пришёл.", 2); len(out) != 1 {
		t.Errorf("两次出现应命中：%v", out)
	}
}

func TestConflictKeepsCurrentUntilResolved(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.UpsertTerm(&Term{Source: "堀北", Target: "堀北", Status: "ok"}, intp(0))
	r, err := store.UpsertTerm(&Term{Source: "堀北", Target: "掘北", Status: "ok"}, intp(1))
	if err != nil || r != "conflict" {
		t.Fatalf("r = %q err = %v", r, err)
	}
	term, _ := store.GetTerm("堀北")
	if term.Target != "堀北" {
		t.Errorf("冲突时保留现译法：%v", term.Target)
	}
	conflicts, _ := store.OpenConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d", len(conflicts))
	}
	hit, _ := store.ResolveTerm("堀北", "掘北")
	if !hit {
		t.Fatal("resolve 应命中")
	}
	_ = store.MarkConflictsResolved("堀北")
	term, _ = store.GetTerm("堀北")
	if term.Target != "掘北" || term.Status != "ok" {
		t.Errorf("term = %+v", term)
	}
	conflicts, _ = store.OpenConflicts()
	if len(conflicts) != 0 {
		t.Errorf("conflicts = %v", conflicts)
	}
}

func TestConcurrentUpsertsMakeOneAtomicConflictDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	initial, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	initial.Close()
	var barrier sync.WaitGroup
	barrier.Add(2)
	results := make([]string, 2)
	errs := make([]error, 2)
	for i, target := range []string{"译名甲", "译名乙"} {
		go func(i int, target string) {
			defer barrier.Done()
			store, err := OpenStore(path)
			if err != nil {
				errs[i] = err
				return
			}
			defer store.Close()
			results[i], errs[i] = store.UpsertTerm(&Term{Source: "Name", Target: target, Status: "ok"}, intp(1))
		}(i, target)
	}
	barrier.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	sorted := results[:]
	if sorted[0] > sorted[1] {
		sorted[0], sorted[1] = sorted[1], sorted[0]
	}
	if sorted[0] != "conflict" || sorted[1] != "inserted" {
		t.Errorf("results = %v", results)
	}
	check, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	terms, _ := check.AllTerms()
	if len(terms) != 1 {
		t.Errorf("terms = %d", len(terms))
	}
	conflicts, _ := check.OpenConflicts()
	if len(conflicts) != 1 {
		t.Errorf("conflicts = %d", len(conflicts))
	}
}

func TestOpeningStoreRemovesLegacyTranslationMemoryTable(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE translation_memory (
                    source_hash TEXT PRIMARY KEY,
                    source_text TEXT NOT NULL,
                    target_text TEXT NOT NULL
                )`)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = raw.Exec("INSERT INTO translation_memory VALUES ('hash', 'source', 'target')")
	_ = raw.Close()

	store2, err := OpenStore(filepath.Join(dir, "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	var n int
	if err := store2.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='translation_memory'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("legacy translation_memory 表应被删除")
	}
}

func TestStats(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.UpsertTerm(&Term{Source: "A", Target: "甲", Status: "ok"}, nil)
	s, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s["terms"] != 1 || s["open_conflicts"] != 0 {
		t.Errorf("stats = %v", s)
	}
}
