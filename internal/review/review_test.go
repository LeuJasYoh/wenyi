package review

// 迁移自 tests/test_review_agent.py（TestBookEvidenceIndex / TestReviewFixer /
// TestReviewRunStore / TestReviewAgentLoop / TestReviewConflictArbiter）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/ingest"
	"wenyi/internal/llm"
)

func reviewTestConfig(t *testing.T) *config.Config {
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
		"pipeline": map[string]any{
			"review_agent_max_evidence_rounds": 2,
			"review_agent_tier":                "strong",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func testChapter(index int, texts [][2]string) *ingest.Chapter {
	segs := make([]*ingest.Segment, len(texts))
	for i, pair := range texts {
		target := pair[1]
		segs[i] = &ingest.Segment{
			Index: i, Source: pair[0], Kind: "text",
			Target: &target, Meta: map[string]any{},
		}
	}
	return &ingest.Chapter{
		Index: index, Title: fmt.Sprintf("Chapter %d", index),
		Segments: segs, Meta: map[string]any{"source_digest": fmt.Sprintf("Digest %d", index)},
	}
}

func marshalT(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- TestBookEvidenceIndex ----

func newEvidenceFixture() (*BookEvidenceIndex, []*ingest.Chapter, *glossary.Term) {
	chapters := []*ingest.Chapter{
		testChapter(0, [][2]string{
			{"Ann arrived.", "安到了。"}, {"Anna left.", "安娜走了。"}, {"Ann spoke.", "安开口了。"},
		}),
		testChapter(1, [][2]string{{"ANN returned.", "安回来了。"}, {"End.", "结束。"}}),
	}
	term := &glossary.Term{Source: "Ann", Target: "安", Aliases: []string{"Annie"}, Type: "人物", Status: "ok"}
	idx := NewBookEvidenceIndex(chapters, []*glossary.Term{term}, map[string]any{
		"style_guide": "克制", "book_synopsis": "安离开后归来。",
	}, nil)
	return idx, chapters, term
}

func TestSelectedOccurrencesUseBookOrderAliasAndASCIIBoundaries(t *testing.T) {
	idx, _, _ := newEvidenceFixture()
	result := idx.TermOccurrences(map[string]any{
		"term": "Annie", "selectors": []any{1, 2, "last"}, "context_radius": 0,
	})
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("result = %v", result)
	}
	if result["canonical_term"] != "Ann" {
		t.Errorf("canonical = %v", result["canonical_term"])
	}
	if result["total_matches"] != 3 {
		t.Errorf("total = %v", result["total_matches"])
	}
	selected, _ := result["selected"].([]map[string]any)
	ordinals := make([]int, 0, len(selected))
	for _, item := range selected {
		o, _ := StrictInt(item["ordinal"])
		ordinals = append(ordinals, o)
	}
	if len(ordinals) != 3 || ordinals[0] != 1 || ordinals[1] != 2 || ordinals[2] != 3 {
		t.Errorf("ordinals = %v", ordinals)
	}
	for _, item := range selected {
		if item["source"] == "Anna left." {
			t.Errorf("Anna 不应命中")
		}
	}
}

func TestGlossaryToolReturnsOnlyRequestedCanonicalTerm(t *testing.T) {
	idx, _, _ := newEvidenceFixture()
	result := idx.Execute(map[string]any{
		"request_id": "glossary-1", "tool": "glossary_term",
		"arguments": map[string]any{"term": "Annie"},
	})
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("result = %v", result)
	}
	if result["request_id"] != "glossary-1" {
		t.Errorf("request_id = %v", result["request_id"])
	}
	term, _ := result["term"].(map[string]any)
	if term["source"] != "Ann" || term["target"] != "安" {
		t.Errorf("term = %v", term)
	}
	aliases, _ := term["aliases"].([]string)
	if len(aliases) != 1 || aliases[0] != "Annie" {
		t.Errorf("aliases = %v", term["aliases"])
	}
	refs := idx.EvidenceRefs(result)
	termRef := "glossary:" + termRefHash("Ann")
	if len(refs) != 1 || !refs[termRef] {
		t.Errorf("refs = %v (want %s)", refs, termRef)
	}
}

func termRefHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:16]
}

func TestExactSourceWinsOverAnotherTermsSameAlias(t *testing.T) {
	_, chapters, term := newEvidenceFixture()
	other := &glossary.Term{Source: "Anne", Target: "安妮", Aliases: []string{"Ann"}, Type: "人物", Status: "ok"}
	idx := NewBookEvidenceIndex(chapters, []*glossary.Term{term, other}, map[string]any{}, nil)
	got, ambiguous := idx.CanonicalTerm("Ann")
	if got != term || len(ambiguous) != 0 {
		t.Errorf("got = %v ambiguous = %v", got, ambiguous)
	}
}

func TestExactCaseSensitiveSourceWinsAndNormalizedCollisionIsAmbiguous(t *testing.T) {
	upper := &glossary.Term{Source: "ANN", Target: "甲", Aliases: []string{"Alice"}, Type: "人物", Status: "ok"}
	title := &glossary.Term{Source: "Ann", Target: "乙", Aliases: []string{"Annie"}, Type: "人物", Status: "ok"}
	chapters := []*ingest.Chapter{testChapter(0, [][2]string{
		{"Alice arrived.", "甲到了。"}, {"Annie left.", "乙走了。"},
	})}
	idx := NewBookEvidenceIndex(chapters, []*glossary.Term{upper, title}, map[string]any{}, nil)
	if got, _ := idx.CanonicalTerm("ANN"); got != upper {
		t.Errorf("ANN")
	}
	if got, _ := idx.CanonicalTerm("Ann"); got != title {
		t.Errorf("Ann")
	}
	got, ambiguous := idx.CanonicalTerm("ann")
	if got != nil || len(ambiguous) != 2 || ambiguous[0] != "ANN" || ambiguous[1] != "Ann" {
		t.Errorf("ambiguous = %v", ambiguous)
	}
	refUpper := idx.GlossaryTerm(map[string]any{"term": "ANN"})["term"].(map[string]any)["ref"]
	refTitle := idx.GlossaryTerm(map[string]any{"term": "Ann"})["term"].(map[string]any)["ref"]
	if refUpper == refTitle {
		t.Errorf("ref 应不同")
	}
	selUpper := idx.TermOccurrences(map[string]any{"term": "ANN", "selectors": []any{1}})["selected"].([]map[string]any)
	if selUpper[0]["source"] != "Alice arrived." {
		t.Errorf("upper = %v", selUpper[0]["source"])
	}
	selTitle := idx.TermOccurrences(map[string]any{"term": "Ann", "selectors": []any{1}})["selected"].([]map[string]any)
	if selTitle[0]["source"] != "Annie left." {
		t.Errorf("title = %v", selTitle[0]["source"])
	}
}

