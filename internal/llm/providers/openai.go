package providers

// openai.go / openrouter.go / openai_compatible.go / ollama.go / vllm.go
// （主规格 §5.8 + 分册 02 §4–§6、§8–§9）。

import (
	"fmt"

	"wenyi/internal/config"
	"wenyi/internal/llm"
)

// ---- OpenAI 官方端点 ----

const OpenAIDefaultBaseURL = "https://api.openai.com/v1"
const OpenAIDefaultAPIKeyEnv = "OPENAI_API_KEY"

type OpenAITierOptions = commonOptions

func parseOpenAIOptions(name string, raw map[string]any) (OpenAITierOptions, error) {
	return parseCommonOptions(name, raw, true)
}

// BuildOpenAIRequestKwargs：reasoning_effort 总是发送（关闭时字面量 "none"）；
// 输出上限键 max_completion_tokens（思考时 4096 下限）。
func BuildOpenAIRequestKwargs(tier llm.ResolvedTier[OpenAITierOptions], messages []llm.Message, jsonMode bool, maxTokens *int) RequestKwargs {
	kwargs := BaseRequestKwargs(tier.Model, messages, jsonMode)
	opts := tier.Options
	effort := opts.ReasoningEffort
	if !opts.Thinking {
		effort = "none"
	}
	kwargs.ReasoningEffort = &effort
	if len(opts.ExtraBody) > 0 {
		kwargs.ExtraBody = DeepMerge(map[string]any{}, opts.ExtraBody)
	}
	if maxTokens != nil {
		limit := *maxTokens
		if opts.Thinking && limit < 4096 {
			limit = 4096
		}
		mct := limit
		kwargs.MaxCompletionTokens = &mct
	}
	return kwargs
}

// OpenAIClient OpenAI 官方端点（无内置默认档位）。
type OpenAIClient struct {
	CompatClient[OpenAITierOptions]
}

func NewOpenAIClient(cfg *config.LLMConfig) (*OpenAIClient, error) {
	tiers, err := ResolveProviderTiers(cfg.Tiers, nil, parseOpenAIOptions)
	if err != nil {
		return nil, err
	}
	c := &OpenAIClient{}
	if err := InitCompatClient(&c.CompatClient, cfg, "OpenAI", OpenAIDefaultBaseURL, OpenAIDefaultAPIKeyEnv, tiers, true); err != nil {
		return nil, err
	}
	c.BuildKwargs = BuildOpenAIRequestKwargs
	return c, nil
}

// ---- OpenRouter ----

const OpenRouterDefaultBaseURL = "https://openrouter.ai/api/v1"
const OpenRouterDefaultAPIKeyEnv = "OPENROUTER_API_KEY"

type OpenRouterTierOptions = commonOptions

func parseOpenRouterOptions(name string, raw map[string]any) (OpenRouterTierOptions, error) {
	return parseCommonOptions(name, raw, true)
}

// BuildOpenRouterRequestKwargs：extra_body.reasoning 方言。
func BuildOpenRouterRequestKwargs(tier llm.ResolvedTier[OpenRouterTierOptions], messages []llm.Message, jsonMode bool, maxTokens *int) RequestKwargs {
	kwargs := BaseRequestKwargs(tier.Model, messages, jsonMode)
	opts := tier.Options
	var reasoning map[string]any
	if opts.Thinking {
		reasoning = map[string]any{"effort": opts.ReasoningEffort}
	} else {
		reasoning = map[string]any{"enabled": false}
	}
	extraBody := map[string]any{"reasoning": reasoning}
	if len(opts.ExtraBody) > 0 {
		extraBody = DeepMerge(extraBody, opts.ExtraBody)
	}
	kwargs.ExtraBody = extraBody
	if maxTokens != nil {
		limit := *maxTokens
		if opts.Thinking && limit < 4096 {
			limit = 4096
		}
		mt := limit
		kwargs.MaxTokens = &mt
	}
	return kwargs
}

// OpenRouterClient OpenRouter 端点（无内置默认档位）。
type OpenRouterClient struct {
	CompatClient[OpenRouterTierOptions]
}

func NewOpenRouterClient(cfg *config.LLMConfig) (*OpenRouterClient, error) {
	tiers, err := ResolveProviderTiers(cfg.Tiers, nil, parseOpenRouterOptions)
	if err != nil {
		return nil, err
	}
	c := &OpenRouterClient{}
	if err := InitCompatClient(&c.CompatClient, cfg, "OpenRouter", OpenRouterDefaultBaseURL, OpenRouterDefaultAPIKeyEnv, tiers, true); err != nil {
		return nil, err
	}
	c.BuildKwargs = BuildOpenRouterRequestKwargs
	return c, nil
}

// ---- 通用 OpenAI 兼容端点（reasoning_style 方言 + request_overrides） ----

// OpenAICompatibleTierOptions：thinking 默认 false；request_overrides 透传。
type OpenAICompatibleTierOptions struct {
	Thinking          bool
	ReasoningEffort   string
	RequestOverrides  map[string]any
}

