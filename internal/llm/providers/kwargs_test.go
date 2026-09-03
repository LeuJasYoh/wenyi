package providers

import (
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/llm"
)

// 迁移自 tests/test_llm.py::TestProviderRequestKwargs / TestProviderFactory，
// test_llm_retrying.py（stub 调用器部分），test_usage.py 的 provider 部分。

func strp(s string) *string { return &s }
func intp(i int) *int      { return &i }

func msg(content string) []llm.Message {
	return []llm.Message{{Role: "user", Content: content}}
}

func tierOf[O any](model string, options O) llm.ResolvedTier[O] {
	return llm.ResolvedTier[O]{Model: model, Options: options}
}

// ---- base_request_kwargs（json 模式注入）----

func TestJSONModeAddsKeywordWithoutMutatingMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "仅输出指定对象。"},
		{Role: "user", Content: "x"},
	}
	kwargs := BaseRequestKwargs("m", messages, true)
	if kwargs.ResponseFormat["type"] != "json_object" {
		t.Errorf("response_format = %v", kwargs.ResponseFormat)
	}
	if !strings.Contains(kwargs.Messages[0].Content, "json") {
		t.Errorf("system 应含 json 指令：%q", kwargs.Messages[0].Content)
	}
	if messages[0].Content != "仅输出指定对象。" {
		t.Errorf("原 messages 被改动：%q", messages[0].Content)
	}
}

func TestJSONModeAlsoMentionsJSONInUserMessage(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "仅输出指定对象。"},
		{Role: "user", Content: "翻译这句话。"},
	}
	kwargs := BaseRequestKwargs("m", messages, true)
	last := kwargs.Messages[len(kwargs.Messages)-1]
	if !strings.Contains(strings.ToLower(last.Content), "json") {
		t.Errorf("user 应含 json：%q", last.Content)
	}
	if messages[len(messages)-1].Content != "翻译这句话。" {
		t.Errorf("原 messages 被改动")
	}
}

func TestJSONModeSkipsUserMessageAlreadyMentioningJSON(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "仅输出指定对象。"},
		{Role: "user", Content: "请输出 JSON 数组。"},
	}
	kwargs := BaseRequestKwargs("m", messages, true)
	if kwargs.Messages[len(kwargs.Messages)-1].Content != "请输出 JSON 数组。" {
		t.Errorf("已含 json 的 user 不应再追加：%q", kwargs.Messages[len(kwargs.Messages)-1].Content)
	}
}

// ---- deepseek 方言 ----

func TestDeepSeekDialectAndRecursiveExtraBody(t *testing.T) {
	tier := tierOf("m", commonOptions{Thinking: true, ReasoningEffort: "high",
		ExtraBody: map[string]any{"thinking": map[string]any{"budget": 8192}}})
	kwargs := BuildDeepSeekRequestKwargs(tier, msg("x"), false, nil)
	if kwargs.ReasoningEffort == nil || *kwargs.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort = %v", kwargs.ReasoningEffort)
	}
	thinking := kwargs.ExtraBody["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget"] != 8192 {
		t.Errorf("extra_body = %v", kwargs.ExtraBody)
	}
	disabled := tierOf("m", commonOptions{Thinking: false, ReasoningEffort: "high", ExtraBody: map[string]any{}})
	dk := BuildDeepSeekRequestKwargs(disabled, msg("x"), false, nil)
	if dk.ReasoningEffort != nil {
		t.Errorf("关闭思考不应发送 reasoning_effort")
	}
	if dk.ExtraBody["thinking"].(map[string]any)["type"] != "disabled" {
		t.Errorf("disabled extra_body = %v", dk.ExtraBody)
	}
}

// ---- openrouter 方言 ----

