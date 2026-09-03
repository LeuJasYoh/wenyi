package agents

// synopsis.go / polisher.go / consistency.go（主规格 §9.4 + 分册 05 §6/§8/§9）。

import (
	"strconv"
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// ---- Synopsizer：全书理解预扫（tier=fast）----

const reduceBudget = 12000

type Synopsizer struct {
	Agent
}

func NewSynopsizer(client llm.LLMClient, cfg *config.Config) *Synopsizer {
	s := &Synopsizer{}
	s.Agent = NewAgent(client, cfg, "Synopsizer")
	return s
}

// DigestChapter 章节梗概（截断 8000 字符；失败回退 ""）。
func (s *Synopsizer) DigestChapter(sourceText string) string {
	if strings.TrimSpace(sourceText) == "" {
		return ""
	}
	runes := []rune(sourceText)
	if len(runes) > 8000 {
		sourceText = string(runes[:8000])
	}
	system := Render("chapter_digest_system", RenderOptions{Src: s.Src, Tgt: s.Tgt})
	user := Render("chapter_digest_user", RenderOptions{Src: s.Src, Tgt: s.Tgt,
		Vars: map[string]string{"source": sourceText}})
	mt := 600
	return s.AskText(system, user, "fast", &mt, "")
}

// BookSynopsis 全书概览（map-reduce 归并，单组预算 12000）。
func (s *Synopsizer) BookSynopsis(digests []string, analysisBrief string) string {
	items := []string{}
	for _, d := range digests {
		if strings.TrimSpace(d) != "" {
			items = append(items, strings.TrimSpace(d))
		}
	}
	if len(items) == 0 {
		return ""
	}
	for {
		groups := groupItems(items, reduceBudget)
		if len(groups) == 1 {
			return s.synth(groups[0], analysisBrief)
		}
		next := []string{}
		for _, g := range groups {
			if out := s.synth(g, analysisBrief); strings.TrimSpace(out) != "" {
				next = append(next, out)
			}
		}
		if len(next) == 0 {
			return ""
		}
		items = next
	}
}

// groupItems 贪心打包（分隔符计入预算）。
func groupItems(items []string, budget int) [][]string {
	var groups [][]string
	var cur []string
	size := 0
	for _, it := range items {
		if len(cur) > 0 && size+len([]rune(it))+1 > budget {
			groups = append(groups, cur)
			cur = nil
			size = 0
		}
		cur = append(cur, it)
		size += len([]rune(it)) + 1
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

func (s *Synopsizer) synth(digests []string, analysisBrief string) string {
	numbered := Numbered(digests)
	brief := analysisBrief
	if brief == "" {
		brief = "（无）"
	}
	system := Render("book_synopsis_system", RenderOptions{Src: s.Src, Tgt: s.Tgt})
	user := Render("book_synopsis_user", RenderOptions{
		Src: s.Src, Tgt: s.Tgt,
		Vars: map[string]string{"analysis": brief, "digests": numbered},
	})
	mt := 1200
	return s.AskText(system, user, "fast", &mt, "")
}

// ---- Polisher：中文润色（tier=strong，失败保守返回原译）----

type Polisher struct {
	Agent
}

func NewPolisher(client llm.LLMClient, cfg *config.Config) *Polisher {
	p := &Polisher{}
	p.Agent = NewAgent(client, cfg, "Polisher")
	return p
}

// Polish 等长润色；失败/段数不符 → 原样返回输入副本。
func (p *Polisher) Polish(targets []string, glossaryTerms []*glossary.Term, style string) []string {
	if len(targets) == 0 {
		return []string{}
	}
	n := len(targets)
	lites := make([]GlossaryTermLite, len(glossaryTerms))
	for i, t := range glossaryTerms {
		lites[i] = GlossaryTermLite{
			Source: t.Source, Target: t.Target, Reading: t.Reading,
			Type: t.Type, Gender: t.Gender, Aliases: t.Aliases,
		}
	}
	st := style
	if st == "" {
		st = "（无）"
	}
	system := Render("polisher_system", RenderOptions{Src: p.Src, Tgt: p.Tgt, Vars: map[string]string{"n": strconv.Itoa(n)}})
	user := Render("polisher_user", RenderOptions{
		Src: p.Src, Tgt: p.Tgt,
		Vars: map[string]string{
			"glossary":       RenderGlossary(lites),
			"style":          st,
			"n":              strconv.Itoa(n),
			"numbered_target": Numbered(targets),
		},
	})
	items, err := p.AskJSONDefault(system, user, "strong", "polished", nil, nil)
	if err == nil {
		if arr, ok := items.([]any); ok && len(arr) == n {
			out := make([]string, n)
			allStr := true
			for i, item := range arr {
				if s, ok := item.(string); ok {
					out[i] = s
				} else {
					out[i] = coerceString(item)
				}
			}
			if allStr {
				return out
			}
		}
	}
	out := make([]string, n)
	copy(out, targets)
	return out
}

func coerceString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return ""
	}
}

// ---- ConsistencyChecker：跨章一致性 QA（tier=cheap）----

type ConsistencyChecker struct {
	Agent
}

func NewConsistencyChecker(client llm.LLMClient, cfg *config.Config) *ConsistencyChecker {
	c := &ConsistencyChecker{}
	c.Agent = NewAgent(client, cfg, "ConsistencyChecker")
	return c
}

// Check 一致性扫描。checkerDiggers 由调用方提供每章摘要文本。
func (c *ConsistencyChecker) CheckRaw(digests string, terms []*glossary.Term) []*jsonx.OMap {
	if strings.TrimSpace(digests) == "" {
		return []*jsonx.OMap{}
	}
	lites := make([]GlossaryTermLite, len(terms))
	for i, t := range terms {
		lites[i] = GlossaryTermLite{
			Source: t.Source, Target: t.Target, Reading: t.Reading,
			Type: t.Type, Gender: t.Gender, Aliases: t.Aliases,
		}
	}
	system := Render("consistency_system", RenderOptions{Src: c.Src, Tgt: c.Tgt})
	// consistency 的 user 是手工拼接，不经 render（分册 05 §9.6）
	user := "【专有名词对照表】\n" + RenderGlossary(lites) + "\n\n【各章译文摘要】\n" + digests + "\n\n请输出 JSON：{\"issues\":[...]}。"
	data, err := c.AskJSONDefault(system, user, "cheap", "issues", nil, []*jsonx.OMap{})
	if err != nil {
		return []*jsonx.OMap{}
	}
	return DictItems(data)
}

// ChapterDigests 生成各章摘要（首 3 段 + 尾 2 段，每章 ≤600 字符）。
// chapters: [(title, status, targets)] —— 由调用方适配 RunStore。
func BuildChapterDigests(chapters []ChapterDigestInput) string {
	var parts []string
	for _, ch := range chapters {
		if !ch.Done {
			continue
		}
		targets := ch.Targets
		head := targets
		if len(head) > 3 {
			head = head[:3]
		}
		var tail []string
		if len(targets) > 3 {
			tail = targets[len(targets)-2:]
		}
		var nonEmpty []string
		for _, t := range append(append([]string{}, head...), tail...) {
			if t != "" {
				nonEmpty = append(nonEmpty, t)
			}
		}
		snippet := []rune(strings.Join(nonEmpty, "……"))
		if len(snippet) > 600 {
			snippet = snippet[:600]
		}
		label := strings.TrimSpace(ch.Title)
		if label == "" {
			label = "章节 " + strconv.Itoa(ch.Index+1)
		}
		parts = append(parts, "["+label+"]\n"+string(snippet))
	}
	return strings.Join(parts, "\n\n")
}

type ChapterDigestInput struct {
	Index   int
	Title   string
	Done    bool
	Targets []string
}
