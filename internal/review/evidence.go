package review

// evidence.go 对应 pipeline/review_evidence.py（主规格 §12.2/§12.3 + 分册 06 §4）。
// 只读证据索引：展平全书 text_segments，提供四个取证工具与 ref 白名单基础。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"wenyi/internal/glossary"
	"wenyi/internal/ingest"
	"wenyi/internal/jsonx"
)

// SegmentRef 一个可被证据引用的段落（冻结值对象）。
type SegmentRef struct {
	GlobalOrdinal  int
	Chapter        int
	TextIndex      int
	SegmentIndex   int
	Source         string
	Target         string // 影子覆盖或正式译文
	BaselineTarget string // 冻结基线
	TargetOrigin   string // formal | shadow_override
	ChapterTitle   string
}

// Ref 稳定证据 ID：ch{chapter}:text{text_index}:seg{segment_index}。
func (s *SegmentRef) Ref() string {
	return fmt.Sprintf("ch%d:text%d:seg%d", s.Chapter, s.TextIndex, s.SegmentIndex)
}

func truncateRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]), true
	}
	return s, false
}

// Compact 紧凑视图（source/target 截 4000；shadow 时追加 baseline_target）。
func (s *SegmentRef) Compact() map[string]any {
	src, srcTrunc := truncateRunes(s.Source, 4000)
	tgt, tgtTrunc := truncateRunes(s.Target, 4000)
	m := map[string]any{
		"ref":             s.Ref(),
		"chapter":         s.Chapter,
		"text_index":      s.TextIndex,
		"segment_index":   s.SegmentIndex,
		"chapter_title":   s.ChapterTitle,
		"source":          src,
		"target":          tgt,
		"target_origin":   s.TargetOrigin,
		"source_truncated":  srcTrunc,
		"target_truncated":  tgtTrunc,
	}
	if s.TargetOrigin == "shadow_override" {
		base, baseTrunc := truncateRunes(s.BaselineTarget, 4000)
		m["baseline_target"] = base
		m["baseline_target_truncated"] = baseTrunc
	}
	return m
}

// LocationKey (chapter, text_index)。
type LocationKey = [2]int

// BookEvidenceIndex 只读全书证据索引。
type BookEvidenceIndex struct {
	Segments      []*SegmentRef
	Terms         []*glossary.Term
	Analysis      map[string]any
	ChapterDigests map[int]string

	byLocation map[LocationKey]int
	exactSource map[string]*glossary.Term
	normSource  map[string][]*glossary.Term
	normAlias   map[string][]*glossary.Term

	occMu   sync.Mutex
	occCache map[string][]int
}

// NewBookEvidenceIndex 构造索引；target_overrides 以 (chapter.index, text_index) 提供影子译文。
func NewBookEvidenceIndex(chapters []*ingest.Chapter, terms []*glossary.Term,
	analysis map[string]any, targetOverrides map[LocationKey]string) *BookEvidenceIndex {
	if analysis == nil {
		analysis = map[string]any{}
	}
	idx := &BookEvidenceIndex{
		Terms:         terms,
		Analysis:      analysis,
		ChapterDigests: map[int]string{},
		byLocation:    map[LocationKey]int{},
		exactSource:   map[string]*glossary.Term{},
		normSource:    map[string][]*glossary.Term{},
		normAlias:     map[string][]*glossary.Term{},
		occCache:      map[string][]int{},
	}
	ordinal := 0
	for _, ch := range chapters {
		if digest, ok := ch.Meta["source_digest"].(string); ok {
			idx.ChapterDigests[ch.Index] = digest
		} else {
			idx.ChapterDigests[ch.Index] = ""
		}
		for ti, seg := range ch.TextSegments() {
			baseline := seg.TargetText()
			target := baseline
			origin := "formal"
			if override, ok := targetOverrides[LocationKey{ch.Index, ti}]; ok {
				target = override
				origin = "shadow_override"
			}
			ref := &SegmentRef{
				GlobalOrdinal:  ordinal,
				Chapter:        ch.Index,
				TextIndex:      ti,
				SegmentIndex:   seg.Index,
				Source:         seg.Source,
				Target:         target,
				BaselineTarget: baseline,
				TargetOrigin:   origin,
				ChapterTitle:   ch.Title,
			}
			idx.Segments = append(idx.Segments, ref)
			idx.byLocation[LocationKey{ch.Index, ti}] = ordinal
			ordinal++
		}
	}
	for _, t := range terms {
		idx.exactSource[t.Source] = t
		idx.normSource[NormalizedText(t.Source)] = append(idx.normSource[NormalizedText(t.Source)], t)
		for _, alias := range t.Aliases {
			key := NormalizedText(alias)
			idx.normAlias[key] = append(idx.normAlias[key], t)
		}
	}
	return idx
}

