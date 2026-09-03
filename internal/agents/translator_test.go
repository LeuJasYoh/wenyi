package agents

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
	"wenyi/internal/pipeline/checks"
)

// 迁移自 tests/test_translator.py（断言语义等价）。

var numberedRe = regexp.MustCompile(`(?m)^\[(\d+)\]`)

func countSegments(userContent string) int {
	return len(numberedRe.FindAllString(userContent, -1))
}

func translatorTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.FromDict(map[string]any{
		"language": map[string]any{"source": "ja", "target": "zh"},
		"llm": map[string]any{
			"provider": "fake",
			"tiers": map[string]any{
				"strong": map[string]any{"model": "deepseek-v4-pro"},
				"cheap":  map[string]any{"model": "deepseek-v4-flash"},
			},
		},
		"pipeline": map[string]any{"align_retry_limit": 1},
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func translationsJSON(n int, dropLast bool) string {
	count := n
	if dropLast && count > 1 {
		count = count - 1
	}
	items := []any{}
	for i := 0; i < count; i++ {
		items = append(items, fmt.Sprintf("译%d", i))
	}
	om := jsonx.NewOMap()
	om.Set("translations", items)
	b, _ := jsonx.Marshal(om)
	return string(b)
}

func TestHappyPathAligned(t *testing.T) {
	handler := func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		n := countSegments(messages[len(messages)-1].Content)
		return translationsJSON(n, false), nil
	}
	cfg := translatorTestConfig(t)
	tr := NewTranslator(llm.NewFakeClient(handler), cfg)
	out, err := tr.TranslateBatch([]string{"あ", "い", "う"}, nil, "", "", "", "")
	if err != nil {
		t.Fatalf("TranslateBatch: %v", err)
	}
	want := []string{"译0", "译1", "译2"}
	if len(out) != 3 || out[0] != want[0] || out[1] != want[1] || out[2] != want[2] {
		t.Errorf("out = %v, want %v", out, want)
	}
}

func TestFallbackToPerSegmentOnMismatch(t *testing.T) {
	handler := func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		n := countSegments(messages[len(messages)-1].Content)
		return translationsJSON(n, true), nil // 故意制造段数不符
	}
	client := llm.NewFakeClient(handler)
	cfg := translatorTestConfig(t)
	tr := NewTranslator(client, cfg)
	out, err := tr.TranslateBatch([]string{"あ", "い", "う"}, nil, "", "", "", "")
	if err != nil {
		t.Fatalf("TranslateBatch: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("兜底后长度 = %d, want 3", len(out))
	}
	singleCalls := 0
	for _, c := range client.Calls {
		msgs := c["messages"].([]llm.Message)
		if countSegments(msgs[len(msgs)-1].Content) == 1 {
			singleCalls++
		}
	}
	if singleCalls < 3 {
		t.Errorf("逐段调用次数 = %d, want >= 3", singleCalls)
	}
}

func TestEmptyPerSegmentFallbackIsRejected(t *testing.T) {
	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"translations": []}`, nil
	})
	cfg := translatorTestConfig(t)
	tr := NewTranslator(client, cfg)
	_, err := tr.TranslateBatch([]string{"あ", "い"}, nil, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "第 0 段失败") {
		t.Errorf("err = %v, want 含 '第 0 段失败'", err)
	}
}

func TestNonStringTranslationIsRejected(t *testing.T) {
	client := llm.NewFakeClient(func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		return `{"translations": [null]}`, nil
	})
	cfg := translatorTestConfig(t)
	tr := NewTranslator(client, cfg)
	_, err := tr.TranslateBatch([]string{"あ"}, nil, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "第 0 段失败") {
		t.Errorf("err = %v, want 含 '第 0 段失败'", err)
	}
}

func TestProviderFailureIsNotRetriedByAlignmentLayer(t *testing.T) {
	handler := func(messages []llm.Message, tier string, jsonMode bool) (string, error) {
		return "", errors.New("provider unavailable")
	}
	client := llm.NewFakeClient(handler)
	cfg := translatorTestConfig(t)
	tr := NewTranslator(client, cfg)
	_, err := tr.TranslateBatch([]string{"あ", "い"}, nil, "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("err = %v, want 含 'provider unavailable'", err)
	}
	if len(client.Calls) != 1 {
		t.Errorf("调用次数 = %d, want 1（传输异常不得被对齐层放大）", len(client.Calls))
	}
}

func TestTranslatorPromptOrder(t *testing.T) {
	tpl := TemplateText("translator_user")
	i1 := strings.Index(tpl, "【本章梗概】")
	i2 := strings.Index(tpl, "【专有名词对照表】")
	if i1 < 0 || i2 < 0 || !(i1 < i2) {
		t.Errorf("【本章梗概】(%d) 应在 【专有名词对照表】(%d) 之前", i1, i2)
	}
}

func TestLengthFlags(t *testing.T) {
	sources := []string{strings.Repeat("これは長い日本語の文章です。", 3), "短い", strings.Repeat("x", 10)}
	targets := []string{"", "短い但正常的中文译文内容", strings.Repeat("x", 40)}
	flags := checks.LengthFlags(sources, targets, 0.30, 3.0)
	kinds := map[int]string{}
	for _, f := range flags {
		kinds[f.Index] = f.Reason
	}
	if kinds[0] != "empty" {
		t.Errorf("index 0 = %q, want empty", kinds[0])
	}
	if kinds[2] != "too_long" {
		t.Errorf("index 2 = %q, want too_long", kinds[2])
	}
}