func TestOpenRouterDialectAndExplicitDisable(t *testing.T) {
	enabled := tierOf("m", commonOptions{Thinking: true, ReasoningEffort: "high", ExtraBody: map[string]any{}})
	eb := BuildOpenRouterRequestKwargs(enabled, msg("x"), false, nil).ExtraBody["reasoning"].(map[string]any)
	if eb["effort"] != "high" {
		t.Errorf("enabled = %v", eb)
	}
	disabled := tierOf("m", commonOptions{Thinking: false, ReasoningEffort: "high", ExtraBody: map[string]any{}})
	db := BuildOpenRouterRequestKwargs(disabled, msg("x"), false, nil).ExtraBody["reasoning"].(map[string]any)
	if db["enabled"] != false {
		t.Errorf("disabled = %v", db)
	}
}

// ---- openai 方言 ----

func TestOpenAIDialect(t *testing.T) {
	tier := tierOf("m", commonOptions{Thinking: true, ReasoningEffort: "low", ExtraBody: map[string]any{}})
	kwargs := BuildOpenAIRequestKwargs(tier, msg("x"), false, nil)
	if kwargs.ReasoningEffort == nil || *kwargs.ReasoningEffort != "low" {
		t.Errorf("reasoning_effort = %v", kwargs.ReasoningEffort)
	}
	if kwargs.ExtraBody != nil {
		t.Errorf("extra_body 应为 nil")
	}
	disabled := tierOf("m", commonOptions{Thinking: false, ReasoningEffort: "high", ExtraBody: map[string]any{}})
	dk := BuildOpenAIRequestKwargs(disabled, msg("x"), false, nil)
	if dk.ReasoningEffort == nil || *dk.ReasoningEffort != "none" {
		t.Errorf("disabled effort = %v", dk.ReasoningEffort)
	}
}

func TestOpenAIUsesMaxCompletionTokens(t *testing.T) {
	enabled := tierOf("m", commonOptions{Thinking: true, ReasoningEffort: "high", ExtraBody: map[string]any{}})
	disabled := tierOf("m", commonOptions{Thinking: false, ReasoningEffort: "high", ExtraBody: map[string]any{}})
	ek := BuildOpenAIRequestKwargs(enabled, msg("x"), false, intp(100))
	dk := BuildOpenAIRequestKwargs(disabled, msg("x"), false, intp(100))
	if ek.MaxTokens != nil {
		t.Errorf("不应有 max_tokens")
	}
	if ek.MaxCompletionTokens == nil || *ek.MaxCompletionTokens != 4096 {
		t.Errorf("enabled max_completion_tokens = %v", ek.MaxCompletionTokens)
	}
	if dk.MaxCompletionTokens == nil || *dk.MaxCompletionTokens != 100 {
		t.Errorf("disabled max_completion_tokens = %v", dk.MaxCompletionTokens)
	}
}

// ---- 通用兼容端点 reasoning_style ----

func TestGenericCompatibleEndpointMapsReasoningDialects(t *testing.T) {
	tier := tierOf("m", OpenAICompatibleTierOptions{Thinking: true, ReasoningEffort: "medium",
		RequestOverrides: map[string]any{"thinking": map[string]any{"budget": 8192}}})
	deepseek := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, intp(100), "deepseek")
	openaiK := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, nil, "openai")
	openrouter := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, nil, "openrouter")

	if deepseek.ReasoningEffort == nil || *deepseek.ReasoningEffort != "medium" {
		t.Errorf("deepseek effort = %v", deepseek.ReasoningEffort)
	}
	th := deepseek.ExtraBody["thinking"].(map[string]any)
	if th["type"] != "enabled" || th["budget"] != 8192 {
		t.Errorf("deepseek extra_body = %v", deepseek.ExtraBody)
	}
	if deepseek.MaxTokens == nil || *deepseek.MaxTokens != 4096 {
		t.Errorf("deepseek max_tokens = %v", deepseek.MaxTokens)
	}
	if openaiK.ReasoningEffort == nil || *openaiK.ReasoningEffort != "medium" {
		t.Errorf("openai effort = %v", openaiK.ReasoningEffort)
	}
	if _, ok := openaiK.ExtraBody["thinking"]; !ok || openaiK.ExtraBody["thinking"].(map[string]any)["budget"] != 8192 {
		t.Errorf("openai extra_body = %v", openaiK.ExtraBody)
	}
	or := openrouter.ExtraBody["reasoning"].(map[string]any)
	if or["effort"] != "medium" {
		t.Errorf("openrouter reasoning = %v", or)
	}
	if openrouter.ExtraBody["thinking"].(map[string]any)["budget"] != 8192 {
		t.Errorf("openrouter extra_body = %v", openrouter.ExtraBody)
	}
}

