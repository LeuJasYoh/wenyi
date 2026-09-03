package providers

// deepseek.go（主规格 §5.8 + 分册 02 §3）。

import (
	"fmt"

	"wenyi/internal/config"
	"wenyi/internal/llm"
)

const DeepSeekDefaultBaseURL = "https://api.deepseek.com"
const DeepSeekDefaultAPIKeyEnv = "DEEPSEEK_API_KEY"

// DeepSeekTierOptions：thinking/reasoning_effort/extra_body（extra=forbid）。
type DeepSeekTierOptions = commonOptions

func parseDeepSeekOptions(name string, raw map[string]any) (DeepSeekTierOptions, error) {
	return parseCommonOptions(name, raw, true)
}

// NormalizeDeepSeekUsage：DeepSeek 缓存字段在 usage 顶层。
func NormalizeDeepSeekUsage(usage map[string]any) *llm.UsageSample {
	if usage == nil {
		return nil
	}
	hit := readUsageInt(usage, "prompt_cache_hit_tokens")
	miss := readUsageInt(usage, "prompt_cache_miss_tokens")
	return llm.MakeUsageSample(usage, hit, miss)
}

func deepSeekDefaultTiers() map[string]tierDefault {
	return map[string]tierDefault{
		"strong": {model: "deepseek-v4-pro"},
		"cheap":  {model: "deepseek-v4-flash"},
		"fast":   {model: "deepseek-v4-flash", options: map[string]any{"thinking": false}},
	}
}

// BuildDeepSeekRequestKwargs 思考方言：extra_body.thinking.type + reasoning_effort（仅思考时）。
func BuildDeepSeekRequestKwargs(tier llm.ResolvedTier[DeepSeekTierOptions], messages []llm.Message, jsonMode bool, maxTokens *int) RequestKwargs {
	kwargs := BaseRequestKwargs(tier.Model, messages, jsonMode)
	opts := tier.Options
	typeName := "disabled"
	if opts.Thinking {
		typeName = "enabled"
	}
	extraBody := map[string]any{"thinking": map[string]any{"type": typeName}}
	if opts.Thinking {
		effort := opts.ReasoningEffort
		kwargs.ReasoningEffort = &effort
	}
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

// DeepSeekClient DeepSeek 原生兼容端点。
type DeepSeekClient struct {
	CompatClient[DeepSeekTierOptions]
}

// NewDeepSeekClient 构造（内置三档默认）。
func NewDeepSeekClient(cfg *config.LLMConfig) (*DeepSeekClient, error) {
	tiers, err := ResolveProviderTiers(cfg.Tiers, deepSeekDefaultTiers(), parseDeepSeekOptions)
	if err != nil {
		return nil, err
	}
	c := &DeepSeekClient{}
	if err := InitCompatClient(&c.CompatClient, cfg, "DeepSeek", DeepSeekDefaultBaseURL, DeepSeekDefaultAPIKeyEnv, tiers, true); err != nil {
		return nil, err
	}
	c.NormalizeUsage = NormalizeDeepSeekUsage
	c.BuildKwargs = BuildDeepSeekRequestKwargs
	return c, nil
}

var _ = fmt.Sprintf // 保留 fmt 引用
