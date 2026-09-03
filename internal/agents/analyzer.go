package agents

// analyzer.go 全局分析 Agent（主规格 §9.4 + 分册 05 §5）。

import (
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// textCoerce 等价 analyzer._text：str→strip；int/float（排除 bool）→字符串；其它→default。
func textCoerce(v any, def string) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case int64:
		return itoa(int(t))
	case float64:
		if t == float64(int64(t)) {
			return itoa(int(t))
		}
		return trimFloat(t)
	case int:
		return itoa(t)
	default:
		return def
	}
}

func itoa(i int) string {
	return strings.TrimSpace(sprintfD(i))
}

func sprintfD(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func trimFloat(f float64) string {
	s := ""
	b := []byte{}
	// 简易 float→string（够用于清洗）
	neg := f < 0
	if neg {
		f = -f
	}
	intPart := int64(f)
	frac := f - float64(intPart)
	b = append(b, []byte(sprintfD(int(intPart)))...)
	if frac > 0 {
		b = append(b, '.')
		for i := 0; i < 6 && frac > 1e-9; i++ {
			frac *= 10
			d := int(frac)
			b = append(b, byte('0'+d))
			frac -= float64(d)
		}
	}
	s = string(b)
	if neg {
		s = "-" + s
	}
	return s
}

// Analyzer 前期分析师（tier=strong，失败即抛）。
type Analyzer struct {
	Agent
}

func NewAnalyzer(client llm.LLMClient, cfg *config.Config) *Analyzer {
	a := &Analyzer{}
	a.Agent = NewAgent(client, cfg, "Analyzer")
	return a
}

// Analyze 分析样章并清洗 8 个字符串键 + characters/terms 列表。
func (a *Analyzer) Analyze(sampleText string) (*jsonx.OMap, error) {
	system := Render("analyzer_system", RenderOptions{Src: a.Src, Tgt: a.Tgt})
	user := Render("analyzer_user", RenderOptions{Src: a.Src, Tgt: a.Tgt,
		Vars: map[string]string{"sample": sampleText}})
	data, err := a.AskJSON(system, user, "strong", "", nil)
	if err != nil {
		return nil, err
	}
	om, ok := data.(*jsonx.OMap)
	if !ok {
		om = jsonx.NewOMap()
	}
	for _, key := range []string{"genre", "tone", "style_guide", "narration", "pacing", "register", "dialogue_style", "rhetoric"} {
		v, _ := om.Get(key)
		om.Set(key, textCoerce(v, ""))
	}
	for _, key := range []string{"characters", "terms"} {
		v, _ := om.Get(key)
		arr := DictItems(v)
		vals := make([]any, len(arr))
		for i, item := range arr {
			vals[i] = item
		}
		om.Set(key, vals)
	}
	return om, nil
}

// SeedGlossary 角色→人物 / 术语→模型类型，种入术语库；返回写入条数。
func (a *Analyzer) SeedGlossary(store *glossary.Store, analysis *jsonx.OMap) (int, error) {
	count := 0
	zero := 0
	if chars, ok := analysis.Get("characters"); ok {
		for _, ch := range DictItems(chars) {
			src, _ := ch.Get("source")
			tgt, _ := ch.Get("target")
			source := textCoerce(src, "")
			target := textCoerce(tgt, "")
			if source == "" || target == "" {
				continue
			}
			reading, _ := ch.Get("reading")
			gender, _ := ch.Get("gender")
			note, _ := ch.Get("note")
			if _, err := store.UpsertTerm(&glossary.Term{
				Source: source, Target: target,
				Reading: textCoerce(reading, ""), Type: glossary.TypePerson,
				Gender: textCoerce(gender, ""), Note: textCoerce(note, ""),
				Status: "ok",
			}, &zero); err != nil {
				return count, err
			}
			count++
		}
	}
	if terms, ok := analysis.Get("terms"); ok {
		for _, tm := range DictItems(terms) {
			src, _ := tm.Get("source")
			tgt, _ := tm.Get("target")
			source := textCoerce(src, "")
			target := textCoerce(tgt, "")
			if source == "" || target == "" {
				continue
			}
			reading, _ := tm.Get("reading")
			typ, _ := tm.Get("type")
			typeVal := textCoerce(typ, "")
			if typeVal == "" {
				typeVal = glossary.TypeTerm
			}
			note, _ := tm.Get("note")
			if _, err := store.UpsertTerm(&glossary.Term{
				Source: source, Target: target,
				Reading: textCoerce(reading, ""), Type: typeVal,
				Note: textCoerce(note, ""), Status: "ok",
			}, &zero); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// StyleBrief 浓缩为注入译者的风格/角色简报。
func (a *Analyzer) StyleBrief(analysis *jsonx.OMap) string {
	var lines []string
	get := func(key string) string {
		v, _ := analysis.Get(key)
		return textCoerce(v, "")
	}
	if v := get("genre"); v != "" {
		lines = append(lines, "体裁："+v)
	}
	if v := get("tone"); v != "" {
		lines = append(lines, "语气文体："+v)
	}
	if v := get("style_guide"); v != "" {
		lines = append(lines, "风格指南："+v)
	}
	for _, kv := range [][2]string{
		{"narration", "叙事"}, {"pacing", "句式节奏"}, {"register", "语域"},
		{"dialogue_style", "对话风格"}, {"rhetoric", "修辞"},
	} {
		if v := get(kv[0]); v != "" {
			lines = append(lines, kv[1]+"："+v)
		}
	}
	if chars, ok := analysis.Get("characters"); ok {
		items := DictItems(chars)
		if len(items) > 0 {
			lines = append(lines, "角色：")
			for _, ch := range items {
				src, _ := ch.Get("source")
				tgt, hasTgt := ch.Get("target")
				name := textCoerce(tgt, "")
				if !hasTgt || name == "" {
					name = textCoerce(src, "")
				}
				gender, _ := ch.Get("gender")
				note, _ := ch.Get("note")
				var parts []string
				if v := textCoerce(gender, ""); v != "" {
					parts = append(parts, v)
				}
				if v := textCoerce(note, ""); v != "" {
					parts = append(parts, v)
				}
				line := "  - " + name + "（" + textCoerce(src, "")
				if len(parts) > 0 {
					line += "，" + strings.Join(parts, "，")
				}
				line += "）"
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}