func TestDistinctExactSourcesAreNotMergedIntoOneConflictKey(t *testing.T) {
	_, chapters, _ := newEvidenceFixture()
	upper := &glossary.Term{Source: "ANN", Target: "甲", Type: "人物", Status: "ok"}
	title := &glossary.Term{Source: "Ann", Target: "乙", Type: "人物", Status: "ok"}
	evidence := NewBookEvidenceIndex(chapters, []*glossary.Term{upper, title}, map[string]any{}, nil)
	mk := func(source string, proposed string, index int) map[string]any {
		return map[string]any{
			"chapter": 0, "index": index, "_chunk_id": fmt.Sprintf("chunk-%d", index),
			"type": "terminology", "detail": "译名问题", "suggestion": proposed,
			"consistency": map[string]any{"kind": "term", "subject_source": source, "proposed_value": proposed},
		}
	}
	issues := NormalizeReviewIssues([]map[string]any{mk("ANN", "甲", 0), mk("Ann", "乙", 1)}, evidence)
	cons0 := issues[0]["consistency"].(map[string]any)
	cons1 := issues[1]["consistency"].(map[string]any)
	if cons0["key"] == cons1["key"] {
		t.Errorf("key 不应相同")
	}
	if groups := BuildConflictGroups(issues); len(groups) != 0 {
		t.Errorf("groups = %v", groups)
	}
	var ambiguous []map[string]any
	ambiguous = append(ambiguous, map[string]any{
		"chapter": 0, "index": 0, "_chunk_id": "ambiguous-0",
		"type": "terminology", "detail": "译名问题", "suggestion": "甲",
		"consistency": map[string]any{"kind": "term", "subject_source": "ann", "proposed_value": "甲"},
	})
	ambiguous = append(ambiguous, map[string]any{
		"chapter": 0, "index": 1, "_chunk_id": "ambiguous-1",
		"type": "terminology", "detail": "译名问题", "suggestion": "乙",
		"consistency": map[string]any{"kind": "term", "subject_source": "ann", "proposed_value": "乙"},
	})
	ambiguousIssues := NormalizeReviewIssues(ambiguous, evidence)
	for _, issue := range ambiguousIssues {
		if auto, _ := issue["consistency"].(map[string]any)["auto_arbitration"].(bool); auto {
			t.Errorf("歧义应 auto_arbitration=false")
		}
	}
	if groups := BuildConflictGroups(ambiguousIssues); len(groups) != 0 {
		t.Errorf("ambiguous groups = %v", groups)
	}
}

func TestBookContextHasStableRefsAndRejectsUnknownChapter(t *testing.T) {
	idx, _, _ := newEvidenceFixture()
	style := idx.Execute(map[string]any{
		"request_id": "style-1", "tool": "book_context",
		"arguments": map[string]any{"section": "style_guide"},
	})
	digest := idx.Execute(map[string]any{
		"request_id": "digest-1", "tool": "book_context",
		"arguments": map[string]any{"section": "chapter_digest", "chapter": 1},
	})
	unknown := idx.BookContext(map[string]any{"section": "chapter_digest", "chapter": 99})
	if refs := idx.EvidenceRefs(style); len(refs) != 1 || !refs["book:style_guide"] {
		t.Errorf("style refs = %v", refs)
	}
	if refs := idx.EvidenceRefs(digest); len(refs) != 1 || !refs["book:chapter_digest:ch1"] {
		t.Errorf("digest refs = %v", refs)
	}
	if unknown["ok"] != false || unknown["error"] != "chapter_not_found" {
		t.Errorf("unknown = %v", unknown)
	}
}

func TestOversizedEvidenceResultIsRejected(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var texts [][2]string
	for i := 0; i < 8; i++ {
		texts = append(texts, [2]string{fmt.Sprintf("Ann %d %s", i, long), long})
	}
	_, _, term := newEvidenceFixture()
	idx := NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, texts)}, []*glossary.Term{term}, map[string]any{}, nil)
	selectors := make([]any, 8)
	for i := range selectors {
		selectors[i] = i + 1
	}
	result := idx.Execute(map[string]any{
		"request_id": "large-1", "tool": "term_occurrences",
		"arguments": map[string]any{"term": "Ann", "selectors": selectors, "context_radius": 2},
	})
	if result["ok"] != false || result["error"] != "evidence_result_too_large" {
		t.Errorf("result = %v", result)
	}
}

func TestSegmentContextCrossesChapterBoundary(t *testing.T) {
	idx, _, _ := newEvidenceFixture()
	result := idx.SegmentContext(map[string]any{"chapter": 1, "index": 0, "before": 1, "after": 1})
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("result = %v", result)
	}
	segs := result["segments"].([]map[string]any)
	sources := make([]string, 0, len(segs))
	for _, s := range segs {
		sources = append(sources, s["source"].(string))
	}
	want := []string{"Ann spoke.", "ANN returned.", "End."}
	if len(sources) != 3 || sources[0] != want[0] || sources[1] != want[1] || sources[2] != want[2] {
		t.Errorf("sources = %v", sources)
	}
}