func TestGenericCompatibleEndpointExplicitlyDisablesReasoning(t *testing.T) {
	tier := tierOf("m", OpenAICompatibleTierOptions{Thinking: false, ReasoningEffort: "high", RequestOverrides: map[string]any{}})
	ds := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, nil, "deepseek")
	if ds.ExtraBody["thinking"].(map[string]any)["type"] != "disabled" {
		t.Errorf("deepseek disabled = %v", ds.ExtraBody)
	}
	oa := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, nil, "openai")
	if oa.ReasoningEffort == nil || *oa.ReasoningEffort != "none" {
		t.Errorf("openai disabled = %v", oa.ReasoningEffort)
	}
	or := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, nil, "openrouter")
	if or.ExtraBody["reasoning"].(map[string]any)["enabled"] != false {
		t.Errorf("openrouter disabled = %v", or.ExtraBody)
	}
}

func TestGenericCompatibleEndpointCanOnlyUseRawOverrides(t *testing.T) {
	tier := tierOf("m", OpenAICompatibleTierOptions{Thinking: true, ReasoningEffort: "high",
		RequestOverrides: map[string]any{"enable_thinking": true}})
	kwargs := BuildOpenAICompatibleRequestKwargs(tier, msg("x"), false, intp(100), "none")
	if kwargs.ReasoningEffort != nil {
		t.Errorf("none 方言不应有 reasoning_effort")
	}
	if kwargs.ExtraBody["enable_thinking"] != true {
		t.Errorf("extra_body = %v", kwargs.ExtraBody)
	}
	if kwargs.MaxTokens == nil || *kwargs.MaxTokens != 4096 {
		t.Errorf("thinking=true 应有 4096 下限：%v", kwargs.MaxTokens)
	}
}

// ---- 档位解析 ----

func llmCfg(tiers map[string]*config.TierConfig, mutate func(*config.LLMConfig)) *config.LLMConfig {
	cfg := &config.LLMConfig{Provider: "x", Timeout: 600, MaxRetries: 0, Tiers: tiers}
	if cfg.Tiers == nil {
		cfg.Tiers = map[string]*config.TierConfig{}
	}
	if mutate != nil {
		mutate(cfg)
	}
	return cfg
}

func tierCfg(model string, options map[string]any) *config.TierConfig {
	return &config.TierConfig{Model: strp(model), Options: options}
}

func TestResolveProviderTiersOverridesAndDefaults(t *testing.T) {
	tiers, err := ResolveProviderTiers(map[string]*config.TierConfig{
		"fast": tierCfg("custom-fast", map[string]any{"thinking": false}),
	}, deepSeekDefaultTiers(), parseDeepSeekOptions)
	if err != nil {
		t.Fatal(err)
	}
	if tiers["fast"].Model != "custom-fast" {
		t.Errorf("fast.model = %s", tiers["fast"].Model)
	}
	if tiers["strong"].Model != "deepseek-v4-pro" || tiers["cheap"].Model != "deepseek-v4-flash" {
		t.Errorf("默认档被破坏")
	}

	// 只覆盖 options 不重复 model
	tiers2, _ := ResolveProviderTiers(map[string]*config.TierConfig{
		"fast": {Options: map[string]any{"thinking": true}},
	}, deepSeekDefaultTiers(), parseDeepSeekOptions)
	if tiers2["fast"].Model != "deepseek-v4-flash" || !tiers2["fast"].Options.Thinking {
		t.Errorf("fast = %+v", tiers2["fast"])
	}

	// 未知 option 拒绝
	_, err = ResolveProviderTiers(map[string]*config.TierConfig{
		"strong": tierCfg("m", map[string]any{"unknown_option": true}),
	}, deepSeekDefaultTiers(), parseDeepSeekOptions)
	if err == nil || !strings.Contains(err.Error(), "unknown_option") {
		t.Errorf("err = %v, want 含 unknown_option", err)
	}

	// 缺 strong
	_, err = ResolveProviderTiers(nil, nil, parseDeepSeekOptions)
	if err == nil || !strings.Contains(err.Error(), "strong") {
		t.Errorf("err = %v, want 缺 strong 报错", err)
	}
}

