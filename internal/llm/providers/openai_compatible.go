// Package providers 实现 8 个 LLM provider（主规格 §5.7–§5.9 + 分册 02）。
package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"wenyi/internal/config"
	"wenyi/internal/llm"
)

const jsonModeInstruction = "Output must be valid json."

// RequestKwargs 是 Chat Completions 请求参数（等价 Python 侧 kwargs dict）。
// ExtraBody 在真正发送时合并到请求体顶层（OpenAI SDK extra_body 语义）。
type RequestKwargs struct {
	Model               string
	Messages            []llm.Message
	Stream              bool
	ResponseFormat      map[string]string // nil = 不发送
	ReasoningEffort     *string           // nil = 不发送
	MaxTokens           *int              // nil = 不发送
	MaxCompletionTokens *int              // nil = 不发送
	ExtraBody           map[string]any
}

// ChatResponse 是 chat.completions 响应的最小投影。
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage map[string]any `json:"usage"`
}

// Content 返回 choices[0].message.content or ""。
func (r *ChatResponse) Content() string {
	if len(r.Choices) == 0 {
		return ""
	}
	if r.Choices[0].Message.Content == nil {
		return ""
	}
	return *r.Choices[0].Message.Content
}

// ChatCaller 发起一次 chat.completions 调用（openai SDK 等价；测试可注入 stub）。
type ChatCaller interface {
	Create(kwargs RequestKwargs) (*ChatResponse, error)
}

// BaseRequestKwargs 构造基础参数（model/messages/stream）并为 JSON 模式补充指令；
// 不修改调用方传入的 messages（主规格 §5.7）。
func BaseRequestKwargs(model string, messages []llm.Message, jsonMode bool) RequestKwargs {
	requestMessages := messages
	if jsonMode {
		requestMessages = make([]llm.Message, len(messages))
		copy(requestMessages, messages)
		foundSystem := false
		for i := range requestMessages {
			if requestMessages[i].Role == "system" {
				requestMessages[i].Content = requestMessages[i].Content + "\n\n" + jsonModeInstruction
				foundSystem = true
				break
			}
		}
		if !foundSystem {
			requestMessages = append([]llm.Message{{Role: "system", Content: jsonModeInstruction}}, requestMessages...)
		}
		// 最后一条 user 消息兜底补 "json" 字样（兼容只校验 user 内容的网关）
		for i := len(requestMessages) - 1; i >= 0; i-- {
			if requestMessages[i].Role == "user" {
				if !strings.Contains(strings.ToLower(requestMessages[i].Content), "json") {
					requestMessages[i].Content = requestMessages[i].Content + "\n\n" + jsonModeInstruction
				}
				break
			}
		}
	}
	kwargs := RequestKwargs{Model: model, Messages: requestMessages, Stream: false}
	if jsonMode {
		kwargs.ResponseFormat = map[string]string{"type": "json_object"}
	}
	return kwargs
}

// DeepMerge 递归合并，用户值优先（主规格 §5.7）。
func DeepMerge(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		bm, bIsMap := merged[k].(map[string]any)
		vm, vIsMap := v.(map[string]any)
		if bIsMap && vIsMap {
			merged[k] = DeepMerge(bm, vm)
		} else {
			merged[k] = v
		}
	}
	return merged
}

// NormalizeOpenAIUsage 从嵌套 prompt_tokens_details.cached_tokens 读缓存命中。
func NormalizeOpenAIUsage(usage map[string]any) *llm.UsageSample {
	if usage == nil {
		return nil
	}
	details, _ := readUsageValue(usage, "prompt_tokens_details").(map[string]any)
	if details == nil || readUsageValue(details, "cached_tokens") == nil {
		return llm.MakeUsageSample(usage, 0, 0)
	}
	cached := readUsageInt(details, "cached_tokens")
	miss := readUsageInt(usage, "prompt_tokens") - cached
	if miss < 0 {
		miss = 0
	}
	return llm.MakeUsageSample(usage, cached, miss)
}

