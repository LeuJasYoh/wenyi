// Package llmfactory 按 provider 名构造 client（主规格 §5.2）。
// 独立成包以避免 llm ↔ providers 的 import cycle。
package llmfactory

import (
	"fmt"
	"os"
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/fakellm"
	"wenyi/internal/llm"
	"wenyi/internal/llm/providers"
)

// BuildClient 按 config.llm.provider 构造 client；未知 provider 报错并列出全部支持值。
func BuildClient(cfg *config.Config) (llm.LLMClient, error) {
	provider := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(cfg.LLM.Provider)), "_", "-")
	switch provider {
	case "deepseek":
		return providers.NewDeepSeekClient(&cfg.LLM)
	case "openai":
		return providers.NewOpenAIClient(&cfg.LLM)
	case "openrouter":
		return providers.NewOpenRouterClient(&cfg.LLM)
	case "openai-compatible":
		return providers.NewOpenAICompatibleClient(&cfg.LLM)
	case "ollama":
		return providers.NewOllamaClient(&cfg.LLM)
	case "vllm":
		return providers.NewVLLMClient(&cfg.LLM)
	case "gemini", "google":
		return providers.NewGeminiClient(&cfg.LLM)
	case "fake":
		if os.Getenv("WENYI_FAKE_ROUTING") != "" {
			return llm.NewFakeClient(fakellm.RoutingHandler), nil
		}
		return llm.NewFakeClient(nil), nil
	default:
		return nil, fmt.Errorf(
			"不支持的 llm.provider：%s（支持：deepseek / openai / openrouter / openai-compatible / ollama / vllm / gemini|google / fake）",
			cfg.LLM.Provider,
		)
	}
}
