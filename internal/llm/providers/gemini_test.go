package providers

import (
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// 迁移自 tests/test_llm_gemini.py 与 test_llm_retrying.py 的 stub 调用器部分、
// test_llm.py::TestProviderFactory。

func geminiCfg(mutate func(*config.LLMConfig)) *config.LLMConfig {
	cfg := &config.LLMConfig{Provider: "gemini", Timeout: 600, MaxRetries: 0, Tiers: map[string]*config.TierConfig{}}
	if mutate != nil {
		mutate(cfg)
	}
	return cfg
}

func TestGeminiTierOptionsThinkingMutualExclusion(t *testing.T) {
	_, err := parseGeminiOptions("strong", map[string]any{"thinking_level": "high"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseGeminiOptions("strong", map[string]any{"thinking_budget": 1024})
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseGeminiOptions("strong", map[string]any{"thinking_level": "high", "thinking_budget": 1024})
	if err == nil || !strings.Contains(err.Error(), "thinking_level 与 thinking_budget 互斥") {
		t.Errorf("err = %v", err)
	}
}

func TestGeminiAPIKeyEnvPrecedence(t *testing.T) {
	t.Setenv("CUSTOM_KEY", "custom_val")
	t.Setenv("GEMINI_API_KEY", "gemini_val")
	t.Setenv("GOOGLE_API_KEY", "google_val")
	if key, env := GetAPIKeyFromEnv("CUSTOM_KEY"); key != "custom_val" || env != "CUSTOM_KEY" {
		t.Errorf("custom: %q %q", key, env)
	}
	if key, env := GetAPIKeyFromEnv(""); key != "gemini_val" || env != "GEMINI_API_KEY" {
		t.Errorf("gemini: %q %q", key, env)
	}
	t.Setenv("GEMINI_API_KEY", "")
	if key, env := GetAPIKeyFromEnv(""); key != "google_val" || env != "GOOGLE_API_KEY" {
		t.Errorf("google: %q %q", key, env)
	}
	t.Setenv("GOOGLE_API_KEY", "")
	if key, env := GetAPIKeyFromEnv(""); key != "" || env != "GEMINI_API_KEY" {
		t.Errorf("none: %q %q", key, env)
	}
}

func TestConvertMessagesToGemini(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: "You are a translator."},
		{Role: "system", Content: "Translate carefully."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "你好"},
		{Role: "user", Content: "World"},
	}
	sysInst, contents := ConvertMessagesToGemini(messages)
	if sysInst == nil || *sysInst != "You are a translator.\n\nTranslate carefully." {
		t.Errorf("sys = %v", sysInst)
	}
	if len(contents) != 3 {
		t.Fatalf("contents = %v", contents)
	}
	if contents[0]["role"] != "user" || contents[1]["role"] != "model" || contents[2]["role"] != "user" {
		t.Errorf("roles 不符：%v", contents)
	}
	p0 := contents[0]["parts"].([]map[string]any)[0]["text"]
	if p0 != "Hello" {
		t.Errorf("p0 = %v", p0)
	}
}

func TestExtractGeminiUsage(t *testing.T) {
	meta := map[string]any{
		"prompt_token_count": 100, "candidates_token_count": 50,
		"thoughts_token_count": 25, "total_token_count": 175,
		"cached_content_token_count": 30,
	}
	sample := ExtractGeminiUsage(meta)
	if sample.PromptTokens != 100 || sample.CompletionTokens != 75 || sample.TotalTokens != 175 ||
		sample.CacheHitTokens != 30 || sample.CacheMissTokens != 70 {
		t.Errorf("sample = %+v", sample)
	}
	if ExtractGeminiUsage(nil) != nil {
		t.Errorf("nil 应返回 nil")
	}
}

func TestGeminiValidateCredentials(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) { c.APIKeyEnv = strp("TEST_MISSING_ENV_KEY") })
	client, err := NewGeminiClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_MISSING_ENV_KEY", "")
	if err := client.ValidateCredentials(); err == nil || !strings.Contains(err.Error(), "未设置环境变量") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("TEST_MISSING_ENV_KEY", "valid_key")
	if err := client.ValidateCredentials(); err != nil {
		t.Errorf("err = %v", err)
	}
}

// geminiStubCaller：记录参数、按序返回结果/错误。
type geminiStubCaller struct {
	responses []any
	calls     int
	lastModel string
	lastConfig map[string]any
	timeoutMS int
}

func (g *geminiStubCaller) GenerateContent(model string, contents []map[string]any, configKwargs map[string]any) (*GeminiResponse, error) {
	g.calls++
	g.lastModel = model
	g.lastConfig = configKwargs
	if g.calls > len(g.responses) {
		return nil, &llm.TransportError{Kind: "connection", Msg: "stub 耗尽"}
	}
	switch o := g.responses[g.calls-1].(type) {
	case error:
		return nil, o
	case *GeminiResponse:
		return o, nil
	}
	return nil, nil
}

func (g *geminiStubCaller) TimeoutMS() int { return g.timeoutMS }