func TestTargetOverridesAreVisibleWithoutMutatingChapters(t *testing.T) {
	_, chapters, term := newEvidenceFixture()
	original := *chapters[0].TextSegments()[0].Target
	idx := NewBookEvidenceIndex(chapters, []*glossary.Term{term}, map[string]any{},
		map[LocationKey]string{{0, 0}: "影子修订。"})
	context := idx.SegmentContext(map[string]any{"chapter": 0, "index": 0, "before": 0, "after": 0})
	if idx.Segments[0].Target != "影子修订。" {
		t.Errorf("shadow target")
	}
	seg := context["segments"].([]map[string]any)[0]
	if seg["target"] != "影子修订。" || seg["target_origin"] != "shadow_override" {
		t.Errorf("seg = %v", seg)
	}
	if seg["baseline_target"] != original {
		t.Errorf("baseline = %v", seg["baseline_target"])
	}
	if *chapters[0].TextSegments()[0].Target != original {
		t.Errorf("正式章节被改动")
	}
}

func TestFormalTargetsAreLabeledWithoutDuplicateBaselinePayload(t *testing.T) {
	idx, _, _ := newEvidenceFixture()
	context := idx.SegmentContext(map[string]any{"chapter": 0, "index": 0, "before": 0, "after": 0})
	seg := context["segments"].([]map[string]any)[0]
	if seg["target_origin"] != "formal" {
		t.Errorf("origin = %v", seg["target_origin"])
	}
	if _, has := seg["baseline_target"]; has {
		t.Errorf("formal 不应有 baseline_target")
	}
}

// ---- TestReviewFixer ----

func fixerPropose(t *testing.T, payload string) (ProvisionalPatch, error) {
	t.Helper()
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return payload, nil
	})
	return NewReviewFixer(client, reviewTestConfig(t)).Propose(1, "ch0:text1:seg1", 0, 1,
		"Original sentence.", "当前译文。", []map[string]any{{
			"issue_id": "r1-review-00001", "chapter": 0, "index": 1,
			"type": "mistranslation", "detail": "原意不完整", "suggestion": "补全信息",
		}}, FixerOptions{})
}

func validFixerPayload(replacement string) string {
	return `{"segment_ref": "ch0:text1:seg1", "before_hash": "` + TargetHash("当前译文。") + `", "issue_ids": ["r1-review-00001"], "replacement": "` + replacement + `", "complete": true}`
}

func TestValidFullSegmentPatchIsProvisional(t *testing.T) {
	patch, err := fixerPropose(t, validFixerPayload("修订后的完整译文。"))
	if err != nil {
		t.Fatal(err)
	}
	if patch.Before != "当前译文。" || patch.After != "修订后的完整译文。" {
		t.Errorf("patch = %+v", patch)
	}
	if len(patch.IssueIDs) != 1 || patch.IssueIDs[0] != "r1-review-00001" {
		t.Errorf("issue_ids = %v", patch.IssueIDs)
	}
	if patch.Status != "provisional" {
		t.Errorf("status = %v", patch.Status)
	}
}

func TestRejectsProtocolDriftAndUnchangedReplacement(t *testing.T) {
	wrongIDs := strings.Replace(validFixerPayload("修订。"), "r1-review-00001", "another-issue", 1)
	extraField := strings.Replace(validFixerPayload("修订。"), `"complete": true`, `"explanation": "不允许", "complete": true`, 1)
	cases := []struct {
		payload string
		reason  string
	}{
		{wrongIDs, "issue_ids_mismatch"},
		{extraField, "unexpected_fields"},
		{validFixerPayload("当前译文。"), "unchanged_replacement"},
	}
	for _, tc := range cases {
		_, err := fixerPropose(t, tc.payload)
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("reason %s: err = %v", tc.reason, err)
		}
	}
}

// ---- TestReviewRunStore ----

func TestCandidateIDFormatWithAndWithoutRound(t *testing.T) {
	if got := ReviewCandidateID(2, 10, 3, nil); got != "ch2-base10-candidate3" {
		t.Errorf("got = %s", got)
	}
	four := 4
	if got := ReviewCandidateID(2, 10, 3, &four); got != "r4-ch2-base10-candidate3" {
		t.Errorf("got = %s", got)
	}
}

func TestEqualTimestampsNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	moment := time.Date(2026, 7, 27, 12, 30, 0, 0, time.UTC)
	first, err := NewReviewRunStore(dir, &moment)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewReviewRunStore(dir, &moment)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunDir == second.RunDir {
		t.Errorf("目录应不同")
	}
	fi1, _ := os.Stat(first.RunDir)
	fi2, _ := os.Stat(second.RunDir)
	if !fi1.IsDir() || !fi2.IsDir() {
		t.Errorf("目录应存在")
	}
	if strings.Contains(filepath.Base(first.RunDir), ":") {
		t.Errorf("目录名含冒号：%s", first.RunDir)
	}
}