func readUsageInt(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch t := m[key].(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}

func readUsageValue(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

// tierDefault 是 provider 内置默认档位。
type tierDefault struct {
	model   string
	options map[string]any
}

// ResolveProviderTiers 合并用户覆盖与内置默认，用 provider 专属 parse 校验（主规格 §5.7）。
func ResolveProviderTiers[O any](
	overrides map[string]*config.TierConfig,
	defaults map[string]tierDefault,
	parse func(name string, raw map[string]any) (O, error),
) (map[string]llm.ResolvedTier[O], error) {
	tiers := make(map[string]llm.ResolvedTier[O])
	for name, d := range defaults {
		opts, err := parse(name, copyMap(d.options))
		if err != nil {
			return nil, err
		}
		tiers[name] = llm.ResolvedTier[O]{Model: d.model, Options: opts}
	}
	for name, override := range overrides {
		cur := defaults[name]
		model := ""
		if override.Model != nil {
			model = *override.Model
		} else if cur.model != "" {
			model = cur.model
		}
		if model == "" {
			return nil, fmt.Errorf("llm.tiers.%s.model 不能为空", name)
		}
		optionValues := copyMap(cur.options)
		for k, v := range override.Options {
			optionValues[k] = v
		}
		opts, err := parse(name, optionValues)
		if err != nil {
			return nil, err
		}
		tiers[name] = llm.ResolvedTier[O]{Model: model, Options: opts}
	}
	if _, ok := tiers["strong"]; !ok {
		return nil, fmt.Errorf("配置缺少 llm.tiers.strong.model")
	}
	return tiers, nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// options 解析辅助：thinking/reasoning_effort/extra_body 族（extra="forbid"）。
type commonOptions struct {
	Thinking        bool
	ReasoningEffort string
	ExtraBody       map[string]any
}

func parseCommonOptions(name string, raw map[string]any, defaultThinking bool) (commonOptions, error) {
	opts := commonOptions{Thinking: defaultThinking, ReasoningEffort: "high", ExtraBody: map[string]any{}}
	for k, v := range raw {
		switch k {
		case "thinking":
			opts.Thinking = pyBool(v)
		case "reasoning_effort":
			if s, ok := v.(string); ok {
				opts.ReasoningEffort = s
			} else {
				opts.ReasoningEffort = fmt.Sprintf("%v", v)
			}
		case "extra_body":
			if m, ok := v.(map[string]any); ok {
				opts.ExtraBody = m
			} else {
				return opts, fmt.Errorf("llm.tiers.%s.options.extra_body 必须是对象", name)
			}
		default:
			return opts, fmt.Errorf("llm.tiers.%s.options 含未知键 %q", name, k)
		}
	}
	return opts, nil
}

func pyBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case nil:
		return false
	}
	return true
}

// ---- HTTP caller（openai SDK 等价物）----

type httpChatCaller struct {
	baseURL string
	apiKey  string
	timeout time.Duration
	client  *http.Client
}

func newHTTPChatCaller(baseURL, apiKey string, timeoutSec int) *httpChatCaller {
	return &httpChatCaller{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		timeout: time.Duration(timeoutSec) * time.Second,
		client:  &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

func (h *httpChatCaller) Create(kwargs RequestKwargs) (*ChatResponse, error) {
	body := map[string]any{
		"model":    kwargs.Model,
		"messages": kwargs.Messages,
		"stream":   kwargs.Stream,
	}
	if kwargs.ResponseFormat != nil {
		body["response_format"] = kwargs.ResponseFormat
	}
	if kwargs.ReasoningEffort != nil {
		body["reasoning_effort"] = *kwargs.ReasoningEffort
	}
	if kwargs.MaxTokens != nil {
		body["max_tokens"] = *kwargs.MaxTokens
	}
	if kwargs.MaxCompletionTokens != nil {
		body["max_completion_tokens"] = *kwargs.MaxCompletionTokens
	}
	for k, v := range kwargs.ExtraBody {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		headers := map[string]string{}
		for k, v := range resp.Header {
			if len(v) > 0 {
				headers[strings.ToLower(k)] = v[0]
			}
		}
		return nil, &llm.HTTPError{
			StatusCode: resp.StatusCode,
			Headers:    headers,
			RequestID:  headers["x-request-id"],
		}
	}
	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func classifyTransportError(err error) error {
	if ne, ok := err.(netTimeout); ok && ne.Timeout() {
		return &llm.TransportError{Kind: "timeout", Msg: err.Error()}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unsupported protocol scheme"), strings.Contains(msg, "invalid URL"):
		return &llm.TransportError{Kind: "invalid_url", Msg: msg}
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "certificate"):
		return &llm.TransportError{Kind: "ssl", Msg: msg}
	default:
		return &llm.TransportError{Kind: "connection", Msg: msg}
	}
}

type netTimeout interface {
	error
	Timeout() bool
}

// ---- 兼容基类 ----

// CompatClient 是全部 OpenAI Chat Completions 兼容 provider 的共用客户端。
type CompatClient[O any] struct {
	*llm.Core
	cfg            *config.LLMConfig
	ProviderName   string
	BaseURL        string
	APIKey         string // 配置直存密钥（llm.api_key；优先于环境变量）
	APIKeyEnv      string
	Tiers          map[string]llm.ResolvedTier[O]
	RequiresAPIKey bool
	NormalizeUsage func(usage map[string]any) *llm.UsageSample
	BuildKwargs    func(tier llm.ResolvedTier[O], messages []llm.Message, jsonMode bool, maxTokens *int) RequestKwargs

	caller   ChatCaller
	callerMu sync.Mutex
}

// InitCompatClient 初始化兼容基类（构造函数共用部分）。
func InitCompatClient[O any](c *CompatClient[O], cfg *config.LLMConfig,
	providerName string, defaultBaseURL, defaultAPIKeyEnv string,
	tiers map[string]llm.ResolvedTier[O], requiresAPIKey bool) error {
	c.Core = llm.NewCore()
	c.cfg = cfg
	c.ProviderName = providerName
	c.BaseURL = ""
	if cfg.BaseURL != nil {
		c.BaseURL = *cfg.BaseURL
	}
	if c.BaseURL == "" && defaultBaseURL != "" {
		c.BaseURL = defaultBaseURL
	}
	if cfg.APIKey != nil {
		c.APIKey = strings.TrimSpace(*cfg.APIKey)
	}
	c.APIKeyEnv = ""
	if cfg.APIKeyEnv != nil {
		c.APIKeyEnv = *cfg.APIKeyEnv
	}
	if c.APIKeyEnv == "" && defaultAPIKeyEnv != "" {
		c.APIKeyEnv = defaultAPIKeyEnv
	}
	if c.BaseURL == "" {
		return fmt.Errorf("%s provider 需要配置 llm.base_url", providerName)
	}
	c.Tiers = tiers
	c.RequiresAPIKey = requiresAPIKey
	c.NormalizeUsage = NormalizeOpenAIUsage
	return nil
}

// ValidateCredentials 凭据校验（主规格 §5.7）：llm.api_key 直存优先，其次环境变量。
func (c *CompatClient[O]) ValidateCredentials() error {
	if c.APIKey != "" {
		return nil
	}
	if c.APIKeyEnv == "" {
		if c.RequiresAPIKey {
			return fmt.Errorf("%s provider 需要配置 llm.api_key 或 llm.api_key_env", c.ProviderName)
		}
		return nil
	}
	key := strings.TrimSpace(os.Getenv(c.APIKeyEnv))
	if key == "" {
		return fmt.Errorf("未设置环境变量 %s（%s API key），或在设置页填写 API Key", c.APIKeyEnv, c.ProviderName)
	}
	return nil
}

// SetCallerForTesting 注入 stub（等价 Python client._client = stub）。
func (c *CompatClient[O]) SetCallerForTesting(caller ChatCaller) {
	c.callerMu.Lock()
	defer c.callerMu.Unlock()
	c.caller = caller
}

func (c *CompatClient[O]) ensureCaller() (ChatCaller, error) {
	c.callerMu.Lock()
	defer c.callerMu.Unlock()
	if c.caller != nil {
		return c.caller, nil
	}
	if err := c.ValidateCredentials(); err != nil {
		return nil, err
	}
	apiKey := c.APIKey
	if apiKey == "" && c.APIKeyEnv != "" {
		apiKey = os.Getenv(c.APIKeyEnv)
	}
	if apiKey == "" {
		apiKey = "no-key"
	}
	c.caller = newHTTPChatCaller(c.BaseURL, apiKey, c.cfg.Timeout)
	return c.caller, nil
}

var sharedRand = rand.New(rand.NewSource(time.Now().UnixNano()))

// Complete 唯一公共调用入口（非流式，带统一重试与用量记录）。
func (c *CompatClient[O]) Complete(messages []llm.Message, tier string, jsonMode bool, maxTokens *int, stage string) (string, error) {
	tierConfig := llm.ResolveTier(c.Tiers, tier)
	kwargs := c.BuildKwargs(tierConfig, messages, jsonMode, maxTokens)
	caller, err := c.ensureCaller()
	if err != nil {
		return "", err
	}
	var stagePtr *string
	if stage != "" {
		s := stage
		stagePtr = &s
	}
	maxAttempts := c.cfg.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	reporter := &llm.RetryReporter{
		Provider: c.ProviderName, Tier: tier, Stage: stagePtr, MaxAttempts: maxAttempts,
		Emit: c.EmitEvent,
	}
	var response *ChatResponse
	callErr := llm.CallWithRetry(c.cfg.MaxRetries, reporter, sharedRand, func() error {
		resp, err := caller.Create(kwargs)
		if err != nil {
			return err
		}
		response = resp
		return nil
	})
	if callErr != nil {
		return "", callErr
	}
	sample := c.NormalizeUsage(response.Usage)
	c.Usage.Record(tier, sample, stage)
	return response.Content(), nil
}

// CompleteJSON = Complete(json_mode=true) 后 ParseJSONLoose。
func (c *CompatClient[O]) CompleteJSON(messages []llm.Message, tier string, maxTokens *int, stage string) (any, error) {
	text, err := c.Complete(messages, tier, true, maxTokens, stage)
	if err != nil {
		return nil, err
	}
	return llm.ParseJSONLoose(text)
}

// ensure io import used（httpChatCaller decode 使用）
var _ = io.EOF