func TestGeminiTimeoutInMilliseconds(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) {
		c.APIKeyEnv = strp("TEST_GEMINI_KEY")
		c.Timeout = 17
	})
	t.Setenv("TEST_GEMINI_KEY", "valid_key")
	client, _ := NewGeminiClient(cfg)
	caller, err := client.EnsureCaller()
	if err != nil {
		t.Fatal(err)
	}
	if caller.TimeoutMS() != 17000 {
		t.Errorf("timeoutMS = %d, want 17000", caller.TimeoutMS())
	}
}

func TestGeminiCompleteAndUsage(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) {
		c.APIKeyEnv = strp("TEST_GEMINI_KEY")
		c.Tiers["strong"] = tierCfg("gemini-3.6-flash", map[string]any{"temperature": 0.3})
	})
	resp := &GeminiResponse{Text: "翻译结果测试", UsageMetadata: map[string]any{
		"prompt_token_count": 80, "candidates_token_count": 20,
		"thoughts_token_count": 5, "total_token_count": 105, "cached_content_token_count": 0,
	}}
	resp.Candidates = append(resp.Candidates, struct {
		FinishReason any `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text any `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}{FinishReason: "STOP"})
	stub := &geminiStubCaller{responses: []any{resp}}
	client, _ := NewGeminiClient(cfg)
	client.SetCallerForTesting(stub)
	out, err := client.Complete([]llm.Message{{Role: "user", Content: "test"}}, "strong", false, nil, "translation")
	if err != nil || out != "翻译结果测试" {
		t.Fatalf("complete = %q %v", out, err)
	}
	if stub.lastModel != "gemini-3.6-flash" {
		t.Errorf("model = %s", stub.lastModel)
	}
	if stub.lastConfig["temperature"] != 0.3 {
		t.Errorf("temperature = %v", stub.lastConfig["temperature"])
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
	if getI(totals, "prompt_tokens") != 80 || getI(totals, "completion_tokens") != 25 {
		t.Errorf("totals = %v", totals)
	}
}

func TestGeminiRetriesServerErrorAndRecordsWait(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) {
		c.MaxRetries = 1
		c.Tiers["strong"] = tierCfg("gemini-3.6-flash", nil)
	})
	failure := &llm.HTTPError{StatusCode: 503, Headers: map[string]string{"retry-after-ms": "0"}}
	success := &GeminiResponse{Text: "ok"}
	success.Candidates = append(success.Candidates, struct {
		FinishReason any `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text any `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}{FinishReason: "STOP"})
	stub := &geminiStubCaller{responses: []any{failure, success}}
	client, _ := NewGeminiClient(cfg)
	client.SetCallerForTesting(stub)
	var events []map[string]any
	client.SetEventSink(func(event string, data map[string]any) {
		d := map[string]any{"event": event}
		for k, v := range data {
			d[k] = v
		}
		events = append(events, d)
	})
	out, err := client.Complete([]llm.Message{{Role: "user", Content: "test"}}, "strong", false, nil, "")
	if err != nil || out != "ok" {
		t.Fatalf("complete = %q %v", out, err)
	}
	if stub.calls != 2 {
		t.Errorf("calls = %d", stub.calls)
	}
	if len(events) != 1 || events[0]["event"] != "llm_retry_wait" {
		t.Fatalf("events = %v", events)
	}
	if events[0]["reason"] != "http_503" {
		t.Errorf("reason = %v", events[0]["reason"])
	}
}

func TestGeminiDoesNotRetryClientError(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) {
		c.MaxRetries = 4
		c.Tiers["strong"] = tierCfg("gemini-3.6-flash", nil)
	})
	failure := &llm.HTTPError{StatusCode: 401}
	stub := &geminiStubCaller{responses: []any{failure}}
	client, _ := NewGeminiClient(cfg)
	client.SetCallerForTesting(stub)
	var events []map[string]any
	client.SetEventSink(func(event string, data map[string]any) {
		events = append(events, map[string]any{"event": event})
	})
	if _, err := client.Complete([]llm.Message{{Role: "user", Content: "test"}}, "strong", false, nil, ""); err == nil {
		t.Fatal("期望报错")
	}
	if stub.calls != 1 {
		t.Errorf("calls = %d", stub.calls)
	}
	if len(events) != 0 {
		t.Errorf("events = %v", events)
	}
}

func TestGeminiJSONMode(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) {
		c.APIKeyEnv = strp("TEST_GEMINI_KEY")
		c.Tiers["strong"] = tierCfg("gemini-3.6-flash", nil)
	})
	resp := &GeminiResponse{Text: `{"status": "ok", "result": 123}`}
	resp.Candidates = append(resp.Candidates, struct {
		FinishReason any `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text any `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}{FinishReason: "STOP"})
	stub := &geminiStubCaller{responses: []any{resp}}
	client, _ := NewGeminiClient(cfg)
	client.SetCallerForTesting(stub)
	v, err := client.CompleteJSON([]llm.Message{{Role: "user", Content: "return json"}}, "strong", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	om, ok := v.(*jsonx.OMap)
	if !ok {
		t.Fatalf("结果应为对象：%T", v)
	}
	if status, _ := om.Get("status"); status != "ok" {
		t.Errorf("status = %v", status)
	}
	if stub.lastConfig["response_mime_type"] != "application/json" {
		t.Errorf("config = %v", stub.lastConfig)
	}
}