// SegmentRefLookup O(1) 按 (chapter, text_index) 查段。
func (e *BookEvidenceIndex) SegmentRefLookup(chapter, textIndex int) *SegmentRef {
	if ord, ok := e.byLocation[LocationKey{chapter, textIndex}]; ok {
		return e.Segments[ord]
	}
	return nil
}

// CanonicalTerm 精确 source → 规范化 source → 规范化 alias；
// 歧义返回 (nil, sorted sources)（主规格 §12.2）。
func (e *BookEvidenceIndex) CanonicalTerm(query string) (*glossary.Term, []string) {
	if t, ok := e.exactSource[query]; ok {
		return t, nil
	}
	norm := NormalizedText(query)
	if norm != "" {
		if hits := e.normSource[norm]; len(hits) == 1 {
			return hits[0], nil
		} else if len(hits) > 1 {
			return nil, termSources(hits)
		}
		if hits := e.normAlias[norm]; len(hits) == 1 {
			return hits[0], nil
		} else if len(hits) > 1 {
			return nil, termSources(hits)
		}
	}
	return nil, nil
}

func termSources(terms []*glossary.Term) []string {
	sources := make([]string, 0, len(terms))
	for _, t := range terms {
		sources = append(sources, t.Source)
	}
	sort.Strings(sources)
	return sources
}

// occurrences 懒扫描 + 缓存：术语按 MatchSources 匹配；字面短语按原样匹配。
func (e *BookEvidenceIndex) occurrences(query string) []int {
	term, ambiguous := e.CanonicalTerm(query)
	key := "literal:" + NormalizedText(query)
	if len(ambiguous) > 0 {
		return nil
	}
	if term != nil {
		key = "term:" + term.Source
	}
	e.occMu.Lock()
	if cached, ok := e.occCache[key]; ok {
		e.occMu.Unlock()
		return cached
	}
	e.occMu.Unlock()

	var keys []string
	if term != nil {
		keys = glossary.MatchSources(term)
	} else {
		keys = []string{query}
	}
	var hits []int
	for _, seg := range e.Segments {
		for _, k := range keys {
			if k == "" {
				continue
			}
			if glossary.SourceMatchesText(k, seg.Source) {
				hits = append(hits, seg.GlobalOrdinal)
				break
			}
		}
	}
	e.occMu.Lock()
	e.occCache[key] = hits
	e.occMu.Unlock()
	return hits
}

func argString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func argInt(args map[string]any, key string) (int, bool) {
	if v, ok := args[key]; ok {
		return StrictInt(v)
	}
	return 0, false
}

// TermOccurrences 工具 2：按命中次序取证术语出现位置（selectors ≤8，1 基）。
func (e *BookEvidenceIndex) TermOccurrences(arguments map[string]any) map[string]any {
	query, ok := argString(arguments, "term")
	if !ok || query == "" || len([]rune(query)) > 128 {
		return map[string]any{"ok": false, "error": "invalid_arguments"}
	}
	term, ambiguous := e.CanonicalTerm(query)
	if len(ambiguous) > 0 {
		return map[string]any{"ok": false, "error": "ambiguous_term", "sources": ambiguous}
	}
	hits := e.occurrences(query)
	total := len(hits)

	radius := 1
	if v, ok := arguments["context_radius"]; ok {
		if r, ok := StrictInt(v); ok && r >= 0 && r <= 2 {
			radius = r
		} else {
			return map[string]any{"ok": false, "error": "invalid_arguments"}
		}
	}

	selValues := []any{"first", "middle", "last"}
	if v, ok := arguments["selectors"]; ok {
		items, isList := sliceOf(v)
		if !isList || len(items) == 0 || len(items) > 8 {
			return map[string]any{"ok": false, "error": "invalid_arguments"}
		}
		selValues = items
	}

	var selected []map[string]any
	var invalidSelectors []any
	seen := map[int]bool{}
	for _, sel := range selValues {
		position := -1
		switch t := sel.(type) {
		case string:
			switch t {
			case "first":
				position = 1
			case "middle":
				position = (total - 1) / 2 + 1
			case "last":
				position = total
			default:
				if n, err := strconv.Atoi(trimSpaces(t)); err == nil {
					position = n
				}
			}
		case int64:
			position = int(t)
		case int:
			position = t
		}
		if position < 1 || position > total {
			invalidSelectors = append(invalidSelectors, sel)
			continue
		}
		if seen[position] {
			continue
		}
		seen[position] = true
		seg := e.Segments[hits[position-1]]
		item := seg.Compact()
		item["ordinal"] = position
		item["context"] = e.contextCompacts(seg.GlobalOrdinal, radius)
		selected = append(selected, item)
	}
	if invalidSelectors == nil {
		invalidSelectors = []any{}
	}
	result := map[string]any{
		"ok":                true,
		"total_matches":     total,
		"selected":          selected,
		"invalid_selectors": invalidSelectors,
	}
	if term != nil {
		result["canonical_term"] = term.Source
		result["glossary_term"] = glossaryTermDict(term)
	} else {
		result["canonical_term"] = nil
	}
	return result
}

