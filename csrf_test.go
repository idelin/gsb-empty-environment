package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testOrigin = "https://pay.example.com"
	testSecret = "0123456789abcdef0123456789abcdef"
)

func newManager(t *testing.T, ttl time.Duration) *TokenManager {
	t.Helper()
	m, err := NewTokenManager([]byte(testSecret), ttl, NewMemoryTokenStore())
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return m
}

// writeHandler simulates the payment endpoint's state change.
type writeHandler struct{ writes *atomic.Int32 }

func (h writeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.writes.Add(1)
	w.WriteHeader(http.StatusOK)
}

func newPaymentServer(p *Protector, writes *atomic.Int32) http.Handler {
	return p.ProtectWrite("transfer", writeHandler{writes})
}

func postForm(t *testing.T, h http.Handler, sessionID, userID, token, origin, fetchSite string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"csrf_token": {token}, "amount": {"100"}}
	req := httptest.NewRequest(http.MethodPost, "https://pay.example.com/transfer", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	req = ContextWithIdentity(req, sessionID, userID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCrossSessionTokenRejected(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	// Attacker obtains a token from their own session.
	attackerToken, err := m.Issue("attacker-session", "attacker", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Victim is tricked into submitting it from the victim's session.
	rec := postForm(t, h, "victim-session", "victim", attackerToken, testOrigin, "same-origin")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-session token: got %d, want 403", rec.Code)
	}
	if !errors.Is(m.Validate(attackerToken, "victim-session", "victim", "transfer"), ErrSessionMismatch) {
		t.Fatal("want ErrSessionMismatch for cross-session use")
	}
	if writes.Load() != 0 {
		t.Fatal("write occurred despite rejected token")
	}
	// The rejected token remains valid in its own session.
	rec = postForm(t, h, "attacker-session", "attacker", attackerToken, testOrigin, "same-origin")
	if rec.Code != http.StatusOK {
		t.Fatalf("token in bound session: got %d, want 200", rec.Code)
	}
}

func TestCrossUserTokenRejected(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	// Same session, but the token was issued to a different user.
	token, err := m.Issue("shared-session", "alice", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec := postForm(t, h, "shared-session", "mallory", token, testOrigin, "same-origin")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user token: got %d, want 403", rec.Code)
	}
	if writes.Load() != 0 {
		t.Fatal("write occurred despite rejected token")
	}
}

func TestReplayRejected(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	token, err := m.Issue("sess-1", "alice", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if rec := postForm(t, h, "sess-1", "alice", token, testOrigin, "same-origin"); rec.Code != http.StatusOK {
		t.Fatalf("first submit: got %d, want 200", rec.Code)
	}
	if rec := postForm(t, h, "sess-1", "alice", token, testOrigin, "same-origin"); rec.Code != http.StatusForbidden {
		t.Fatalf("replay: got %d, want 403", rec.Code)
	}
	if writes.Load() != 1 {
		t.Fatalf("writes = %d, want exactly 1", writes.Load())
	}
}

func TestCrossSiteGetCannotWrite(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	// Attacker lures the victim into loading a cross-site GET URL.
	token, err := m.Issue("victim-session", "victim", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet,
		"https://pay.example.com/transfer?csrf_token="+url.QueryEscape(token), nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req = ContextWithIdentity(req, "victim-session", "victim")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("cross-site GET write: got %d, want 405", rec.Code)
	}
	if writes.Load() != 0 {
		t.Fatal("GET triggered a write")
	}
	// The token must remain unconsumed by the failed attempt.
	if err := m.Validate(token, "victim-session", "victim", "transfer"); err != nil {
		t.Fatalf("token consumed by rejected GET: %v", err)
	}
}

func TestLegitimateFormFlow(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	// Victim's browser renders the form; the service issues a token.
	token, err := m.Issue("victim-session", "victim", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec := postForm(t, h, "victim-session", "victim", token, testOrigin, "same-origin")
	if rec.Code != http.StatusOK {
		t.Fatalf("legitimate submit: got %d, want 200", rec.Code)
	}
	if writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", writes.Load())
	}
}

func TestConcurrentSubmissionsSingleSuccess(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	token, err := m.Issue("sess-1", "alice", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	const n = 32
	var okCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			form := url.Values{"csrf_token": {token}}
			req := httptest.NewRequest(http.MethodPost, "https://pay.example.com/transfer",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", testOrigin)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req = ContextWithIdentity(req, "sess-1", "alice")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				okCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("concurrent submits accepted = %d, want exactly 1", okCount.Load())
	}
	if writes.Load() != 1 {
		t.Fatalf("writes = %d, want exactly 1", writes.Load())
	}
}

func TestCrossSiteFormPostRejected(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	var writes atomic.Int32
	h := newPaymentServer(p, &writes)

	token, err := m.Issue("victim-session", "victim", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Auto-submitting form hosted on the attacker's origin.
	rec := postForm(t, h, "victim-session", "victim", token, "https://evil.example", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST: got %d, want 403", rec.Code)
	}
	if writes.Load() != 0 {
		t.Fatal("write occurred despite cross-site rejection")
	}
}

func TestCrossSiteNavigationAllowedOnRead(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	p := NewProtector(m, testOrigin)
	read := p.ProtectRead(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Top-level navigation from another site is allowed for reads.
	req := httptest.NewRequest(http.MethodGet, "https://pay.example.com/balance", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	rec := httptest.NewRecorder()
	read.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-site navigation: got %d, want 200", rec.Code)
	}

	// Cross-site script-driven fetch is rejected.
	req = httptest.NewRequest(http.MethodGet, "https://pay.example.com/balance", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	rec = httptest.NewRecorder()
	read.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site fetch: got %d, want 403", rec.Code)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	m := newManager(t, 50*time.Millisecond)
	token, err := m.Issue("sess-1", "alice", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := m.Validate(token, "sess-1", "alice", "transfer"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token: got %v, want ErrTokenExpired", err)
	}
}

func TestWrongActionRejected(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	token, err := m.Issue("sess-1", "alice", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := m.Validate(token, "sess-1", "alice", "change-password"); !errors.Is(err, ErrActionMismatch) {
		t.Fatalf("wrong action: got %v, want ErrActionMismatch", err)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	m := newManager(t, 5*time.Minute)
	token, err := m.Issue("sess-1", "alice", "transfer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tampered := token[:len(token)-2] + "AA"
	if err := m.Validate(tampered, "sess-1", "alice", "transfer"); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func ExampleTokenManager() {
	m, _ := NewTokenManager([]byte(testSecret), 2*time.Minute, NewMemoryTokenStore())
	token, _ := m.Issue("session-abc", "user-42", "transfer")
	fmt.Println(m.Validate(token, "session-abc", "user-42", "transfer"))
	fmt.Println(m.Validate(token, "session-abc", "user-42", "transfer"))
	// Output:
	// <nil>
	// csrf: token already consumed or unknown form instance
}