// ---- DeepSeekClient 默认与用量 ----

func minimalDeepSeekCfg(maxRetries int) *config.LLMConfig {
	return llmCfg(map[string]*config.TierConfig{
		"strong": tierCfg("m1", nil),
		"cheap":  tierCfg("m2", nil),
	}, func(c *config.LLMConfig) {
		c.Provider = "deepseek"
		c.BaseURL = strp("x")
		c.APIKeyEnv = strp("X")
		c.Timeout = 1
		c.MaxRetries = maxRetries
	})
}

func TestDeepSeekProviderDefaults(t *testing.T) {
	cfg := llmCfg(nil, nil)
	c, err := NewDeepSeekClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != DeepSeekDefaultBaseURL || c.APIKeyEnv != DeepSeekDefaultAPIKeyEnv {
		t.Errorf("defaults = %s %s", c.BaseURL, c.APIKeyEnv)
	}
	if c.Tiers["strong"].Model != "deepseek-v4-pro" || c.Tiers["cheap"].Model != "deepseek-v4-flash" {
		t.Errorf("models 不符")
	}
	if !c.Tiers["strong"].Options.Thinking || c.Tiers["fast"].Options.Thinking {
		t.Errorf("thinking 配置不符")
	}
}

// stubCaller：按序返回结果或错误。
type stubCaller struct {
	outcomes []any
	idx      int
	calls    int
}

func (s *stubCaller) Create(kwargs RequestKwargs) (*ChatResponse, error) {
	s.calls++
	if s.idx >= len(s.outcomes) {
		t := &testing.T{}
		_ = t
		return nil, &llm.TransportError{Kind: "connection", Msg: "stub 响应已耗尽"}
	}
	out := s.outcomes[s.idx]
	s.idx++
	switch o := out.(type) {
	case error:
		return nil, o
	case *ChatResponse:
		return o, nil
	}
	return nil, nil
}

func makeUsage(prompt, completion, total int64, hit, miss int64) map[string]any {
	m := map[string]any{
		"prompt_tokens":            prompt,
		"completion_tokens":        completion,
		"prompt_cache_hit_tokens":  hit,
		"prompt_cache_miss_tokens": miss,
	}
	if total >= 0 {
		m["total_tokens"] = total
	}
	return m
}

func makeResponse(content string, usage map[string]any) *ChatResponse {
	resp := &ChatResponse{Usage: usage}
	resp.Choices = append(resp.Choices, struct {
		Message struct {
			Content *string `json:"content"`
		} `json:"message"`
	}{})
	resp.Choices[0].Message.Content = strp(content)
	return resp
}

