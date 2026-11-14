package netsec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testOrigin = "http://example.com"

func newHTTPTestService(t *testing.T) (*Service, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	svc, err := NewService(Config{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		TTL:    5 * time.Minute,
		Now:    clock.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, clock
}

func getLink(t *testing.T, handler http.Handler, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/login?token="+url.QueryEscape(token), nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func postLink(t *testing.T, handler http.Handler, token, origin, fetchSite string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func responseBody(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	return resp.Body.String()
}

func TestPreviewGetsAreSideEffectFreeAndPostConsumesOnce(t *testing.T) {
	svc, _ := newHTTPTestService(t)
	var consumed atomic.Int32

	handler, err := NewHandler(HandlerConfig{
		Service:        svc,
		RequiredIntent: "login",
		OnConsume: func(_ context.Context, w http.ResponseWriter, _ *http.Request, claims Claims) error {
			consumed.Add(1)
			if claims.UserID != "alice" {
				t.Errorf("callback user = %q, want alice", claims.UserID)
			}
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    "established",
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	token, err := svc.Issue("alice", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for i := 0; i < 3; i++ {
		resp := getLink(t, handler, token, map[string]string{
			"User-Agent": "Slackbot-LinkExpanding 1.0",
			"Referer":    "https://evil.example/preview",
		})
		body := responseBody(t, resp)
		if resp.Code != http.StatusOK {
			t.Fatalf("preview GET status = %d, want 200; body=%s", resp.Code, body)
		}
		if len(resp.Header().Values("Set-Cookie")) != 0 {
			t.Fatalf("GET set cookies: %v", resp.Header().Values("Set-Cookie"))
		}
		if !strings.Contains(body, `method="post"`) || !strings.Contains(body, token) {
			t.Fatalf("confirmation page missing POST form or token")
		}
		if got := consumed.Load(); got != 0 {
			t.Fatalf("GET authenticated %d times, want 0", got)
		}
	}

	resp := postLink(t, handler, token, testOrigin, "same-origin")
	body := responseBody(t, resp)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("honest POST status = %d, want 204; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
	if len(resp.Header().Values("Set-Cookie")) != 1 {
		t.Fatalf("successful POST cookies = %v, want one session cookie", resp.Header().Values("Set-Cookie"))
	}

	resp = postLink(t, handler, token, testOrigin, "same-origin")
	body = responseBody(t, resp)
	if resp.Code != http.StatusGone {
		t.Fatalf("replayed POST status = %d, want 410; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 1 {
		t.Fatalf("callback count after replay = %d, want 1", got)
	}

	resp = getLink(t, handler, token, nil)
	body = responseBody(t, resp)
	if resp.Code != http.StatusGone {
		t.Fatalf("GET after consumption status = %d, want 410; body=%s", resp.Code, body)
	}
}

func TestCrossSitePostsDoNotSpendToken(t *testing.T) {
	svc, _ := newHTTPTestService(t)
	var consumed atomic.Int32
	handler, err := NewHandler(HandlerConfig{
		Service: svc,
		OnConsume: func(context.Context, http.ResponseWriter, *http.Request, Claims) error {
			consumed.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	token, err := svc.Issue("alice", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cases := []struct {
		name      string
		origin    string
		fetchSite string
	}{
		{name: "cross origin", origin: "https://evil.example", fetchSite: "cross-site"},
		{name: "missing origin", fetchSite: "same-origin"},
		{name: "fetch metadata says cross-site", origin: testOrigin, fetchSite: "cross-site"},
	}
	for _, tc := range cases {
		resp := postLink(t, handler, token, tc.origin, tc.fetchSite)
		body := responseBody(t, resp)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("%s POST status = %d, want 403; body=%s", tc.name, resp.Code, body)
		}
		if got := consumed.Load(); got != 0 {
			t.Fatalf("%s authenticated %d times, want 0", tc.name, got)
		}
	}

	resp := getLink(t, handler, token, map[string]string{
		"Sec-Fetch-Site": "cross-site",
	})
	body := responseBody(t, resp)
	if resp.Code != http.StatusOK {
		t.Fatalf("cross-site-ish GET status = %d, want side-effect-free 200; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 0 {
		t.Fatalf("GET authenticated %d times, want 0", got)
	}

	resp = postLink(t, handler, token, testOrigin, "same-origin")
	body = responseBody(t, resp)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("honest POST status = %d, want 204; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
}

func TestExpiredLinkIsRejectedByGetAndPost(t *testing.T) {
	svc, clock := newHTTPTestService(t)
	var consumed atomic.Int32
	handler, err := NewHandler(HandlerConfig{
		Service: svc,
		OnConsume: func(context.Context, http.ResponseWriter, *http.Request, Claims) error {
			consumed.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	token, err := svc.Issue("alice", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	clock.Advance(5 * time.Minute)

	resp := getLink(t, handler, token, nil)
	body := responseBody(t, resp)
	if resp.Code != http.StatusGone {
		t.Fatalf("expired GET status = %d, want 410; body=%s", resp.Code, body)
	}

	resp = postLink(t, handler, token, testOrigin, "same-origin")
	body = responseBody(t, resp)
	if resp.Code != http.StatusGone {
		t.Fatalf("expired POST status = %d, want 410; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 0 {
		t.Fatalf("expired link authenticated %d times, want 0", got)
	}
}

func TestHandlerChecksIntentAndPendingSessionBinding(t *testing.T) {
	svc, _ := newHTTPTestService(t)
	var consumed atomic.Int32
	handler, err := NewHandler(HandlerConfig{
		Service:        svc,
		RequiredIntent: "login",
		ExpectedBinding: func(r *http.Request) (Binding, error) {
			return Binding{UserID: r.Header.Get("X-Pending-User")}, nil
		},
		OnConsume: func(context.Context, http.ResponseWriter, *http.Request, Claims) error {
			consumed.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	token, err := svc.Issue("alice", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	post := func(user string) *httptest.ResponseRecorder {
		form := url.Values{"token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("X-Pending-User", user)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}

	resp := post("bob")
	body := responseBody(t, resp)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("wrong pending user status = %d, want 403; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 0 {
		t.Fatalf("wrong binding authenticated %d times, want 0", got)
	}

	resp = post("alice")
	body = responseBody(t, resp)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("matching pending user status = %d, want 204; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
}

func TestHandlerRejectsWrongIntentAndLeavesTokenUsable(t *testing.T) {
	svc, _ := newHTTPTestService(t)
	var consumed atomic.Int32
	handler, err := NewHandler(HandlerConfig{
		Service:        svc,
		RequiredIntent: "login",
		OnConsume: func(context.Context, http.ResponseWriter, *http.Request, Claims) error {
			consumed.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	wrongIntent, err := svc.Issue("alice", "password-reset")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	resp := postLink(t, handler, wrongIntent, testOrigin, "same-origin")
	body := responseBody(t, resp)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("wrong intent status = %d, want 403; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 0 {
		t.Fatalf("wrong intent authenticated %d times, want 0", got)
	}

	loginToken, err := svc.Issue("alice", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	resp = postLink(t, handler, loginToken, testOrigin, "same-origin")
	body = responseBody(t, resp)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("login token status = %d, want 204; body=%s", resp.Code, body)
	}
	if got := consumed.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
}

func TestHandlerUsesConfiguredOriginAndSuccessRedirect(t *testing.T) {
	svc, _ := newHTTPTestService(t)
	handler, err := NewHandler(HandlerConfig{
		Service:        svc,
		AllowedOrigins: []string{"https://auth.example.com"},
		SuccessURL:     "/account",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	token, err := svc.Issue("alice", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	resp := postLink(t, handler, token, testOrigin, "same-origin")
	body := responseBody(t, resp)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("request origin not in configured list status = %d, want 403; body=%s", resp.Code, body)
	}

	resp = postLink(t, handler, token, "https://auth.example.com", "same-origin")
	body = responseBody(t, resp)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("configured origin status = %d, want 303; body=%s", resp.Code, body)
	}
	if got := resp.Header().Get("Location"); got != "/account" {
		t.Fatalf("Location = %q, want /account", got)
	}
}

func TestHandlerRejectsNonUserActionMethods(t *testing.T) {
	svc, _ := newHTTPTestService(t)
	handler, err := NewHandler(HandlerConfig{Service: svc})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "/login?token=anything", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d, want 405", recorder.Code)
	}
	if got := recorder.Header().Get("Allow"); got != "GET, POST" {
		t.Fatalf("Allow = %q, want GET, POST", got)
	}
}
