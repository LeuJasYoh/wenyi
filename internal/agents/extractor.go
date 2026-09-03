package agents

// extractor.go 术语抽取 Agent（主规格 §8.5 + 分册 04 + 分册 05 prompt）。

import (
	"encoding/json"
	"fmt"
	"sort"

	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// TranslatedSegmentEvidence 已译段落证据（history）。
type TranslatedSegmentEvidence struct {
	Chapter int
	Segment int
	Source  string
	Target  string
}

// GlossaryExtractor 术语抽取 Agent（tier=fast）。
type GlossaryExtractor struct {
	Agent
	recurringCache map[string][]*glossary.Term
}

func NewExtractor(client llm.LLMClient, cfg *config.Config) *GlossaryExtractor {
	e := &GlossaryExtractor{recurringCache: map[string][]*glossary.Term{}}
	e.Agent = NewAgent(client, cfg, "GlossaryExtractor")
	return e
}

func textValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case bool:
		return ""
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return ""
	}
}

// Extract 抽取并清洗字段（gender=="未知"→""；aliases 过滤非空字符串；type 缺省"术语"）。
func (e *GlossaryExtractor) Extract(sourceText, targetText string, existing []*glossary.Term) ([]*glossary.Term, error) {
	lites := make([]GlossaryTermLite, len(existing))
	for i, t := range existing {
		lites[i] = GlossaryTermLite{
			Source: t.Source, Target: t.Target, Reading: t.Reading,
			Type: t.Type, Gender: t.Gender, Aliases: t.Aliases,
		}
	}
	system := Render("glossary_extractor_system", RenderOptions{Src: e.Src, Tgt: e.Tgt})
	user := Render("glossary_extractor_user", RenderOptions{
		Src: e.Src, Tgt: e.Tgt,
		Vars: map[string]string{
			"glossary": RenderGlossary(lites),
			"source":   sourceText,
			"target":   targetText,
		},
	})
	data, err := e.AskJSONDefault(system, user, "fast", "terms", nil, []*jsonx.OMap{})
	if err != nil {
		return nil, err
	}
	var out []*glossary.Term
	for _, item := range DictItems(data) {
		src, _ := item.Get("source")
		tgt, _ := item.Get("target")
		source := textValue(src)
		target := textValue(tgt)
		if source == "" || target == "" {
			continue
		}
		typVal, _ := item.Get("type")
		typ := textValue(typVal)
		if typ == "" {
			typ = glossary.TypeTerm
		}
		gender := textValue(item2any(item, "gender"))
		if gender == "未知" {
			gender = ""
		}
		var aliases []string
		if av, ok := item.Get("aliases"); ok {
			if arr, ok := av.([]any); ok {
				for _, a := range arr {
					if s, ok := a.(string); ok && s != "" {
						aliases = append(aliases, s)
					}
				}
			}
		}
		note := textValue(item2any(item, "note"))
		out = append(out, &glossary.Term{
			Source: source, Target: target, Type: typ, Gender: gender,
			Aliases: aliases, Note: note, Status: "ok",
		})
	}
	return out, nil
}

func item2any(om *jsonx.OMap, key string) any {
	v, _ := om.Get(key)
	return v
}

// recurringExisting 全书出现 ≥2 次的既有术语（按 (source,type,aliases) 缓存）。
func (e *GlossaryExtractor) recurringExisting(store *glossary.Store, sourceCorpus string) ([]*glossary.Term, error) {
	cacheKey := func(t *glossary.Term) string {
		b, _ := json.Marshal([]any{t.Source, t.Type, t.Aliases})
		return string(b)
	}
	all, err := store.AllTerms()
	if err != nil {
		return nil, err
	}
	var out []*glossary.Term
	for _, t := range all {
		key := cacheKey(t)
		if cached, ok := e.recurringCache[key]; ok {
			out = append(out, cached...)
			continue
		}
		recurring := glossary.RecurringTerms([]*glossary.Term{t}, sourceCorpus, 2)
		e.recurringCache[key] = recurring
		out = append(out, recurring...)
	}
	return out, nil
}

