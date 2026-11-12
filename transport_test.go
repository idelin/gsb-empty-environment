package netsec

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// fakeRoundTripper 是进程内的假推送服务，不发起任何真实 TCP 连接，
// 因而在禁止回环绑定的沙箱中也能确定性地测试请求头、载荷与重定向行为。
type fakeRoundTripper struct {
	mu       sync.Mutex
	requests []*http.Request
	handler  func(*http.Request) (*http.Response, error)
	// dialFail 非空时模拟拨号阶段失败（用于验证 Guard.DialContext 不被绕过）。
	dialFail error
}

func (f *fakeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	captured := request.Clone(request.Context())
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		captured.Body = io.NopCloser(strings.NewReader(string(body)))
	}
	f.requests = append(f.requests, captured)
	handler := f.handler
	dialFail := f.dialFail
	f.mu.Unlock()

	if dialFail != nil {
		return nil, dialFail
	}
	return handler(request)
}

func (f *fakeRoundTripper) snapshot() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*http.Request, len(f.requests))
	copy(out, f.requests)
	return out
}

func textResponse(status int, headers map[string]string, body string) *http.Response {
	response := &http.Response{
		Status:     http.StatusText(status),
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	for key, value := range headers {
		response.Header.Set(key, value)
	}
	return response
}

func redirectResponse(location string, status int) *http.Response {
	return textResponse(status, map[string]string{"Location": location}, "")
}

// fakeTransportWithDialer 把 Guard 的安全拨号器串在假传输前面，
// 确保每次请求都先经过 DialContext 的白名单与 IP 复核。
type fakeTransportWithDialer struct {
	guard *Guard
	inner *fakeRoundTripper
}

func (f *fakeTransportWithDialer) RoundTrip(request *http.Request) (*http.Response, error) {
	host := request.URL.Hostname()
	// 与 http.Transport 行为一致：实际建连前执行白名单与解析 IP 复核。
	if _, err := f.guard.CheckDialPolicy(request.Context(), host); err != nil {
		return nil, err
	}
	return f.inner.RoundTrip(request)
}

var _ http.RoundTripper = (*fakeTransportWithDialer)(nil)

func mustParseURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}
