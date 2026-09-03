// Package llm 是统一 LLM 抽象层（主规格 §5）。
package llm

import "sync"

// Message 是一条对话消息（role: system|user|assistant）。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// EventSink 接收重试等事件；失败仅记日志，不得掩盖模型异常。
type EventSink func(event string, data map[string]any)

// LLMClient 是全部 provider 的公共接口（主规格 §5.1）。
type LLMClient interface {
	// Complete 单次补全。tier 三档 strong/cheap/fast；jsonMode 追加 JSON 模式提示；
	// maxTokens 为 nil 时不限制；stage 用于用量按阶段归因。
	Complete(messages []Message, tier string, jsonMode bool, maxTokens *int, stage string) (string, error)
	// CompleteJSON = Complete(jsonMode=true) 后 ParseJSONLoose。
	CompleteJSON(messages []Message, tier string, maxTokens *int, stage string) (any, error)
	// SetEventSink 线程安全地设置事件接收器。
	SetEventSink(sink EventSink)
	// UsageSummary 返回 {totals, by_tier, by_stage}。
	UsageSummary() map[string]any
	// ValidateCredentials 默认免检；需要密钥的 provider 覆写。
	ValidateCredentials() error
}

// base 提供各 client 共享的事件 sink 与用量管理（导出以供 providers 包内嵌）。
type Core struct {
	mu        sync.Mutex
	eventSink EventSink
	Usage     UsageTracker
}

func NewCore() *Core { return &Core{} }

func (b *Core) SetEventSink(sink EventSink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eventSink = sink
}

func (b *Core) EmitEvent(event string, data map[string]any) {
	b.mu.Lock()
	sink := b.eventSink
	b.mu.Unlock()
	if sink == nil {
		return
	}
	defer func() { _ = recover() }()
	sink(event, data)
}

func (b *Core) UsageSummary() map[string]any {
	return b.Usage.Summary()
}

func (b *Core) ValidateCredentials() error { return nil }