// firstOccurrences 按 (chapter,segment) 排序回查每个新术语首次出现的已译段落。
func firstOccurrences(terms []*glossary.Term, history []TranslatedSegmentEvidence) map[string]TranslatedSegmentEvidence {
	sorted := make([]TranslatedSegmentEvidence, len(history))
	copy(sorted, history)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Chapter != sorted[j].Chapter {
			return sorted[i].Chapter < sorted[j].Chapter
		}
		return sorted[i].Segment < sorted[j].Segment
	})
	out := map[string]TranslatedSegmentEvidence{}
	for _, t := range terms {
		for _, ev := range sorted {
			if glossary.SourceMatchesText(t.Source, ev.Source) {
				out[t.Source] = ev
				break
			}
		}
	}
	return out
}

// alignWithFirstOccurrences 首次译文优先校准（glossary_history_*，tier=fast）；
// 无法可靠对应 → target 空 → 不入库（history_unresolved）。
func (e *GlossaryExtractor) alignWithFirstOccurrences(
	terms []*glossary.Term, firstOccs map[string]TranslatedSegmentEvidence,
) (aligned []*glossary.Term, unresolvedCount int, err error) {
	type candidate struct {
		Source         string `json:"source"`
		ProposedTarget string `json:"proposed_target"`
		FirstSource    string `json:"first_source"`
		FirstTarget    string `json:"first_target"`
	}
	candidates := make([]candidate, 0, len(terms))
	for _, t := range terms {
		ev := firstOccs[t.Source]
		candidates = append(candidates, candidate{
			Source: t.Source, ProposedTarget: t.Target,
			FirstSource: ev.Source, FirstTarget: ev.Target,
		})
	}
	payload, _ := json.MarshalIndent(candidates, "", "  ")
	system := Render("glossary_history_system", RenderOptions{Src: e.Src, Tgt: e.Tgt})
	user := Render("glossary_history_user", RenderOptions{
		Src: e.Src, Tgt: e.Tgt,
		Vars: map[string]string{"candidates_json": string(payload)},
	})
	data, err := e.AskJSONDefault(system, user, "fast", "terms", nil, []*jsonx.OMap{})
	if err != nil {
		return nil, 0, err
	}
	calibrated := map[string]string{}
	for _, item := range DictItems(data) {
		src, _ := item.Get("source")
		tgt, _ := item.Get("target")
		calibrated[textValue(src)] = textValue(tgt)
	}
	for _, t := range terms {
		if target, ok := calibrated[t.Source]; ok && target != "" {
			aligned = append(aligned, &glossary.Term{
				Source: t.Source, Target: target, Reading: t.Reading, Type: t.Type,
				Gender: t.Gender, Aliases: t.Aliases, Note: t.Note, Status: "ok",
			})
		} else {
			unresolvedCount++
		}
	}
	return aligned, unresolvedCount, nil
}

// ExtractAndStore 抽取 + 回查 + 校准 + 入库。返回 summary（6 键）。
func (e *GlossaryExtractor) ExtractAndStore(store *glossary.Store, sourceText, targetText string, chapter int,
	history []TranslatedSegmentEvidence, before *[2]int, sourceCorpus string,
) (map[string]int, error) {
	summary := map[string]int{
		"inserted": 0, "conflict": 0, "unchanged": 0,
		"history_matched": 0, "history_aligned": 0, "history_unresolved": 0,
	}
	existing := []*glossary.Term{}
	var err error
	if sourceCorpus != "" {
		existing, err = e.recurringExisting(store, sourceCorpus)
	} else {
		existing, err = store.AllTerms()
	}
	if err != nil {
		return nil, err
	}
	terms, err := e.Extract(sourceText, targetText, existing)
	if err != nil {
		return nil, err
	}
	firstOccs := map[string]TranslatedSegmentEvidence{}
	if before != nil {
		firstOccs = firstOccurrences(terms, history)
		summary["history_matched"] = len(firstOccs)
		if len(firstOccs) > 0 {
			aligned, unresolved, err := e.alignWithFirstOccurrences(terms, firstOccs)
			if err != nil {
				return nil, err
			}
			summary["history_aligned"] = len(aligned)
			summary["history_unresolved"] = unresolved
			terms = aligned
		}
	}
	for _, t := range terms {
		fc := chapter
		if ev, ok := firstOccs[t.Source]; ok {
			fc = ev.Chapter
		}
		result, err := store.UpsertTerm(t, &fc)
		if err != nil {
			return nil, err
		}
		summary[result]++
	}
	return summary, nil
}
