package llm

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// 统一选择性重试（主规格 §5.5）。Go 侧无 SDK，"SDK 重试关闭"天然满足。

// HTTPError 带 status/headers/request_id 的 HTTP 错误（等价 openai SDK 的 APIStatusError 族）。
type HTTPError struct {
	StatusCode int
	Headers    map[string]string
	RequestID  string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d", e.StatusCode) }

// TransportError 传输层错误：Kind ∈ {"timeout", "connection", "invalid_url", "protocol", "ssl"}。
// timeout/connection 可重试；其余视为永久错误。
type TransportError struct {
	Kind string
	Msg  string
}

func (e *TransportError) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Msg) }

// ErrorTypeName 返回类型名（等价 Python type(err).__name__）。
func ErrorTypeName(err error) string {
	switch err.(type) {
	case *HTTPError:
		return "HTTPError"
	case *TransportError:
		return "TransportError"
	default:
		return "Error"
	}
}

func isRetryableStatus(code int) bool {
	return code == 408 || code == 409 || code == 429 || code >= 500
}

// IsRetryableProviderError 分类：x-should-retry 优先；有状态码时仅 408/409/429/5xx；
// 传输层仅 timeout/connection；其余永久。
func IsRetryableProviderError(err error) bool {
	for e := err; e != nil; e = unwrapOne(e) {
		if h, ok := e.(*HTTPError); ok {
			if v, present := h.Headers["x-should-retry"]; present && v != "" {
				return strings.EqualFold(strings.TrimSpace(v), "true")
			}
			return isRetryableStatus(h.StatusCode)
		}
		if t, ok := e.(*TransportError); ok {
			return t.Kind == "timeout" || t.Kind == "connection"
		}
	}
	return false
}

// RetryReason 返回原因码：server_requested_retry / http_{code} / timeout / connection / ""（不可判定）。
func RetryReason(err error) string {
	for e := err; e != nil; e = unwrapOne(e) {
		if h, ok := e.(*HTTPError); ok {
			if v, present := h.Headers["x-should-retry"]; present && v != "" {
				if strings.EqualFold(strings.TrimSpace(v), "true") {
					return "server_requested_retry"
				}
				return ""
			}
			return fmt.Sprintf("http_%d", h.StatusCode)
		}
		if t, ok := e.(*TransportError); ok {
			switch t.Kind {
			case "timeout":
				return "timeout"
			case "connection":
				return "connection"
			}
			return ""
		}
	}
	return ""
}

// unwrapOne 兼容标准库包装错误（url.Error 等）。
func unwrapOne(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

// StatusCodeOf 提取状态码（无则 0）。
func StatusCodeOf(err error) int {
	for e := err; e != nil; e = unwrapOne(e) {
		if h, ok := e.(*HTTPError); ok {
			return h.StatusCode
		}
	}
	return 0
}

// RequestIDOf 提取请求 ID。
func RequestIDOf(err error) string {
	for e := err; e != nil; e = unwrapOne(e) {
		if h, ok := e.(*HTTPError); ok {
			return h.RequestID
		}
	}
	return ""
}

const maxWaitSeconds = 30.0

// RetryAfterSeconds 解析服务端等待头：retry-after-ms（毫秒）→ retry-after（秒或 HTTP 日期）；
// 上限 30s、下限 0；无头返回 (0, false)。
func RetryAfterSeconds(err error) (float64, bool) {
	for e := err; e != nil; e = unwrapOne(e) {
		h, ok := e.(*HTTPError)
		if !ok {
			continue
		}
		if v := h.Headers["retry-after-ms"]; v != "" {
			var ms float64
			if _, err2 := fmt.Sscanf(strings.TrimSpace(v), "%g", &ms); err2 == nil {
				return clampWait(ms / 1000.0), true
			}
		}
		if v := h.Headers["Retry-After"]; v != "" {
			v = strings.TrimSpace(v)
			var secs float64
			if _, err2 := fmt.Sscanf(v, "%g", &secs); err2 == nil {
				return clampWait(secs), true
			}
			// HTTP 日期
			for _, layout := range []string{http.TimeFormat, time.RFC1123, time.RFC850, time.ANSIC} {
				if t, err3 := time.Parse(layout, v); err3 == nil {
					return clampWait(time.Until(t).Seconds()), true
				}
			}
		}
	}
	return 0, false
}

func clampWait(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > maxWaitSeconds {
		return maxWaitSeconds
	}
	return x
}

// WaitForProviderRetry 优先服务端等待头，否则 wait_random_exponential(multiplier=1, max=30)。
func WaitForProviderRetry(err error, attempt int, rng *rand.Rand) (float64, string) {
	if secs, ok := RetryAfterSeconds(err); ok {
		return secs, "server"
	}
	upper := float64(int(1) << uint(min(attempt, 30))) // 2^attempt
	if upper > maxWaitSeconds {
		upper = maxWaitSeconds
	}
	if upper <= 0 {
		upper = 1
	}
	return rng.Float64() * upper, "exponential_jitter"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// RetryReporter 把重试等待/耗尽写成事件（主规格 §5.5）。
type RetryReporter struct {
	Provider    string
	Tier        string
	Stage       *string
	MaxAttempts int
	Emit        func(event string, data map[string]any)
}

func (r *RetryReporter) errorData(err error) map[string]any {
	data := map[string]any{
		"provider":    r.Provider,
		"tier":        r.Tier,
		"stage":       r.Stage,
		"reason":      RetryReason(err),
		"error_type":  ErrorTypeName(err),
		"status_code": StatusCodeOf(err),
		"request_id":  RequestIDOf(err),
	}
	return data
}

// BeforeSleep 发 llm_retry_wait 事件（等待前）。
func (r *RetryReporter) BeforeSleep(failedAttempt, nextAttempt int, waitSeconds float64, waitSource string, err error) {
	data := r.errorData(err)
	data["failed_attempt"] = failedAttempt
	data["next_attempt"] = nextAttempt
	data["max_attempts"] = r.MaxAttempts
	data["wait_seconds"] = waitSeconds
	data["wait_source"] = waitSource
	r.Emit("llm_retry_wait", data)
}

// Exhausted 发 llm_retry_exhausted 事件。
func (r *RetryReporter) Exhausted(attempts int, err error) {
	data := r.errorData(err)
	data["attempts"] = attempts
	data["max_attempts"] = r.MaxAttempts
	r.Emit("llm_retry_exhausted", data)
}

// CallWithRetry 是 provider_retry 的等价循环：maxRetries+1 次总尝试；
// 每次可重试失败等待前发事件；耗尽发事件并返回最后错误；永久错误立即返回。
func CallWithRetry(maxRetries int, reporter *RetryReporter, rng *rand.Rand, fn func() error) error {
	attempts := maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	if reporter != nil && reporter.MaxAttempts < 1 {
		reporter.MaxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if !IsRetryableProviderError(err) {
			return err
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		wait, source := WaitForProviderRetry(err, attempt, rng)
		if reporter != nil {
			reporter.BeforeSleep(attempt, attempt+1, wait, source, err)
		}
		time.Sleep(time.Duration(wait * float64(time.Second)))
	}
	if reporter != nil {
		reporter.Exhausted(attempts, lastErr)
	}
	return lastErr
}
