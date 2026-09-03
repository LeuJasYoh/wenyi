package providers

// gemini.go（主规格 §5.9 + 分册 02 §7）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"wenyi/internal/config"
	"wenyi/internal/llm"
)

const GeminiDefaultAPIKeyEnv = "GEMINI_API_KEY"
const GeminiFallbackAPIKeyEnv = "GOOGLE_API_KEY"

// GeminiTierOptions 与 OpenAI 风格完全不同：thinking_level/budget 互斥。
type GeminiTierOptions struct {
	ThinkingLevel   *string
	ThinkingBudget  *int
	Temperature     *float64
	MaxOutputTokens *int
	ExtraBody       map[string]any
}

func parseGeminiOptions(name string, raw map[string]any) (GeminiTierOptions, error) {
	opts := GeminiTierOptions{ExtraBody: map[string]any{}}
	for k, v := range raw {
		switch k {
		case "thinking_level":
			if s, ok := v.(string); ok {
				opts.ThinkingLevel = &s
			}
		case "thinking_budget":
			if f, ok := toInt(v); ok {
				opts.ThinkingBudget = &f
			}
		case "temperature":
			if f, ok := v.(float64); ok {
				opts.Temperature = &f
			}
		case "max_output_tokens":
			if f, ok := toInt(v); ok {
				opts.MaxOutputTokens = &f
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
	if opts.ThinkingLevel != nil && opts.ThinkingBudget != nil {
		return opts, fmt.Errorf("thinking_level 与 thinking_budget 互斥，不能同时设置")
	}
	return opts, nil
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	}
	return 0, false
}

func geminiDefaultTiers() map[string]tierDefault {
	return map[string]tierDefault{
		"strong": {model: "gemini-3.6-flash"},
		"cheap":  {model: "gemini-3.6-flash"},
		"fast":   {model: "gemini-3.6-flash"},
	}
}

// ConvertMessagesToGemini 转成 system_instruction + contents（多条 system 以 \n\n 连接）。
func ConvertMessagesToGemini(messages []llm.Message) (*string, []map[string]any) {
	var systemParts []string
	var contents []map[string]any
	for _, msg := range messages {
		role := msg.Role
		if role == "" {
			role = "user"
		}
		content := msg.Content
		switch role {
		case "system":
			if strings.TrimSpace(content) != "" {
				systemParts = append(systemParts, content)
			}
		case "assistant":
			contents = append(contents, map[string]any{
				"role":  "model",
				"parts": []map[string]any{{"text": content}},
			})
		default:
			contents = append(contents, map[string]any{
				"role":  "user",
				"parts": []map[string]any{{"text": content}},
			})
		}
	}
	var sys *string
	if len(systemParts) > 0 {
		s := strings.Join(systemParts, "\n\n")
		sys = &s
	}
	return sys, contents
}

// ExtractGeminiUsage 用量提取（candidates + thoughts 为输出）。
func ExtractGeminiUsage(usageMetadata map[string]any) *llm.UsageSample {
	if usageMetadata == nil {
		return nil
	}
	prompt := readUsageInt(usageMetadata, "prompt_token_count")
	completion := readUsageInt(usageMetadata, "candidates_token_count") + readUsageInt(usageMetadata, "thoughts_token_count")
	total := readUsageInt(usageMetadata, "total_token_count")
	if total == 0 {
		total = prompt + completion
	}
	hit := readUsageInt(usageMetadata, "cached_content_token_count")
	miss := prompt - hit
	if miss < 0 {
		miss = 0
	}
	return &llm.UsageSample{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
	}
}

// GetAPIKeyFromEnv 优先级：custom → GEMINI_API_KEY → GOOGLE_API_KEY。
func GetAPIKeyFromEnv(customEnv string) (string, string) {
	lookup := func(name string) string {
		return strings.TrimSpace(os.Getenv(name))
	}
	if customEnv != "" {
		if v := lookup(customEnv); v != "" {
			return v, customEnv
		}
	}
	if v := lookup(GeminiDefaultAPIKeyEnv); v != "" {
		return v, GeminiDefaultAPIKeyEnv
	}
	if v := lookup(GeminiFallbackAPIKeyEnv); v != "" {
		return v, GeminiFallbackAPIKeyEnv
	}
	env := customEnv
	if env == "" {
		env = GeminiDefaultAPIKeyEnv
	}
	return "", env
}

// GeminiResponse 是 generate_content 响应的最小投影。
type GeminiResponse struct {
	Text           any `json:"text"`
	Candidates     []struct {
		FinishReason any `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text any `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata map[string]any `json:"usageMetadata"`
}

// GeminiCaller google-genai SDK 等价（测试可注入 stub）。
type GeminiCaller interface {
	GenerateContent(model string, contents []map[string]any, config map[string]any) (*GeminiResponse, error)
	// TimeoutMS 暴露 timeout 换算结果（测试断言用）。
	TimeoutMS() int
}

type httpGeminiCaller struct {
	apiKey    string
	baseURL   string
	timeoutMS int
	client    *http.Client
}

func (h *httpGeminiCaller) TimeoutMS() int { return h.timeoutMS }

func (h *httpGeminiCaller) GenerateContent(model string, contents []map[string]any, config map[string]any) (*GeminiResponse, error) {
	body := map[string]any{"contents": contents}
	for k, v := range config {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := h.baseURL + "/v1beta/models/" + model + ":generateContent"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", h.apiKey)
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
		return nil, &llm.HTTPError{StatusCode: resp.StatusCode, Headers: headers, RequestID: headers["x-request-id"]}
	}
	var out GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GeminiClient 独立实现（不继承兼容基类）。
type GeminiClient struct {
	*llm.Core
	cfg   *config.LLMConfig
	Tiers map[string]llm.ResolvedTier[GeminiTierOptions]

	caller   GeminiCaller
	callerMu sync.Mutex
}

func NewGeminiClient(cfg *config.LLMConfig) (*GeminiClient, error) {
	tiers, err := ResolveProviderTiers(cfg.Tiers, geminiDefaultTiers(), parseGeminiOptions)
	if err != nil {
		return nil, err
	}
	return &GeminiClient{Core: llm.NewCore(), cfg: cfg, Tiers: tiers}, nil
}

func (g *GeminiClient) ValidateCredentials() error {
	key, targetEnv := GetAPIKeyFromEnv("")
	if g.cfg.APIKeyEnv != nil && *g.cfg.APIKeyEnv != "" {
		key, targetEnv = GetAPIKeyFromEnv(*g.cfg.APIKeyEnv)
	}
	if key == "" {
		return fmt.Errorf("未设置环境变量 %s（或 %s）", targetEnv, GeminiFallbackAPIKeyEnv)
	}
	return nil
}

// SetCallerForTesting 注入 stub。
func (g *GeminiClient) SetCallerForTesting(caller GeminiCaller) {
	g.callerMu.Lock()
	defer g.callerMu.Unlock()
	g.caller = caller
}

func (g *GeminiClient) EnsureCaller() (GeminiCaller, error) {
	g.callerMu.Lock()
	defer g.callerMu.Unlock()
	if g.caller != nil {
		return g.caller, nil
	}
	if err := g.ValidateCredentials(); err != nil {
		return nil, err
	}
	customEnv := ""
	if g.cfg.APIKeyEnv != nil {
		customEnv = *g.cfg.APIKeyEnv
	}
	key, _ := GetAPIKeyFromEnv(customEnv)
	baseURL := "https://generativelanguage.googleapis.com"
	if g.cfg.BaseURL != nil && *g.cfg.BaseURL != "" {
		baseURL = strings.TrimRight(*g.cfg.BaseURL, "/")
	}
	timeoutMS := g.cfg.Timeout * 1000
	g.caller = &httpGeminiCaller{
		apiKey:    key,
		baseURL:   baseURL,
		timeoutMS: timeoutMS,
		client:    &http.Client{Timeout: time.Duration(g.cfg.Timeout) * time.Second},
	}
	return g.caller, nil
}

// Complete 非流式调用（主规格 §5.9）。
func (g *GeminiClient) Complete(messages []llm.Message, tier string, jsonMode bool, maxTokens *int, stage string) (string, error) {
	tierConfig := llm.ResolveTier(g.Tiers, tier)
	caller, err := g.EnsureCaller()
	if err != nil {
		return "", err
	}
	systemInstruction, contents := ConvertMessagesToGemini(messages)
	opts := tierConfig.Options
	configKwargs := map[string]any{}
	if systemInstruction != nil {
		configKwargs["system_instruction"] = *systemInstruction
	}
	if jsonMode {
		configKwargs["response_mime_type"] = "application/json"
	}
	var effectiveMaxTokens *int
	if maxTokens != nil {
		effectiveMaxTokens = maxTokens
	} else if opts.MaxOutputTokens != nil {
		effectiveMaxTokens = opts.MaxOutputTokens
	}
	if effectiveMaxTokens != nil {
		configKwargs["max_output_tokens"] = *effectiveMaxTokens
	}
	if opts.Temperature != nil {
		configKwargs["temperature"] = *opts.Temperature
	}
	if opts.ThinkingLevel != nil || opts.ThinkingBudget != nil {
		thinking := map[string]any{}
		if opts.ThinkingLevel != nil {
			thinking["thinking_level"] = *opts.ThinkingLevel
		}
		if opts.ThinkingBudget != nil {
			thinking["thinking_budget"] = *opts.ThinkingBudget
		}
		configKwargs["thinking_config"] = thinking
	}
	for k, v := range opts.ExtraBody { // 扁平 update，用户可覆盖任何键
		configKwargs[k] = v
	}
	var stagePtr *string
	if stage != "" {
		s := stage
		stagePtr = &s
	}
	maxAttempts := g.cfg.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	reporter := &llm.RetryReporter{
		Provider: "Gemini", Tier: tier, Stage: stagePtr, MaxAttempts: maxAttempts,
		Emit: g.EmitEvent,
	}
	var response *GeminiResponse
	callErr := llm.CallWithRetry(g.cfg.MaxRetries, reporter, sharedRand, func() error {
		resp, err := caller.GenerateContent(tierConfig.Model, contents, configKwargs)
		if err != nil {
			return err
		}
		response = resp
		return nil
	})
	if callErr != nil {
		return "", callErr
	}
	sample := ExtractGeminiUsage(response.UsageMetadata)
	g.Usage.Record(tier, sample, stage)
	if len(response.Candidates) == 0 {
		return "", fmt.Errorf("Gemini API 未返回任何候选结果 (candidates 为空)")
	}
	candidate := response.Candidates[0]
	finishReason := fmt.Sprintf("%v", candidate.FinishReason)
	upper := strings.ToUpper(finishReason)
	if strings.Contains(upper, "SAFETY") || strings.Contains(upper, "BLOCK") {
		return "", fmt.Errorf("Gemini API 响应被安全拦截 (finish_reason=%s)", finishReason)
	}
	if s, ok := response.Text.(string); ok {
		return s, nil
	}
	var b strings.Builder
	for _, part := range candidate.Content.Parts {
		if part.Text != nil {
			if s, ok := part.Text.(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String(), nil
}

// CompleteJSON = Complete(json_mode=true) 后 ParseJSONLoose。
func (g *GeminiClient) CompleteJSON(messages []llm.Message, tier string, maxTokens *int, stage string) (any, error) {
	text, err := g.Complete(messages, tier, true, maxTokens, stage)
	if err != nil {
		return nil, err
	}
	return llm.ParseJSONLoose(text)
}

var _ = io.EOF