func TestGeminiSafetyBlock(t *testing.T) {
	cfg := geminiCfg(func(c *config.LLMConfig) {
		c.APIKeyEnv = strp("TEST_GEMINI_KEY")
		c.Tiers["strong"] = tierCfg("gemini-3.6-flash", nil)
	})
	resp := &GeminiResponse{Text: nil}
	resp.Candidates = append(resp.Candidates, struct {
		FinishReason any `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text any `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}{FinishReason: "SAFETY"})
	stub := &geminiStubCaller{responses: []any{resp}}
	client, _ := NewGeminiClient(cfg)
	client.SetCallerForTesting(stub)
	_, err := client.Complete([]llm.Message{{Role: "user", Content: "unsafe content"}}, "strong", false, nil, "")
	if err == nil || !strings.Contains(err.Error(), "安全拦截") {
		t.Errorf("err = %v", err)
	}
}

// ---- DeepSeek 重试集成（test_llm_retrying.py）----

func TestTransientErrorRetriesOnceAndRecordsWaitEvent(t *testing.T) {
	client, _ := NewDeepSeekClient(minimalDeepSeekCfg(1))
	stub := &stubCaller{outcomes: []any{
		&llm.HTTPError{StatusCode: 502, Headers: map[string]string{"retry-after-ms": "0"}, RequestID: "req-test"},
		makeResponse("ok", nil),
	}}
	client.SetCallerForTesting(stub)
	var events []map[string]any
	client.SetEventSink(func(event string, data map[string]any) {
		d := map[string]any{"event": event}
		for k, v := range data {
			d[k] = v
		}
		events = append(events, d)
	})
	out, err := client.Complete(msg("x"), "strong", false, nil, "Translator")
	if err != nil || out != "ok" {
		t.Fatalf("complete = %q %v", out, err)
	}
	if stub.calls != 2 {
		t.Errorf("calls = %d", stub.calls)
	}
	if len(events) != 1 || events[0]["event"] != "llm_retry_wait" {
		t.Fatalf("events = %v", events)
	}
	e := events[0]
	if e["reason"] != "http_502" || e["failed_attempt"] != 1 || e["next_attempt"] != 2 ||
		e["wait_seconds"] != 0.0 || e["wait_source"] != "server" || e["stage"] == nil || *(e["stage"].(*string)) != "Translator" ||
		e["request_id"] != "req-test" {
		t.Errorf("event = %v", e)
	}
}

func TestRetryExhaustionRecordedAndReraisesLastError(t *testing.T) {
	client, _ := NewDeepSeekClient(minimalDeepSeekCfg(2))
	lastErr := &llm.HTTPError{StatusCode: 503, Headers: map[string]string{"retry-after-ms": "0"}}
	stub := &stubCaller{outcomes: []any{lastErr, lastErr, lastErr}}
	client.SetCallerForTesting(stub)
	var events []map[string]any
	client.SetEventSink(func(event string, data map[string]any) {
		d := map[string]any{"event": event}
		for k, v := range data {
			d[k] = v
		}
		events = append(events, d)
	})
	_, err := client.Complete(msg("x"), "strong", false, nil, "Analyzer")
	if err != lastErr {
		t.Fatalf("err = %v", err)
	}
	if stub.calls != 3 {
		t.Errorf("calls = %d", stub.calls)
	}
	wantEvents := []string{"llm_retry_wait", "llm_retry_wait", "llm_retry_exhausted"}
	for i, want := range wantEvents {
		if events[i]["event"] != want {
			t.Fatalf("events[%d] = %v, want %s", i, events[i], want)
		}
	}
	if events[2]["attempts"] != 3 {
		t.Errorf("attempts = %v", events[2]["attempts"])
	}
	if s, ok := events[2]["stage"].(*string); !ok || *s != "Analyzer" {
		t.Errorf("stage = %v", events[2]["stage"])
	}
}

func TestPermanentErrorIsNotRetriedOrReported(t *testing.T) {
	client, _ := NewDeepSeekClient(minimalDeepSeekCfg(4))
	err401 := &llm.HTTPError{StatusCode: 401}
	stub := &stubCaller{outcomes: []any{err401}}
	client.SetCallerForTesting(stub)
	var events []map[string]any
	client.SetEventSink(func(event string, data map[string]any) {
		events = append(events, map[string]any{"event": event})
	})
	if _, err := client.Complete(msg("x"), "strong", false, nil, ""); err != err401 {
		t.Fatalf("err = %v", err)
	}
	if stub.calls != 1 {
		t.Errorf("calls = %d", stub.calls)
	}
	if len(events) != 0 {
		t.Errorf("events = %v", events)
	}
}