func parseOpenAICompatibleOptions(name string, raw map[string]any) (OpenAICompatibleTierOptions, error) {
	opts := OpenAICompatibleTierOptions{Thinking: false, ReasoningEffort: "high", RequestOverrides: map[string]any{}}
	for k, v := range raw {
		switch k {
		case "thinking":
			opts.Thinking = pyBool(v)
		case "reasoning_effort":
			if s, ok := v.(string); ok {
				opts.ReasoningEffort = s
			}
		case "request_overrides":
			if m, ok := v.(map[string]any); ok {
				opts.RequestOverrides = m
			} else {
				return opts, fmt.Errorf("llm.tiers.%s.options.request_overrides 必须是对象", name)
			}
		default:
			return opts, fmt.Errorf("llm.tiers.%s.options 含未知键 %q", name, k)
		}
	}
	return opts, nil
}

// reasoningBody 按 reasoning_style 方言转换（分册 02 §6.2 表）。
func reasoningBody(options OpenAICompatibleTierOptions, style string) (reasoningKwargs map[string]any, extraBody map[string]any) {
	reasoningKwargs = map[string]any{}
	extraBody = map[string]any{}
	switch style {
	case "deepseek":
		if options.Thinking {
			extraBody["thinking"] = map[string]any{"type": "enabled"}
			reasoningKwargs["reasoning_effort"] = options.ReasoningEffort
		} else {
			extraBody["thinking"] = map[string]any{"type": "disabled"}
		}
	case "openai":
		if options.Thinking {
			reasoningKwargs["reasoning_effort"] = options.ReasoningEffort
		} else {
			reasoningKwargs["reasoning_effort"] = "none"
		}
	case "openrouter":
		if options.Thinking {
			extraBody["reasoning"] = map[string]any{"effort": options.ReasoningEffort}
		} else {
			extraBody["reasoning"] = map[string]any{"enabled": false}
		}
	default:
		// "none"：什么都不加
	}
	return
}

// BuildOpenAICompatibleRequestKwargs（reasoning_style 可注入，测试用）。
func BuildOpenAICompatibleRequestKwargs(tier llm.ResolvedTier[OpenAICompatibleTierOptions], messages []llm.Message, jsonMode bool, maxTokens *int, reasoningStyle string) RequestKwargs {
	kwargs := BaseRequestKwargs(tier.Model, messages, jsonMode)
	opts := tier.Options
	reasoningKwargs, extraBody := reasoningBody(opts, reasoningStyle)
	if v, ok := reasoningKwargs["reasoning_effort"].(string); ok {
		effort := v
		kwargs.ReasoningEffort = &effort
	}
	if len(opts.RequestOverrides) > 0 {
		extraBody = DeepMerge(extraBody, opts.RequestOverrides)
	}
	if len(extraBody) > 0 {
		kwargs.ExtraBody = extraBody
	}
	if maxTokens != nil {
		limit := *maxTokens
		if opts.Thinking && limit < 4096 {
			limit = 4096
		}
		mt := limit
		kwargs.MaxTokens = &mt
	}
	return kwargs
}

// OpenAICompatibleClient 任意兼容端点。
type OpenAICompatibleClient struct {
	CompatClient[OpenAICompatibleTierOptions]
	ReasoningStyle string
}

func NewOpenAICompatibleClientWithParams(cfg *config.LLMConfig, providerName string, defaultBaseURL, defaultAPIKeyEnv string, requiresAPIKey bool) (*OpenAICompatibleClient, error) {
	tiers, err := ResolveProviderTiers(cfg.Tiers, nil, parseOpenAICompatibleOptions)
	if err != nil {
		return nil, err
	}
	c := &OpenAICompatibleClient{ReasoningStyle: cfg.ReasoningStyle}
	if err := InitCompatClient(&c.CompatClient, cfg, providerName, defaultBaseURL, defaultAPIKeyEnv, tiers, requiresAPIKey); err != nil {
		return nil, err
	}
	style := c.ReasoningStyle
	c.BuildKwargs = func(tier llm.ResolvedTier[OpenAICompatibleTierOptions], messages []llm.Message, jsonMode bool, maxTokens *int) RequestKwargs {
		return BuildOpenAICompatibleRequestKwargs(tier, messages, jsonMode, maxTokens, style)
	}
	return c, nil
}

func NewOpenAICompatibleClient(cfg *config.LLMConfig) (*OpenAICompatibleClient, error) {
	return NewOpenAICompatibleClientWithParams(cfg, "OpenAI-compatible", "", "", false)
}

// ---- Ollama / vLLM（薄子类） ----

const OllamaDefaultBaseURL = "http://localhost:11434/v1"
const VLLMDefaultBaseURL = "http://localhost:8000/v1"

type OllamaClient struct {
	*OpenAICompatibleClient
}

func NewOllamaClient(cfg *config.LLMConfig) (*OllamaClient, error) {
	c, err := NewOpenAICompatibleClientWithParams(cfg, "Ollama", OllamaDefaultBaseURL, "", false)
	if err != nil {
		return nil, err
	}
	return &OllamaClient{OpenAICompatibleClient: c}, nil
}

type VLLMClient struct {
	*OpenAICompatibleClient
}

func NewVLLMClient(cfg *config.LLMConfig) (*VLLMClient, error) {
	c, err := NewOpenAICompatibleClientWithParams(cfg, "vLLM", VLLMDefaultBaseURL, "", false)
	if err != nil {
		return nil, err
	}
	return &VLLMClient{OpenAICompatibleClient: c}, nil
}