func TestRoundScopesIsolateFilesEventsAndIssueSnapshots(t *testing.T) {
	dir := t.TempDir()
	debug, err := NewReviewRunStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rd := range []struct {
		round  int
		detail string
	}{{1, "第一轮"}, {2, "第二轮"}} {
		reviewRound, detail := rd.round, rd.detail
		unlock, err := debug.RoundScope(reviewRound)
		if err != nil {
			t.Fatal(err)
		}
		_ = debug.WriteJSON("agents/same.json", map[string]any{"detail": detail})
		debug.RecordInitialIssues(0, 0, []map[string]any{{
			"index": 0, "type": "missing", "detail": detail, "suggestion": "修复",
		}})
		_ = debug.LogEvent("round_probe", nil)
		unlock()
	}
	firstRaw, _ := os.ReadFile(filepath.Join(debug.RunDir, "rounds", "001", "agents", "same.json"))
	secondRaw, _ := os.ReadFile(filepath.Join(debug.RunDir, "rounds", "002", "agents", "same.json"))
	var first, second map[string]any
	_ = json.Unmarshal(firstRaw, &first)
	_ = json.Unmarshal(secondRaw, &second)
	if first["detail"] != "第一轮" || second["detail"] != "第二轮" {
		t.Errorf("details = %v %v", first, second)
	}
	one, two := 1, 2
	firstIssues, _ := debug.ResultSnapshots(&one)
	secondIssues, _ := debug.ResultSnapshots(&two)
	if len(firstIssues) != 1 || len(secondIssues) != 1 {
		t.Fatalf("issues = %v %v", firstIssues, secondIssues)
	}
	if firstIssues[0]["review_round"] != 1 || secondIssues[0]["review_round"] != 2 {
		t.Errorf("rounds")
	}
	if firstIssues[0]["candidate_id"] == secondIssues[0]["candidate_id"] {
		t.Errorf("candidate_id 应不同")
	}
	eventsRaw, _ := os.ReadFile(filepath.Join(debug.RunDir, "events.jsonl"))
	var rounds []any
	for _, line := range strings.Split(strings.TrimSpace(string(eventsRaw)), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		rounds = append(rounds, ev["review_round"])
	}
	if len(rounds) != 2 || rounds[0].(float64) != 1 || rounds[1].(float64) != 2 {
		t.Errorf("rounds = %v", rounds)
	}
}

// ---- TestReviewAgentLoop ----

func agentLoopEvidence() *BookEvidenceIndex {
	return NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, [][2]string{
			{"Ann arrived.", "安到了。"}, {"Ann spoke.", "安开口了。"},
		})},
		[]*glossary.Term{{Source: "Ann", Target: "安", Type: "人物", Status: "ok"}},
		map[string]any{}, nil)
}

