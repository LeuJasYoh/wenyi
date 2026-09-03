package llm

// FakeHandler 是测试注入的确定性路由函数（对应 FakeClient(handler)）。
type FakeHandler func(messages []Message, tier string, jsonMode bool) (string, error)

// FakeClient 离线测试用（主规格 §5.10）：记录调用快照（messages/tier/json_mode/max_tokens/stage）；
// 有 handler 则返回 handler(messages, tier, jsonMode)，否则 json_mode 返回 "[]"、普通返回 ""。
type FakeClient struct {
	*Core
	Handler FakeHandler
	Calls   []map[string]any
}

func NewFakeClient(handler FakeHandler) *FakeClient {
	return &FakeClient{Core: NewCore(), Handler: handler}
}

func (f *FakeClient) Complete(messages []Message, tier string, jsonMode bool, maxTokens *int, stage string) (string, error) {
	snapshot := make([]Message, len(messages))
	copy(snapshot, messages)
	var mt any
	if maxTokens != nil {
		mt = *maxTokens
	}
	var st any
	if stage != "" {
		st = stage
	}
	f.Calls = append(f.Calls, map[string]any{
		"messages":   snapshot,
		"tier":       tier,
		"json_mode":  jsonMode,
		"max_tokens": mt,
		"stage":      st,
	})
	if f.Handler != nil {
		return f.Handler(messages, tier, jsonMode)
	}
	if jsonMode {
		return "[]", nil
	}
	return "", nil
}

func (f *FakeClient) CompleteJSON(messages []Message, tier string, maxTokens *int, stage string) (any, error) {
	text, err := f.Complete(messages, tier, true, maxTokens, stage)
	if err != nil {
		return nil, err
	}
	return ParseJSONLoose(text)
}
