package llm

import (
	"fmt"
	"testing"
)

// 迁移自 tests/test_llm_retrying.py（test_orchestrator_retry_sink 归阶段 3）。

func httpErr(status int, headers map[string]string) *HTTPError {
	if headers == nil {
		headers = map[string]string{}
	}
	return &HTTPError{StatusCode: status, Headers: headers, RequestID: "req-test"}
}

func TestTransientHTTPStatusesAreRetryable(t *testing.T) {
	for _, status := range []int{408, 409, 429, 500, 502, 599} {
		if !IsRetryableProviderError(httpErr(status, nil)) {
			t.Errorf("status %d 应可重试", status)
		}
		if got := RetryReason(httpErr(status, nil)); got != fmt.Sprintf("http_%d", status) {
			t.Errorf("reason = %q, want %q", got, fmt.Sprintf("http_%d", status))
		}
	}
}

func TestPermanentHTTPStatusesAreNotRetryable(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 413, 422} {
		if IsRetryableProviderError(httpErr(status, nil)) {
			t.Errorf("status %d 不应可重试", status)
		}
	}
}

func TestServerRetryOverrideTakesPrecedenceOverStatus(t *testing.T) {
	if IsRetryableProviderError(httpErr(503, map[string]string{"x-should-retry": "false"})) {
		t.Errorf("x-should-retry=false 的 503 不应重试")
	}
	if !IsRetryableProviderError(httpErr(400, map[string]string{"x-should-retry": "true"})) {
		t.Errorf("x-should-retry=true 的 400 应重试")
	}
}

func TestOnlyTransientTransportErrorsAreRetryable(t *testing.T) {
	if got := RetryReason(&TransportError{Kind: "timeout"}); got != "timeout" {
		t.Errorf("timeout reason = %q", got)
	}
	if got := RetryReason(&TransportError{Kind: "connection"}); got != "connection" {
		t.Errorf("connection reason = %q", got)
	}
	if got := RetryReason(&TransportError{Kind: "invalid_url"}); got != "" {
		t.Errorf("invalid_url reason = %q, want 空", got)
	}
	if got := RetryReason(&TransportError{Kind: "protocol"}); got != "" {
		t.Errorf("protocol reason = %q, want 空", got)
	}
	if got := RetryReason(errString("application failure")); got != "" {
		t.Errorf("RuntimeError reason = %q, want 空", got)
	}
	if IsRetryableProviderError(&TransportError{Kind: "invalid_url"}) {
		t.Errorf("invalid_url 不应可重试")
	}
	if !IsRetryableProviderError(&TransportError{Kind: "timeout"}) {
		t.Errorf("timeout 应可重试")
	}
	if !IsRetryableProviderError(&TransportError{Kind: "connection"}) {
		t.Errorf("connection 应可重试")
	}
	if IsRetryableProviderError(errString("application failure")) {
		t.Errorf("普通错误不应可重试")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestRetryAfterParsing(t *testing.T) {
	// retry-after-ms（毫秒）
	if s, ok := RetryAfterSeconds(httpErr(503, map[string]string{"retry-after-ms": "1500"})); !ok || s != 1.5 {
		t.Errorf("retry-after-ms=1500 → %v %v", s, ok)
	}
	// retry-after（秒）
	if s, ok := RetryAfterSeconds(httpErr(503, map[string]string{"Retry-After": "2"})); !ok || s != 2 {
		t.Errorf("Retry-After=2 → %v %v", s, ok)
	}
	// 上限 30s 钳制
	if s, _ := RetryAfterSeconds(httpErr(503, map[string]string{"Retry-After": "120"})); s != 30 {
		t.Errorf("120s 应钳制到 30，得 %v", s)
	}
	// 下限 0
	if s, _ := RetryAfterSeconds(httpErr(503, map[string]string{"retry-after-ms": "0"})); s != 0 {
		t.Errorf("0ms → %v", s)
	}
	// 无头
	if _, ok := RetryAfterSeconds(httpErr(503, nil)); ok {
		t.Errorf("无头应 ok=false")
	}
}

func TestWaitForProviderRetryPrefersServerHeader(t *testing.T) {
	secs, source := WaitForProviderRetry(httpErr(502, map[string]string{"retry-after-ms": "0"}), 1, nil)
	if secs != 0 || source != "server" {
		t.Errorf("wait = %v %q", secs, source)
	}
}
