package agents

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/llm"
)

// AlignmentError 对齐失败异常（Python: class AlignmentError(Exception)）。
type AlignmentError struct{ Msg string }

func (e *AlignmentError) Error() string { return e.Msg }

// Translator 文学翻译 Agent（主规格 §9.4 / 分册 05 §7；tier=strong）。
// 核心保证：输入 N 段输出 N 段一一对应；绝不用空串占位。
type Translator struct {
	Agent
}

func NewTranslator(client llm.LLMClient, cfg *config.Config) *Translator {
	t := &Translator{}
	t.Agent = NewAgent(client, cfg, "Translator")
	return t
}

func orNone(s string) string {
	if s == "" {
		return "（无）"
	}
	return s
}

// TranslateBatch 翻译一批：整批（align_retry_limit+1 次尝试）→ 逐段兜底。
// 传输错误只由 provider 层重试，这里不二次放大。
func (t *Translator) TranslateBatch(sources []string, glossary []GlossaryTermLite,
	style, context, bookSynopsis, chapterDigest string) ([]string, error) {
	if len(sources) == 0 {
		return []string{}, nil
	}
	attempts := t.Config.Pipeline.AlignRetryLimit + 1
	for i := 0; i < attempts; i++ {
		out, err := t.callBatch(sources, glossary, style, context, bookSynopsis, chapterDigest)
		if err == nil {
			return out, nil
		}
		if ae, ok := err.(*AlignmentError); ok {
			_ = ae
			continue
		}
		return nil, err
	}
	results := make([]string, len(sources))
	for idx, source := range sources {
		one, err := t.callBatch([]string{source}, glossary, style, context, bookSynopsis, chapterDigest)
		if err != nil {
			return nil, &AlignmentError{Msg: fmt.Sprintf("逐段兜底翻译在第 %d 段失败", idx)}
		}
		results[idx] = one[0]
	}
	return results, nil
}

// callBatch 渲染 prompt → _ask_json(key="translations") → 严格校验（类型/等长/非空字符串）。
func (t *Translator) callBatch(sources []string, glossary []GlossaryTermLite,
	style, context, bookSynopsis, chapterDigest string) ([]string, error) {
	n := len(sources)
	system := Render("translator_system", RenderOptions{
		Src: t.Src, Tgt: t.Tgt,
		Overrides: map[string]string{
			"lang_guidance": TranslateGuidance(t.Src, t.Config.HonorificStrategy),
		},
	})
	user := Render("translator_user", RenderOptions{
		Src: t.Src, Tgt: t.Tgt,
		Vars: map[string]string{
			"style":           orNone(style),
			"book_synopsis":   orNone(bookSynopsis),
			"chapter_digest":  orNone(chapterDigest),
			"glossary":        RenderGlossary(glossary),
			"context":         orNone(context),
			"n":               strconv.Itoa(n),
			"n_minus_1":       strconv.Itoa(n - 1),
			"numbered_source": Numbered(sources),
		},
	})
	data, err := t.AskJSON(system, user, "strong", "translations", nil)
	if err != nil {
		var jpe *llm.JsonParseError
		if errors.As(err, &jpe) {
			return nil, &AlignmentError{Msg: "模型返回的译文 JSON 无法解析"}
		}
		return nil, err
	}
	items, ok := data.([]any)
	if !ok {
		return nil, &AlignmentError{Msg: "模型未返回译文数组"}
	}
	if len(items) != n {
		return nil, &AlignmentError{Msg: fmt.Sprintf("译文数量不匹配：期望 %d 段，实际 %d 段", n, len(items))}
	}
	out := make([]string, n)
	for i, item := range items {
		s, ok := item.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, &AlignmentError{Msg: "模型返回了空译文或非字符串译文"}
		}
		out[i] = s
	}
	return out, nil
}