func TestRequestsSelectedEvidenceThenConfirmsAndAdds(t *testing.T) {
	calls := 0
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		calls++
		if calls == 1 {
			return `{"action":"request_evidence","requests":[{"request_id":"term-1","tool":"term_occurrences","arguments":{"term":"Ann","selectors":[1],"context_radius":0}}],"complete":false}`, nil
		}
		return `{"action":"final","decisions":[{"candidate_id":"ch0-base0-candidate0","verdict":"confirmed","detail":"译名不统一","suggestion":"统一译为安","reason":"","consistency":{"subject_source":"Ann","kind":"term","proposed_value":"安"},"evidence_refs":["ch0:text0:seg0"]}],"new_issues":[{"index":1,"type":"pronoun","detail":"代词错误","suggestion":"改为她","consistency":{"subject_source":"Ann","kind":"pronoun","proposed_value":"她"},"evidence_refs":[]}],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived.", "Ann spoke."}, []string{"安到了。", "安开口了。"},
		[]map[string]any{{
			"index": 0, "type": "terminology", "detail": "疑似译名错误", "suggestion": "核对译名",
		}}, nil)
	traceRaw, err := os.ReadFile(filepath.Join(debug.RunDir, "agents", "chunk-ch0-base0-n2.json"))
	if err != nil {
		t.Fatalf("trace 缺失：%v", err)
	}
	var trace map[string]any
	_ = json.Unmarshal(traceRaw, &trace)
	eventsRaw, _ := os.ReadFile(filepath.Join(debug.RunDir, "events.jsonl"))
	hasEvidenceEvent := false
	for _, line := range strings.Split(strings.TrimSpace(string(eventsRaw)), "\n") {
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil && ev["event"] == "review_evidence_supplied" {
			hasEvidenceEvent = true
		}
	}
	if calls != 2 {
		t.Errorf("calls = %d", calls)
	}
	if len(outcome.Issues) != 2 {
		t.Fatalf("issues = %v", outcome.Issues)
	}
	if outcome.Issues[0]["suggestion"] != "统一译为安" {
		t.Errorf("suggestion = %v", outcome.Issues[0]["suggestion"])
	}
	refs := outcome.Issues[0]["evidence_refs"].([]string)
	if len(refs) == 0 || !containsStr(refs, "ch0:text0:seg0") {
		t.Errorf("refs = %v", refs)
	}
	if outcome.Issues[1]["origin"] != "agent" {
		t.Errorf("origin = %v", outcome.Issues[1]["origin"])
	}
	if outcome.FallbackReason != "" {
		t.Errorf("fallback = %v", outcome.FallbackReason)
	}
	if trace["status"] != "finished" {
		t.Errorf("trace status = %v", trace["status"])
	}
	if !hasEvidenceEvent {
		t.Errorf("缺少 review_evidence_supplied 事件")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestDismissedSummaryIsSelfContainedAndLinksToInitialCandidate(t *testing.T) {
	initial := map[string]any{
		"index": 0, "type": "terminology", "detail": "疑似译名错误", "suggestion": "核对译名",
	}
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"action":"final","decisions":[{"candidate_id":"ch0-base0-candidate0","verdict":"dismissed","detail":"","suggestion":"","reason":"术语表和首处译法均支持当前译文。","consistency":{},"evidence_refs":[]}],"new_issues":[],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	debug.RecordInitialIssues(0, 0, []map[string]any{initial})
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived."}, []string{"安到了。"}, []map[string]any{initial}, nil)
	debug.RecordDismissed(0, 0, outcome.Dismissed)
	initialRows, dismissedRows := debug.ResultSnapshots(nil)
	if len(outcome.Issues) != 0 {
		t.Errorf("issues = %v", outcome.Issues)
	}
	if len(dismissedRows) != 1 || len(initialRows) != 1 {
		t.Fatalf("rows")
	}
	if dismissedRows[0]["candidate_id"] != initialRows[0]["candidate_id"] {
		t.Errorf("candidate 链接断开")
	}
	for _, field := range []string{"type", "detail", "suggestion", "reason"} {
		if dismissedRows[0][field] == nil || dismissedRows[0][field] == "" {
			t.Errorf("dismissed 缺少 %s", field)
		}
	}
}

func TestCurrentSegmentRefIsVisibleInPrompt(t *testing.T) {
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		user := m[len(m)-1].Content
		if !strings.Contains(user, `"ref": "ch0:text0:seg0"`) {
			t.Errorf("缺 segment_refs_json：%s", user[:min2(200, len(user))])
		}
		if !strings.Contains(user, "ref=ch0:text0:seg0") {
			t.Errorf("缺 pairs ref")
		}
		return `{"action":"final","decisions":[{"candidate_id":"ch0-base0-candidate0","verdict":"confirmed","detail":"确认","suggestion":"修正","reason":"","consistency":{},"evidence_refs":[]}],"new_issues":[],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived."}, []string{"安到了。"},
		[]map[string]any{{"index": 0, "type": "missing", "detail": "候选", "suggestion": "修正"}}, nil)
	refs := outcome.Issues[0]["evidence_refs"].([]string)
	if len(refs) != 1 || refs[0] != "ch0:text0:seg0" {
		t.Errorf("refs = %v", refs)
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestOutOfChunkNewIssueFallsBackToInitialCandidates(t *testing.T) {
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"action":"final","decisions":[{"candidate_id":"ch0-base0-candidate0","verdict":"dismissed","reason":"误报","detail":"","suggestion":"","consistency":{},"evidence_refs":[]}],"new_issues":[{"index":2,"type":"missing","detail":"越界","suggestion":"补译","consistency":{},"evidence_refs":[]}],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	initial := map[string]any{"index": 0, "type": "missing", "detail": "初审候选", "suggestion": "补译"}
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived."}, []string{"安到了。"}, []map[string]any{initial}, nil)
	if outcome.FallbackReason == "" {
		t.Errorf("应回退")
	}
	if outcome.Issues[0]["detail"] != "初审候选" {
		t.Errorf("detail = %v", outcome.Issues[0]["detail"])
	}
	if b, _ := outcome.Issues[0]["agent_fallback"].(bool); !b {
		t.Errorf("agent_fallback")
	}
}

func TestNonemptyInvalidConsistencyFallsBack(t *testing.T) {
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"action":"final","decisions":[{"candidate_id":"ch0-base0-candidate0","verdict":"confirmed","detail":"候选","suggestion":"修正","reason":"","consistency":{"kind":"typo","subject_source":"Ann","proposed_value":"安"},"evidence_refs":[]}],"new_issues":[],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	initial := map[string]any{"index": 0, "type": "missing", "detail": "初审候选", "suggestion": "补译"}
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived."}, []string{"安到了。"}, []map[string]any{initial}, nil)
	if !strings.Contains(outcome.FallbackReason, "invalid_consistency") {
		t.Errorf("fallback = %v", outcome.FallbackReason)
	}
	if outcome.Issues[0]["detail"] != "初审候选" {
		t.Errorf("detail = %v", outcome.Issues[0]["detail"])
	}
}

func TestThirdEvidenceRequestAfterTwoRoundsFallsBack(t *testing.T) {
	calls := 0
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		calls++
		return fmt.Sprintf(`{"action":"request_evidence","requests":[{"request_id":"request-%d","tool":"segment_context","arguments":{"chapter":0,"index":0,"before":%d,"after":0}}],"complete":false}`, calls, calls-1), nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived."}, []string{"安到了。"},
		[]map[string]any{{"index": 0, "type": "missing", "detail": "候选", "suggestion": "补译"}}, nil)
	if calls != 3 {
		t.Errorf("calls = %d", calls)
	}
	if !strings.Contains(outcome.FallbackReason, "evidence_round_limit") {
		t.Errorf("fallback = %v", outcome.FallbackReason)
	}
}

func TestUnknownEvidenceRefFallsBack(t *testing.T) {
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"action":"final","decisions":[{"candidate_id":"ch0-base0-candidate0","verdict":"confirmed","detail":"候选","suggestion":"补译","reason":"","consistency":{},"evidence_refs":["invented:ref"]}],"new_issues":[],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	outcome := NewReviewAgentLoop(client, reviewTestConfig(t), agentLoopEvidence(), debug).ReviewChunk(
		0, 0, []string{"Ann arrived."}, []string{"安到了。"},
		[]map[string]any{{"index": 0, "type": "missing", "detail": "候选", "suggestion": "补译"}}, nil)
	if !strings.Contains(outcome.FallbackReason, "unknown_evidence_ref") {
		t.Errorf("fallback = %v", outcome.FallbackReason)
	}
}

// ---- TestReviewConflictArbiter ----

func TestConflictingCrossChunkClaimsAreArbitrated(t *testing.T) {
	evidence := NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, [][2]string{{"Ann.", "安。"}, {"Ann.", "安妮。"}})},
		[]*glossary.Term{{Source: "Ann", Target: "安", Type: "人物", Status: "ok"}},
		map[string]any{}, nil)
	issues := NormalizeReviewIssues([]map[string]any{
		{"chapter": 0, "index": 0, "_chunk_id": "chunk-a", "type": "terminology",
			"detail": "译名问题", "suggestion": "用安",
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": "安"}},
		{"chapter": 0, "index": 1, "_chunk_id": "chunk-b", "type": "terminology",
			"detail": "译名问题", "suggestion": "用安妮",
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": "安妮"}},
	}, evidence)
	conflicts := BuildConflictGroups(issues)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d", len(conflicts))
	}
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"action":"final","conflict_id":"review-conflict-0001","status":"suggested","recommended_value":"安","reason":"沿用首次出现和术语表。","evidence_refs":[],"complete":true}`, nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflicts[0])
	if result["status"] != "suggested" || result["recommended_value"] != "安" {
		t.Errorf("result = %v", result)
	}
	supported := result["supported_issue_ids"].([]string)
	rejected := result["rejected_issue_ids"].([]string)
	if len(supported) != 1 || supported[0] != issues[0]["issue_id"] {
		t.Errorf("supported = %v", supported)
	}
	if len(rejected) != 1 || rejected[0] != issues[1]["issue_id"] {
		t.Errorf("rejected = %v", rejected)
	}
}

func TestAllIssuesWithTheWinningValueAreKept(t *testing.T) {
	evidence := NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, [][2]string{
			{"Ann A.", "安。"}, {"Ann B.", "安妮。"}, {"Ann C.", "安。"},
		})},
		[]*glossary.Term{{Source: "Ann", Target: "安", Type: "人物", Status: "ok"}},
		map[string]any{}, nil)
	var raw []map[string]any
	for index, proposed := range []string{"安", "安妮", "安"} {
		raw = append(raw, map[string]any{
			"chapter": 0, "index": index, "_chunk_id": fmt.Sprintf("chunk-%d", index),
			"type": "terminology", "detail": "译名问题", "suggestion": "统一为" + proposed,
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": proposed},
		})
	}
	issues := NormalizeReviewIssues(raw, evidence)
	conflict := BuildConflictGroups(issues)[0]
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		cid, _ := conflict["conflict_id"].(string)
		return fmt.Sprintf(`{"action":"final","conflict_id":%q,"status":"suggested","recommended_value":"安","reason":"沿用多数且与术语表一致的译名。","evidence_refs":[],"complete":true}`, cid), nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflict)
	supported := result["supported_issue_ids"].([]string)
	if len(supported) != 2 || supported[0] != issues[0]["issue_id"] || supported[1] != issues[2]["issue_id"] {
		t.Errorf("supported = %v", supported)
	}
	rejected := result["rejected_issue_ids"].([]string)
	if len(rejected) != 1 || rejected[0] != issues[1]["issue_id"] {
		t.Errorf("rejected = %v", rejected)
	}
}

func TestRecommendedValueUsesTheExactExistingProposalSpelling(t *testing.T) {
	evidence := NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, [][2]string{{"Agency A.", "NASA。"}, {"Agency B.", "ESA。"}})},
		nil, map[string]any{}, nil)
	issues := NormalizeReviewIssues([]map[string]any{
		{"chapter": 0, "index": 0, "_chunk_id": "chunk-0", "type": "terminology",
			"detail": "机构简称不统一", "suggestion": "NASA",
			"consistency": map[string]any{"kind": "fixed", "subject_source": "agency", "proposed_value": "NASA"}},
		{"chapter": 0, "index": 1, "_chunk_id": "chunk-1", "type": "terminology",
			"detail": "机构简称不统一", "suggestion": "ESA",
			"consistency": map[string]any{"kind": "fixed", "subject_source": "agency", "proposed_value": "ESA"}},
	}, evidence)
	conflict := BuildConflictGroups(issues)[0]
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		cid, _ := conflict["conflict_id"].(string)
		return fmt.Sprintf(`{"action":"final","conflict_id":%q,"status":"suggested","recommended_value":"nasa","reason":"选择已有的 NASA 写法。","evidence_refs":[],"complete":true}`, cid), nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflict)
	if result["recommended_value"] != "NASA" {
		t.Errorf("recommended = %v", result["recommended_value"])
	}
}

func TestArbiterMustRequeryInheritedEvidenceBeforeCitingIt(t *testing.T) {
	evidence := NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, [][2]string{{"Ann.", "安。"}, {"Ann.", "安妮。"}})},
		[]*glossary.Term{{Source: "Ann", Target: "安", Type: "人物", Status: "ok"}},
		map[string]any{}, nil)
	glossaryResult := evidence.GlossaryTerm(map[string]any{"term": "Ann"})
	var inheritedRef string
	for ref := range evidence.EvidenceRefs(glossaryResult) {
		inheritedRef = ref
	}
	issues := NormalizeReviewIssues([]map[string]any{
		{"chapter": 0, "index": 0, "_chunk_id": "chunk-0", "type": "terminology",
			"detail": "译名问题", "suggestion": "统一为安", "evidence_refs": []string{inheritedRef},
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": "安"}},
		{"chapter": 0, "index": 1, "_chunk_id": "chunk-1", "type": "terminology",
			"detail": "译名问题", "suggestion": "统一为安妮", "evidence_refs": []string{inheritedRef},
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": "安妮"}},
	}, evidence)
	conflict := BuildConflictGroups(issues)[0]
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		cid, _ := conflict["conflict_id"].(string)
		return fmt.Sprintf(`{"action":"final","conflict_id":%q,"status":"suggested","recommended_value":"安","reason":"引用了未重新取得的术语证据。","evidence_refs":[%q],"complete":true}`, cid, inheritedRef), nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflict)
	if result["status"] != "unresolved" {
		t.Errorf("status = %v", result["status"])
	}
	if reason, _ := result["reason"].(string); !strings.Contains(reason, "unknown_evidence_ref") {
		t.Errorf("reason = %v", result["reason"])
	}
}

func TestArbiterPromptSamplesEachProposalInsteadOfEmbeddingAllIssues(t *testing.T) {
	var texts [][2]string
	for index := 0; index < 12; index++ {
		texts = append(texts, [2]string{fmt.Sprintf("SOURCE-%03d", index), fmt.Sprintf("TARGET-%03d", index)})
	}
	evidence := NewBookEvidenceIndex([]*ingest.Chapter{testChapter(0, texts)}, nil, map[string]any{}, nil)
	var raw []map[string]any
	for index, proposed := range append(make([]string, 0, 12), append(repeatStr("安", 10), repeatStr("安妮", 2)...)...) {
		raw = append(raw, map[string]any{
			"chapter": 0, "index": index, "_chunk_id": fmt.Sprintf("chunk-%d", index),
			"type": "terminology", "detail": "译名问题", "suggestion": "统一为" + proposed,
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": proposed},
		})
	}
	issues := NormalizeReviewIssues(raw, evidence)
	conflict := BuildConflictGroups(issues)[0]
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		prompt := m[len(m)-1].Content
		if !strings.Contains(prompt, `"issue_count": 10`) {
			t.Errorf("缺 issue_count 10")
		}
		for _, sampled := range []string{"SOURCE-000", "SOURCE-004", "SOURCE-009"} {
			if !strings.Contains(prompt, sampled) {
				t.Errorf("缺采样 %s", sampled)
			}
		}
		for _, omitted := range []string{"SOURCE-001", "SOURCE-002", "SOURCE-003", "SOURCE-005"} {
			if strings.Contains(prompt, omitted) {
				t.Errorf("不应包含 %s", omitted)
			}
		}
		cid, _ := conflict["conflict_id"].(string)
		return fmt.Sprintf(`{"action":"final","conflict_id":%q,"status":"suggested","recommended_value":"安","reason":"抽样证据一致。","evidence_refs":[],"complete":true}`, cid), nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflict)
	if result["status"] != "suggested" {
		t.Errorf("status = %v", result["status"])
	}
	if len(result["supported_issue_ids"].([]string)) != 10 {
		t.Errorf("supported = %v", result["supported_issue_ids"])
	}
}

func repeatStr(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestOversizedArbitrationSampleFallsBackWithoutModelCall(t *testing.T) {
	longText := strings.Repeat("很长的证据", 500)
	var texts [][2]string
	for index := 0; index < 32; index++ {
		texts = append(texts, [2]string{fmt.Sprintf("%d-%s", index, longText), longText})
	}
	evidence := NewBookEvidenceIndex([]*ingest.Chapter{testChapter(0, texts)}, nil, map[string]any{}, nil)
	var raw []map[string]any
	for index := 0; index < 32; index++ {
		raw = append(raw, map[string]any{
			"chapter": 0, "index": index, "_chunk_id": fmt.Sprintf("chunk-%d", index),
			"type": "terminology", "detail": longText, "suggestion": longText,
			"consistency": map[string]any{"kind": "fixed", "subject_source": "口号", "proposed_value": fmt.Sprintf("版本-%d", index)},
		})
	}
	issues := NormalizeReviewIssues(raw, evidence)
	conflict := BuildConflictGroups(issues)[0]
	called := false
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		called = true
		return "", nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflict)
	if called {
		t.Errorf("不应调用模型")
	}
	if result["status"] != "unresolved" {
		t.Errorf("status = %v", result["status"])
	}
	if reason, _ := result["reason"].(string); !strings.Contains(reason, "大小上限") {
		t.Errorf("reason = %v", result["reason"])
	}
}

func TestArbitrationIsAppliedToTheFinalIssueView(t *testing.T) {
	issues := []map[string]any{
		{"issue_id": "review-00001", "detail": "保留", "suggestion": "统一为安"},
		{"issue_id": "review-00002", "detail": "改写", "suggestion": "统一为安妮"},
	}
	final, rejected := ApplyReviewArbitrations(issues, []map[string]any{{
		"conflict_id": "review-conflict-0001", "status": "suggested",
		"recommended_value": "安", "reason": "采用首次译名。",
		"supported_issue_ids": []string{"review-00001"}, "rejected_issue_ids": []string{"review-00002"},
	}})
	if len(final) != 2 || len(rejected) != 1 {
		t.Fatalf("final/rejected = %d/%d", len(final), len(rejected))
	}
	if final[0]["issue_id"] != "review-00001" || final[1]["issue_id"] != "review-00002" {
		t.Errorf("顺序")
	}
	if final[0]["arbitration"].(map[string]any)["recommended_value"] != "安" {
		t.Errorf("arbitration")
	}
	if final[1]["detail"] != "该处相关表达需按终局仲裁统一为「安」。" {
		t.Errorf("detail = %v", final[1]["detail"])
	}
	if final[1]["pre_arbitration_detail"] != "改写" {
		t.Errorf("pre detail = %v", final[1]["pre_arbitration_detail"])
	}
	if final[1]["suggestion"] != "按终局仲裁将相关表达统一为「安」。" {
		t.Errorf("suggestion = %v", final[1]["suggestion"])
	}
	if final[1]["pre_arbitration_suggestion"] != "统一为安妮" {
		t.Errorf("pre suggestion = %v", final[1]["pre_arbitration_suggestion"])
	}
}

func TestUnresolvedArbitrationKeepsEveryIssue(t *testing.T) {
	issues := []map[string]any{
		{"issue_id": "review-00001", "detail": "甲"},
		{"issue_id": "review-00002", "detail": "乙"},
	}
	final, rejected := ApplyReviewArbitrations(issues, []map[string]any{{
		"conflict_id": "review-conflict-0001", "status": "unresolved",
		"recommended_value": "", "reason": "证据不足。",
		"issue_ids":          []string{"review-00001", "review-00002"},
		"supported_issue_ids": []string{"review-00001", "review-00002"}, "rejected_issue_ids": []string{},
	}})
	if len(final) != 2 || len(rejected) != 0 {
		t.Fatalf("final/rejected = %d/%d", len(final), len(rejected))
	}
	for _, issue := range final {
		if issue["arbitration"].(map[string]any)["status"] != "unresolved" {
			t.Errorf("status")
		}
	}
}

func TestUnproposedSuggestedValueFallsBackToUnresolved(t *testing.T) {
	evidence := NewBookEvidenceIndex(
		[]*ingest.Chapter{testChapter(0, [][2]string{{"Ann.", "安。"}, {"Ann.", "安妮。"}})},
		[]*glossary.Term{{Source: "Ann", Target: "安", Type: "人物", Status: "ok"}},
		map[string]any{}, nil)
	issues := NormalizeReviewIssues([]map[string]any{
		{"chapter": 0, "index": 0, "_chunk_id": "chunk-a", "type": "terminology",
			"detail": "甲", "suggestion": "用安",
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": "安"}},
		{"chapter": 0, "index": 1, "_chunk_id": "chunk-b", "type": "terminology",
			"detail": "乙", "suggestion": "用安妮",
			"consistency": map[string]any{"kind": "term", "subject_source": "Ann", "proposed_value": "安妮"}},
	}, evidence)
	conflict := BuildConflictGroups(issues)[0]
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		cid, _ := conflict["conflict_id"].(string)
		return fmt.Sprintf(`{"action":"final","conflict_id":%q,"status":"suggested","recommended_value":"阿恩","reason":"自创新译名。","evidence_refs":[],"complete":true}`, cid), nil
	})
	dir := t.TempDir()
	debug, _ := NewReviewRunStore(dir, nil)
	result := NewReviewConflictArbiter(client, reviewTestConfig(t), evidence, debug).Arbitrate(conflict)
	if result["status"] != "unresolved" {
		t.Errorf("status = %v", result["status"])
	}
	if reason, _ := result["reason"].(string); !strings.Contains(reason, "recommended_value_not_proposed") {
		t.Errorf("reason = %v", result["reason"])
	}
}

// ---- 端到端 Review 会话冒烟（迁移自 test_review_polish.py 的会话语义）----

func TestRunReviewSessionCleanConfirmed(t *testing.T) {
	// 准备：已完成的书（两章各 1-2 段），fake 全 clean 审校
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state", "book")
	if err := os.MkdirAll(filepath.Join(stateDir, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	chapters := []*ingest.Chapter{
		testChapter(0, [][2]string{{"Ann arrived.", "安到了。"}}),
		testChapter(1, [][2]string{{"End.", "结束。"}}),
	}
	manifest := map[string]any{
		"title": "Book", "fmt": "text", "source_lang": "en", "target_lang": "zh",
		"initialized": true, "meta": map[string]any{},
		"chapters": []any{
			map[string]any{"index": 0, "title": "Chapter 0", "status": "done"},
			map[string]any{"index": 1, "title": "Chapter 1", "status": "done"},
		},
	}
	writeJSONForTest(t, filepath.Join(stateDir, "manifest.json"), manifest)
	for _, ch := range chapters {
		writeJSONForTest(t, filepath.Join(stateDir, "chapters", fmt.Sprintf("ch%d.json", ch.Index)), ch)
	}
	cfg := reviewTestConfig(t)
	cfg.Pipeline.ReviewFixLoop = true
	cfg.Pipeline.ReviewCleanConfirmations = 2
	client := llm.NewFakeClient(func(m []llm.Message, tier string, jsonMode bool) (string, error) {
		system := m[0].Content
		if strings.Contains(system, "译文审校") {
			n := len(numberedLines(m[len(m)-1].Content))
			return fmt.Sprintf(`{"issues":[],"reviewed_segments":%d,"complete":true}`, n), nil
		}
		return "{}", nil
	})
	analyzer := agentsNewAnalyzer(client, cfg)
	deps := SessionDeps{
		Store: &storeStub{manifest: manifest, chapters: map[int]*ingest.Chapter{0: chapters[0], 1: chapters[1]}},
		RunDir: stateDir,
		Client: client, Config: cfg, Analyzer: analyzer,
		FlushUsage: func(scope string) (map[string]any, error) { return nil, nil },
		LogEvent:   func(event string, data map[string]any) error { return nil },
	}
	outcome, err := RunReviewSession(nil, deps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Result["termination"] != "clean_confirmed" {
		t.Errorf("termination = %v", outcome.Result["termination"])
	}
	if len(outcome.Issues) != 0 {
		t.Errorf("issues = %v", outcome.Issues)
	}
	// result.json 存在且 status completed
	resultRaw, err := os.ReadFile(filepath.Join(outcome.RunDir, "result.json"))
	if err != nil {
		t.Fatalf("result.json 缺失：%v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(resultRaw, &result)
	if result["status"] != "completed" {
		t.Errorf("status = %v", result["status"])
	}
}

func TestRunReviewSessionRejectsIncompleteBook(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state", "book")
	if err := os.MkdirAll(filepath.Join(stateDir, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"initialized": true,
		"chapters": []any{
			map[string]any{"index": 0, "title": "Chapter 0", "status": "pending"},
		},
	}
	writeJSONForTest(t, filepath.Join(stateDir, "manifest.json"), manifest)
	cfg := reviewTestConfig(t)
	deps := SessionDeps{
		Store:  &storeStub{manifest: manifest, chapters: map[int]*ingest.Chapter{}},
		RunDir: stateDir,
		Client: llm.NewFakeClient(nil), Config: cfg,
		FlushUsage: func(scope string) (map[string]any, error) { return nil, nil },
		LogEvent:   func(event string, data map[string]any) error { return nil },
	}
	_, err := RunReviewSession(nil, deps, nil)
	if err == nil || !strings.Contains(err.Error(), "仍待翻译章节") {
		t.Errorf("err = %v", err)
	}
}

// storeStub RunStore 最小实现。
type storeStub struct {
	manifest map[string]any
	chapters map[int]*ingest.Chapter
}

func (s *storeStub) PendingChapters() ([]int, error) {
	var out []int
	for _, item := range s.manifest["chapters"].([]any) {
		cm := item.(map[string]any)
		if cm["status"] != "done" {
			out = append(out, int(cm["index"].(int)))
		}
	}
	return out, nil
}

func (s *storeStub) LoadManifest() (map[string]any, error) { return s.manifest, nil }

func (s *storeStub) LoadChapter(ci int) (*ingest.Chapter, error) {
	return s.chapters[ci], nil
}

func (s *storeStub) LoadAnalysis() (map[string]any, error) { return map[string]any{}, nil }

func writeJSONForTest(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func numberedLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "[") {
			out = append(out, line)
		}
	}
	return out
}
