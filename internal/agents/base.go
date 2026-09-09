package agents

import (
	"strings"

	"wenyi/internal/config"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

// Agent 基类（主规格 §9.1 / 分册 05 §2）。
// StageName 等价 Python type(self).__name__，用于用量按阶段归因。
type Agent struct {
	Client    llm.LLMClient
	Config    *config.Config
	Src       string
	Tgt       string
	StageName string
}

// NewAgent 构造基类。
func NewAgent(client llm.LLMClient, cfg *config.Config, stageName string) Agent {
	return Agent{Client: client, Config: cfg, Src: cfg.SourceLang, Tgt: cfg.TargetLang, StageName: stageName}
}

// AskJSON 对应 _ask_json（无 default，异常照常抛）。
// key 非空时从 dict 取 data[key]（缺失回退 nil）；非 dict 且非空（裸列表）直接用。
func (a *Agent) AskJSON(system, user, tier, key string, maxTokens *int) (any, error) {
	return a.askJSON(system, user, tier, key, maxTokens, false, nil)
}

// AskJSONDefault 对应带 default 的 _ask_json（异常返回 default）。
func (a *Agent) AskJSONDefault(system, user, tier, key string, maxTokens *int, def any) (any, error) {
	return a.askJSON(system, user, tier, key, maxTokens, true, def)
}

func (a *Agent) askJSON(system, user, tier, key string, maxTokens *int, hasDefault bool, def any) (any, error) {
	// pipeline.stage_tiers 环节档位覆盖（未配置时返回原档位）
	tier = a.Config.Pipeline.TierFor(a.StageName, tier)
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	data, err := a.Client.CompleteJSON(messages, tier, maxTokens, a.StageName)
	if err != nil {
		if hasDefault {
			return def, nil
		}
		return nil, err
	}
	if key == "" {
		return data, nil
	}
	var fb any
	if hasDefault {
		fb = def
	}
	if om, ok := data.(*jsonx.OMap); ok {
		v, found := om.Get(key)
		if !found {
			return fb, nil
		}
		return v, nil
	}
	if pyTruthy(data) {
		return data, nil
	}
	return fb, nil
}

// AskText 对应 _ask_text：异常回退 default（默认 ""），返回 strip 后文本。
func (a *Agent) AskText(system, user, tier string, maxTokens *int, def string) string {
	tier = a.Config.Pipeline.TierFor(a.StageName, tier)
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	res, err := a.Client.Complete(messages, tier, false, maxTokens, a.StageName)
	if err != nil {
		return def
	}
	return strings.TrimSpace(res)
}

// pyTruthy 模拟 Python 真值判断（用于 data if data else fb）。
func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case []any:
		return len(t) > 0
	case string:
		return len(t) > 0
	case bool:
		return t
	case int64:
		return t != 0
	case float64:
		return t != 0
	default:
		return true
	}
}

// DictItems 对应 Agent.dict_items：过滤非 dict 元素，None/空 → 空切片。
func DictItems(items any) []*jsonx.OMap {
	out := []*jsonx.OMap{}
	arr, ok := items.([]any)
	if !ok {
		return out
	}
	for _, it := range arr {
		if om, ok := it.(*jsonx.OMap); ok {
			out = append(out, om)
		}
	}
	return out
}

// SetSrc 更新源语言（orchestrator._apply_language 同步 8 个 agent 的 .src）。
func (a *Agent) SetSrc(src string) { a.Src = src }
