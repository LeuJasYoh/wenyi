package agents

// backtranslator.go 回译抽检（主规格 §12.4 BackTranslator 部分）。

import (
	"fmt"
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// BackTranslator 回译 + 保真度核查（回译 tier=fast；比对 tier=cheap）。
type BackTranslator struct {
	Agent
}

func NewBackTranslator(client llm.LLMClient, cfg *config.Config) *BackTranslator {
	b := &BackTranslator{}
	b.Agent = NewAgent(client, cfg, "BackTranslator")
	return b
}

// Backtranslate 回译（fast 档；非 list 返回 []）。
func (b *BackTranslator) Backtranslate(targets []string) []string {
	system := Render("backtranslate_system", RenderOptions{Src: b.Src, Tgt: b.Tgt})
	user := Render("backtranslate_user", RenderOptions{
		Src: b.Src, Tgt: b.Tgt,
		Vars: map[string]string{
			"n":               fmt.Sprintf("%d", len(targets)),
			"numbered_target": Numbered(targets),
		},
	})
	data, err := b.AskJSONDefault(system, user, "fast", "backtranslations", nil, nil)
	if err != nil {
		return []string{}
	}
	arr, ok := data.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		} else {
			out = append(out, "")
		}
	}
	return out
}

// BacktransCompareSystem 保真度核查 system（运行时生成，_backtrans_compare_system(src)）。
func BacktransCompareSystem(src string) string {
	label := LangLabel(src)
	return fmt.Sprintf(`你是%s小说到%s译文的翻译保真度核查员。逐段比较回译文本与原始原文，只报告实质性的语义偏离：`+
		`意义改变、信息丢失、关键细节（数字/否定/人称/时态）错误。合理的措辞差异、风格调整不算问题，拿不准就不报。`+
		`仅输出 JSON：{"issues":[{"index":整数段号,"detail":"偏离说明"}]}。`, label, "中文")
}

// Check 回译抽检比对：回译长度不符 → 返回 []；返回 issue（含 index/detail）。
func (b *BackTranslator) Check(sources, targets []string) []*jsonx.OMap {
	back := b.Backtranslate(targets)
	if len(back) != len(sources) {
		return []*jsonx.OMap{}
	}
	system := BacktransCompareSystem(b.Src)
	user := "【原文与回译逐段对照】（回译为译文经模型回译回源语言的结果）\n" +
		numberedSourcePairs(sources, back) +
		"\n\n请逐段核查并输出 JSON：{\"issues\":[...]}。"
	data, err := b.AskJSONDefault(system, user, "cheap", "issues", nil, []*jsonx.OMap{})
	if err != nil {
		return []*jsonx.OMap{}
	}
	return DictItems(data)
}

func numberedSourcePairs(sources, back []string) string {
	var lines []string
	for i := range sources {
		lines = append(lines, fmt.Sprintf("[%d] 原文：%s\n    回译：%s", i, sources[i], back[i]))
	}
	return strings.Join(lines, "\n")
}