func trimSpaces(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			out = append(out, r)
		}
	}
	return string(out)
}

func (e *BookEvidenceIndex) contextCompacts(center, radius int) []map[string]any {
	out := []map[string]any{}
	for ord := center - radius; ord <= center+radius; ord++ {
		if ord < 0 || ord >= len(e.Segments) || ord == center {
			continue
		}
		out = append(out, e.Segments[ord].Compact())
	}
	return out
}

// glossaryTermDict 术语条目（ref = glossary:<sha256(source)[:16]>，各字段截断）。
func glossaryTermDict(t *glossary.Term) map[string]any {
	sum := sha256.Sum256([]byte(t.Source))
	var firstChapter any
	if t.FirstChapter != nil {
		firstChapter = *t.FirstChapter
	}
	src, _ := truncateRunes(t.Source, 256)
	tgt, _ := truncateRunes(t.Target, 256)
	reading, _ := truncateRunes(t.Reading, 256)
	typ, _ := truncateRunes(t.Type, 64)
	gender, _ := truncateRunes(t.Gender, 64)
	note, _ := truncateRunes(t.Note, 2000)
	aliases := append([]string(nil), t.Aliases...)
	if len(aliases) > 16 {
		aliases = aliases[:16]
	}
	return map[string]any{
		"ref":           "glossary:" + hex.EncodeToString(sum[:])[:16],
		"source":        src,
		"target":        tgt,
		"reading":       reading,
		"type":          typ,
		"gender":        gender,
		"aliases":       aliases,
		"first_chapter": firstChapter,
		"note":          note,
	}
}

// GlossaryTerm 工具 1：读取单个术语库条目。
func (e *BookEvidenceIndex) GlossaryTerm(arguments map[string]any) map[string]any {
	query, ok := argString(arguments, "term")
	if !ok || query == "" {
		return map[string]any{"ok": false, "error": "invalid_arguments"}
	}
	term, ambiguous := e.CanonicalTerm(query)
	if len(ambiguous) > 0 {
		return map[string]any{"ok": false, "error": "ambiguous_term", "sources": ambiguous}
	}
	if term == nil {
		return map[string]any{"ok": false, "error": "term_not_found"}
	}
	return map[string]any{"ok": true, "term": glossaryTermDict(term)}
}

// SegmentContext 工具 3：跨章上下文（before/after ≤6 缺省 2）。
func (e *BookEvidenceIndex) SegmentContext(arguments map[string]any) map[string]any {
	chapter, ok1 := argInt(arguments, "chapter")
	index, ok2 := argInt(arguments, "index")
	if !ok1 || !ok2 {
		return map[string]any{"ok": false, "error": "invalid_arguments"}
	}
	clamp := func(key string, def int) (int, bool) {
		if v, ok := arguments[key]; ok {
			n, valid := StrictInt(v)
			if !valid || n < 0 || n > 6 {
				return 0, false
			}
			return n, true
		}
		return def, true
	}
	before, ok3 := clamp("before", 2)
	after, ok4 := clamp("after", 2)
	if !ok3 || !ok4 {
		return map[string]any{"ok": false, "error": "invalid_arguments"}
	}
	center := e.SegmentRefLookup(chapter, index)
	if center == nil {
		return map[string]any{"ok": false, "error": "segment_not_found"}
	}
	segments := []map[string]any{}
	for ord := center.GlobalOrdinal - before; ord <= center.GlobalOrdinal+after; ord++ {
		if ord < 0 || ord >= len(e.Segments) {
			continue
		}
		segments = append(segments, e.Segments[ord].Compact())
	}
	return map[string]any{"ok": true, "center": center.Compact(), "segments": segments}
}

