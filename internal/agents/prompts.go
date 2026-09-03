// Package agents 对应 trans_novel/agents（主规格 §9 + 分册 05）。
// 模板文本从规格分册逐字提取，存放于 promptdata/ 并 go:embed 嵌入。
package agents

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed promptdata/*.txt
var promptFS embed.FS

// TemplateText 返回模板原文（等价 Python prompts.TRANSLATOR_USER.template）。
func TemplateText(name string) string {
	b, err := promptFS.ReadFile("promptdata/" + name + ".txt")
	if err != nil {
		return ""
	}
	// 提取文件结尾恰有一个换行；Python Template 折叠首尾换行，模板文本本身无尾换行
	s := string(b)
	return strings.TrimSuffix(s, "\n")
}

// TemplateNames 返回全部模板键名。
func TemplateNames() []string {
	entries, err := promptFS.ReadDir("promptdata")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), ".txt")
		if n != "" && !strings.Contains(e.Name(), ".yaml") {
			names = append(names, n)
		}
	}
	return names
}

// ---- string.Template 的 safe_substitute 等价实现 ----
// Python 默认 idpattern = [_a-zA-Z][_a-zA-Z0-9]*（大小写不敏感）；
// $$ → 字面 $；$id / ${id}：mapping 中存在则替换，否则原样保留；无效模式原样保留。

func isValidIDRune(r rune, first bool) bool {
	if r == '_' {
		return true
	}
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
		return true
	}
	if !first && r >= '0' && r <= '9' {
		return true
	}
	return false
}

// SafeSubstitute 实现 Python string.Template.safe_substitute 语义。
func SafeSubstitute(tmpl string, mapping map[string]string) string {
	var b strings.Builder
	rs := []rune(tmpl)
	i := 0
	for i < len(rs) {
		if rs[i] != '$' {
			b.WriteRune(rs[i])
			i++
			continue
		}
		// 遇到 $
		if i+1 < len(rs) && rs[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		if i+1 < len(rs) && rs[i+1] == '{' {
			// ${id}
			j := i + 2
			for j < len(rs) && isValidIDRune(rs[j], j == i+2) {
				j++
			}
			if j < len(rs) && rs[j] == '}' && j > i+2 {
				id := string(rs[i+2 : j])
				if v, ok := mapping[id]; ok {
					b.WriteString(v)
				} else {
					b.WriteString("${" + id + "}")
				}
				i = j + 1
				continue
			}
			// 无效 braced 模式：原样保留 $
			b.WriteRune('$')
			i++
			continue
		}
		// $id
		j := i + 1
		for j < len(rs) && isValidIDRune(rs[j], j == i+1) {
			j++
		}
		if j > i+1 {
			id := string(rs[i+1 : j])
			if v, ok := mapping[id]; ok {
				b.WriteString(v)
			} else {
				b.WriteString("$" + id)
			}
			i = j
			continue
		}
		// 无效 named 模式：原样保留 $
		b.WriteRune('$')
		i++
	}
	return b.String()
}

// RenderOptions 是 render 的关键字参数（等价 Python **kwargs）。
type RenderOptions struct {
	Src string
	Tgt string
	// 调用方显式覆盖；nil 表示未提供（注入默认）。
	Overrides map[string]string
	// 其余普通占位符。
	Vars map[string]string
}

// Render 对应 prompts.render：按模板名取模板，注入 6 个语言相关默认占位，
// 再 safe_substitute。未提供的占位原样保留。
func Render(name string, opts RenderOptions) string {
	src := opts.Src
	if src == "" {
		src = "ja"
	}
	tgt := opts.Tgt
	if tgt == "" {
		tgt = "zh"
	}
	m := map[string]string{
		"src_label":            LangLabel(src),
		"tgt_label":            LangLabel(tgt),
		"lang_guidance":        TranslateGuidance(src, "keep_style"),
		"term_guidance":        TermGuidance(src),
		"punct_rule":           strings.TrimSuffix(readPromptFile("punct_rule"), "\n"),
		"review_evidence_tools": strings.TrimSuffix(readPromptFile("review_evidence_tools"), "\n"),
	}
	for k, v := range opts.Overrides {
		m[k] = v
	}
	for k, v := range opts.Vars {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return SafeSubstitute(TemplateText(name), m)
}

func readPromptFile(name string) string {
	b, err := promptFS.ReadFile("promptdata/" + name + ".txt")
	if err != nil {
		return ""
	}
	return string(b)
}

// PunctRule 返回 PUNCT_RULE 常量全文。
func PunctRule() string { return strings.TrimSuffix(readPromptFile("punct_rule"), "\n") }

// ReviewEvidenceTools 返回 REVIEW_EVIDENCE_TOOLS 常量全文。
func ReviewEvidenceTools() string { return strings.TrimSuffix(readPromptFile("review_evidence_tools"), "\n") }

// GlossaryTermLite 是 prompt 渲染所需的术语字段（与 glossary 包的 GlossaryTerm 字段一致；
// 独立定义避免包依赖方向问题——glossary 包 import agents，反向不可）。
type GlossaryTermLite struct {
	Source       string
	Target       string
	Reading      string
	Type         string
	Gender       string
	Aliases      []string
	FirstChapter *int
	Note         string
	Status       string
}

// RenderGlossary 对应 prompts.render_glossary：
// 空 → "（暂无）"；否则逐行 `- {source} → {target}（{type}…） [别名: ...]`。
func RenderGlossary(terms []GlossaryTermLite) string {
	if len(terms) == 0 {
		return "（暂无）"
	}
	var lines []string
	for _, t := range terms {
		// 格式依据主规格 §9.3：`- {source} → {target}（{type}（{gender}，读音:{reading}））`
		// gender/reading 均为空时内层括号整体省略；分册 05 为扁平逗号格式，已登记已知差异清单。
		tag := fmt.Sprintf("（%s）", t.Type)
		var extra []string
		if t.Gender != "" {
			extra = append(extra, t.Gender)
		}
		if t.Reading != "" {
			extra = append(extra, fmt.Sprintf("读音:%s", t.Reading))
		}
		if len(extra) > 0 {
			tag = fmt.Sprintf("（%s（%s））", t.Type, strings.Join(extra, "，"))
		}
		alias := ""
		if len(t.Aliases) > 0 {
			alias = fmt.Sprintf(" [别名: %s]", strings.Join(t.Aliases, ", "))
		}
		lines = append(lines, fmt.Sprintf("- %s → %s%s%s", t.Source, t.Target, tag, alias))
	}
	return strings.Join(lines, "\n")
}

// Numbered 对应 prompts.numbered：`[i] 文本`，0 基编号，换行连接。
func Numbered(texts []string) string {
	var lines []string
	for i, t := range texts {
		lines = append(lines, fmt.Sprintf("[%d] %s", i, t))
	}
	return strings.Join(lines, "\n")
}

// NumberedPairs 对应 prompts.numbered_pairs：`[i] 原文：s\n    译文：t`。
func NumberedPairs(sources, targets []string) string {
	var lines []string
	for i := range sources {
		t := ""
		if i < len(targets) {
			t = targets[i]
		}
		lines = append(lines, fmt.Sprintf("[%d] 原文：%s\n    译文：%s", i, sources[i], t))
	}
	return strings.Join(lines, "\n")
}

// NumberedPairsWithRefs 对应 prompts.numbered_pairs_with_refs。
func NumberedPairsWithRefs(sources, targets, refs []string) string {
	var lines []string
	for i := range sources {
		ref := ""
		if i < len(refs) {
			ref = refs[i]
		}
		t := ""
		if i < len(targets) {
			t = targets[i]
		}
		if ref == "" {
			ref = "（无）"
		}
		lines = append(lines, fmt.Sprintf("[%d] ref=%s 原文：%s\n    译文：%s", i, ref, sources[i], t))
	}
	return strings.Join(lines, "\n")
}
