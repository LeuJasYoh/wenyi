package llmfactory

import (
	"strings"
	"testing"

	"wenyi/internal/config"
	"wenyi/internal/llm"
	"wenyi/internal/llm/providers"
)

// 迁移自 tests/test_llm.py::TestProviderFactory 与 test_llm_gemini.py::test_factory_build_client_gemini。

func factoryCfg(provider string, mutate func(m map[string]any)) *config.Config {
	llm := map[string]any{
		"provider": provider,
		"tiers":    map[string]any{"strong": map[string]any{"model": "m"}},
	}
	if mutate != nil {
		mutate(llm)
	}
	cfg, err := config.FromDict(map[string]any{"llm": llm})
	if err != nil {
		panic(err)
	}
	return cfg
}

func TestBuildsEachProviderFromItsOwnModule(t *testing.T) {
	cases := []struct {
		provider string
		mutate   func(m map[string]any)
		check    func(llm.LLMClient) bool
	}{
		{"openai", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.OpenAIClient); return ok }},
		{"openrouter", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.OpenRouterClient); return ok }},
		{"openai-compatible", func(m map[string]any) { m["base_url"] = "https://example.test/v1" }, func(c llm.LLMClient) bool { _, ok := c.(*providers.OpenAICompatibleClient); return ok }},
		{"ollama", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.OllamaClient); return ok }},
		{"vllm", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.VLLMClient); return ok }},
		{"deepseek", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.DeepSeekClient); return ok }},
		{"gemini", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.GeminiClient); return ok }},
		{"google", nil, func(c llm.LLMClient) bool { _, ok := c.(*providers.GeminiClient); return ok }},
	}
	for _, tc := range cases {
		client, err := BuildClient(factoryCfg(tc.provider, tc.mutate))
		if err != nil {
			t.Errorf("%s: %v", tc.provider, err)
			continue
		}
		if !tc.check(client) {
			t.Errorf("%s: 类型不符 %T", tc.provider, client)
		}
	}
}

func TestLocalProviderDefaults(t *testing.T) {
	ollama, err := BuildClient(factoryCfg("ollama", nil))
	if err != nil {
		t.Fatal(err)
	}
	vllm, err := BuildClient(factoryCfg("vllm", nil))
	if err != nil {
		t.Fatal(err)
	}
	oc := ollama.(*providers.OllamaClient)
	vc := vllm.(*providers.VLLMClient)
	if oc.BaseURL != "http://localhost:11434/v1" || vc.BaseURL != "http://localhost:8000/v1" {
		t.Errorf("base urls = %s %s", oc.BaseURL, vc.BaseURL)
	}
	if oc.RequiresAPIKey || vc.RequiresAPIKey {
		t.Errorf("本地端点不应要求 API key")
	}
	t.Setenv("OLLAMA_API_KEY", "")
	if err := oc.ValidateCredentials(); err != nil {
		t.Errorf("ollama validate: %v", err)
	}
	if err := vc.ValidateCredentials(); err != nil {
		t.Errorf("vllm validate: %v", err)
	}
}

func TestRemoteProviderValidatesAPIKeyBeforeRequest(t *testing.T) {
	client, err := BuildClient(factoryCfg("deepseek", nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "")
	if err := client.ValidateCredentials(); err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	if err := client.ValidateCredentials(); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestGenericProviderRequiresBaseURL(t *testing.T) {
	_, err := BuildClient(factoryCfg("openai-compatible", nil))
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("err = %v", err)
	}
}

func TestCompatibleClientsUseConfiguredReasoningStyle(t *testing.T) {
	compatible, err := BuildClient(factoryCfg("openai-compatible", func(m map[string]any) {
		m["base_url"] = "https://example.test/v1"
		m["reasoning_style"] = "deepseek"
	}))
	if err != nil {
		t.Fatal(err)
	}
	ollama, err := BuildClient(factoryCfg("ollama", func(m map[string]any) { m["reasoning_style"] = "openai" }))
	if err != nil {
		t.Fatal(err)
	}
	vllm, err := BuildClient(factoryCfg("vllm", func(m map[string]any) { m["reasoning_style"] = "openrouter" }))
	if err != nil {
		t.Fatal(err)
	}
	if compatible.(*providers.OpenAICompatibleClient).ReasoningStyle != "deepseek" {
		t.Errorf("compatible style")
	}
	if ollama.(*providers.OllamaClient).ReasoningStyle != "openai" {
		t.Errorf("ollama style")
	}
	if vllm.(*providers.VLLMClient).ReasoningStyle != "openrouter" {
		t.Errorf("vllm style")
	}
}

func TestUnknownProviderRejected(t *testing.T) {
	_, err := BuildClient(factoryCfg("bogus", nil))
	if err == nil {
		t.Errorf("未知 provider 应报错")
	}
}