// BookContext 工具 4：style_guide/book_synopsis/chapter_digest（截 6000）。
func (e *BookEvidenceIndex) BookContext(arguments map[string]any) map[string]any {
	section, ok := argString(arguments, "section")
	if !ok {
		return map[string]any{"ok": false, "error": "invalid_arguments"}
	}
	var value string
	ref := "book:" + section
	switch section {
	case "style_guide":
		value, _ = e.Analysis["style_guide"].(string)
	case "book_synopsis":
		value, _ = e.Analysis["book_synopsis"].(string)
	case "chapter_digest":
		chapter, ok := argInt(arguments, "chapter")
		if !ok {
			return map[string]any{"ok": false, "error": "invalid_arguments"}
		}
		digest, exists := e.ChapterDigests[chapter]
		if !exists {
			return map[string]any{"ok": false, "error": "chapter_not_found"}
		}
		value = digest
		ref = fmt.Sprintf("book:chapter_digest:ch%d", chapter)
	default:
		return map[string]any{"ok": false, "error": "unknown_section"}
	}
	truncated, wasTruncated := truncateRunes(value, 6000)
	result := map[string]any{"ok": true, "ref": ref, "value": truncated}
	if wasTruncated {
		result["truncated"] = true
	}
	return result
}

const evidenceResultMaxBytes = 64_000

// Execute 校验并执行一个取证请求；一切错误都是结构化 ok:false，绝不 panic。
func (e *BookEvidenceIndex) Execute(request map[string]any) map[string]any {
	requestID, _ := request["request_id"].(string)
	tool, _ := request["tool"].(string)
	if requestID == "" {
		return map[string]any{"ok": false, "error": "invalid_request_id"}
	}
	args, ok := OMapArgs(request["arguments"])
	if !ok {
		return map[string]any{"ok": false, "error": "invalid_arguments", "request_id": requestID, "tool": tool}
	}
	var result map[string]any
	switch tool {
	case "glossary_term":
		result = e.GlossaryTerm(args)
	case "term_occurrences":
		result = e.TermOccurrences(args)
	case "segment_context":
		result = e.SegmentContext(args)
	case "book_context":
		result = e.BookContext(args)
	default:
		return map[string]any{"ok": false, "error": "unknown_tool", "request_id": requestID}
	}
	out := map[string]any{"request_id": requestID, "tool": tool}
	for k, v := range result {
		out[k] = v
	}
	if encoded, err := jsonx.Marshal(out); err == nil && len(encoded) > evidenceResultMaxBytes {
		return map[string]any{
			"request_id": requestID, "tool": tool,
			"ok": false, "error": "evidence_result_too_large",
		}
	}
	return out
}

// EvidenceRefs 递归收集载荷中所有 "ref" 字符串键值。
func (e *BookEvidenceIndex) EvidenceRefs(value any) map[string]bool {
	refs := map[string]bool{}
	collectRefs(value, refs)
	return refs
}

func collectRefs(value any, refs map[string]bool) {
	switch t := value.(type) {
	case *jsonx.OMap:
		for _, k := range t.Keys() {
			v, _ := t.Get(k)
			if k == "ref" {
				if s, ok := v.(string); ok && s != "" {
					refs[s] = true
				}
			} else {
				collectRefs(v, refs)
			}
		}
	case map[string]any:
		for k, v := range t {
			if k == "ref" {
				if s, ok := v.(string); ok && s != "" {
					refs[s] = true
				}
			} else {
				collectRefs(v, refs)
			}
		}
	case []any:
		for _, item := range t {
			collectRefs(item, refs)
		}
	case []map[string]any:
		for _, item := range t {
			collectRefs(item, refs)
		}
	case []string:
		// ref 列表本身不是 dict 载荷（与 Python 一致：只收集 dict["ref"]）
	}
}