func TestDeepSeekRecordsUsageAndSplitsByTier(t *testing.T) {
	c, err := NewDeepSeekClient(minimalDeepSeekCfg(0))
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubCaller{outcomes: []any{
		makeResponse("strong-out", makeUsage(1000, 200, 1200, 800, 200)),
		makeResponse("cheap-out", makeUsage(500, 100, 600, 100, 400)),
	}}
	c.SetCallerForTesting(stub)
	out1, err := c.Complete(msg("hi"), "strong", false, nil, "Translator")
	if err != nil || out1 != "strong-out" {
		t.Fatalf("complete = %q %v", out1, err)
	}
	out2, _ := c.Complete(msg("hi"), "cheap", false, nil, "")
	if out2 != "cheap-out" {
		t.Fatalf("complete = %q", out2)
	}
	summary := c.UsageSummary()
	totals := summary["totals"].(map[string]any)
	getI := func(m map[string]any, k string) int64 {
		switch v := m[k].(type) {
		case int64:
			return v
		case int:
			return int64(v)
		}
		return 0
	}
	if getI(totals, "prompt_tokens") != 1500 || getI(totals, "total_tokens") != 1800 || getI(totals, "calls") != 2 {
		t.Errorf("totals = %v", totals)
	}
	if totals["cache_hit_rate"] != 0.6 {
		t.Errorf("rate = %v", totals["cache_hit_rate"])
	}
	byTier := summary["by_tier"].(map[string]any)
	if byTier["strong"].(map[string]any)["cache_hit_rate"] != 0.8 || byTier["cheap"].(map[string]any)["cache_hit_rate"] != 0.2 {
		t.Errorf("by_tier rates 不符")
	}
	byStage := summary["by_stage"].(map[string]any)
	if len(byStage) != 1 {
		t.Errorf("by_stage = %v", byStage)
	}
	if byStage["Translator"].(map[string]any)["cache_hit_rate"] != 0.8 {
		t.Errorf("stage rate = %v", byStage["Translator"])
	}
}

// ---- OpenAI 嵌套缓存明细 ----

func TestOpenAINestedCachedTokensAreNormalized(t *testing.T) {
	cfg := llmCfg(map[string]*config.TierConfig{"strong": tierCfg("m", nil)}, func(c *config.LLMConfig) {
		c.Provider = "openai"
		c.BaseURL = strp("x")
		c.APIKeyEnv = strp("X")
	})
	client, err := NewOpenAIClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	usage := map[string]any{
		"prompt_tokens":     100,
		"completion_tokens": 20,
		"total_tokens":      120,
		"prompt_tokens_details": map[string]any{"cached_tokens": 40},
	}
	client.SetCallerForTesting(&stubCaller{outcomes: []any{makeResponse("ok", usage)}})
	out, err := client.Complete(msg("x"), "strong", false, nil, "Translator")
	if err != nil || out != "ok" {
		t.Fatalf("complete = %q %v", out, err)
	}
	totals := client.UsageSummary()["totals"].(map[string]any)
	getI := func(m map[string]any, k string) int64 {
		switch v := m[k].(type) {
		case int64:
			return v
		case int:
			return int64(v)
		}
		return 0
	}
	if getI(totals, "cache_hit_tokens") != 40 || getI(totals, "cache_miss_tokens") != 60 {
		t.Errorf("totals = %v", totals)
	}
	if totals["cache_hit_rate"] != 0.4 {
		t.Errorf("rate = %v", totals["cache_hit_rate"])
	}
}

func TestMissingCacheDetailsRemainUnknown(t *testing.T) {
	sample := NormalizeOpenAIUsage(map[string]any{
		"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120,
	})
	tracker := &llm.UsageTracker{}
	tracker.Record("strong", sample, "")
	totals := tracker.Summary()["totals"].(map[string]any)
	getI := func(m map[string]any, k string) int64 {
		switch v := m[k].(type) {
		case int64:
			return v
		case int:
			return int64(v)
		}
		return 0
	}
	if getI(totals, "cache_hit_tokens") != 0 || getI(totals, "cache_miss_tokens") != 0 {
		t.Errorf("totals = %v", totals)
	}
}

func TestCompleteWithNoneUsageDoesNotCount(t *testing.T) {
	c, _ := NewDeepSeekClient(minimalDeepSeekCfg(0))
	c.SetCallerForTesting(&stubCaller{outcomes: []any{makeResponse("ok", nil)}})
	out, err := c.Complete(msg("x"), "strong", false, nil, "")
	if err != nil || out != "ok" {
		t.Fatalf("complete = %q %v", out, err)
	}
	summary := c.UsageSummary()
	totals := summary["totals"].(map[string]any)
	if totals["calls"] != int64(0) {
		t.Errorf("calls = %v", totals["calls"])
	}
	if len(summary["by_tier"].(map[string]any)) != 0 {
		t.Errorf("by_tier 应为空")
	}
}
