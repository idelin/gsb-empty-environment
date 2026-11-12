package par

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSecret = "topsecret"

// fakeAuthenticator accepts client_secret_post credentials for registered
// clients and records every invocation.
type fakeAuthenticator struct {
	secrets map[string]string
	calls   int
}

func (f *fakeAuthenticator) authenticate(r *http.Request) (string, error) {
	f.calls++
	clientID := r.PostFormValue("client_id")
	if secret, ok := f.secrets[clientID]; ok && secret == r.PostFormValue("client_secret") {
		return clientID, nil
	}
	return "", errAuthFailed
}

var errAuthFailed = &authError{"bad credentials"}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func newTestManager(t *testing.T, now func() time.Time, ttl time.Duration) (*Manager, *fakeAuthenticator) {
	t.Helper()
	authn := &fakeAuthenticator{secrets: map[string]string{
		"client-a": testSecret,
		"client-b": "other-secret",
	}}
	opts := []Option{WithClock(now)}
	if ttl > 0 {
		opts = append(opts, WithTTL(ttl))
	}
	return NewManager(authn.authenticate, opts...), authn
}

func postForm(t *testing.T, handler http.Handler, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/par", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func push(t *testing.T, handler http.Handler, form url.Values) (string, int) {
	t.Helper()
	rec := postForm(t, handler, form)
	if rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		RequestURI string `json:"request_uri"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode push response: %v", err)
	}
	return payload.RequestURI, payload.ExpiresIn
}

func validPushForm() url.Values {
	return url.Values{
		"client_id":     {"client-a"},
		"client_secret": {testSecret},
		"response_type": {"code"},
		"redirect_uri":  {"https://client.example/cb"},
		"scope":         {"openid profile"},
		"state":         {"pushed-state"},
	}
}

// captureHandler records the query parameters the authorization endpoint
// actually observes after the middleware runs.
type captureHandler struct {
	mu      sync.Mutex
	queries []url.Values
}

func (h *captureHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queries = append(h.queries, r.URL.Query())
}

func (h *captureHandler) snapshot() []url.Values {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]url.Values, len(h.queries))
	copy(out, h.queries)
	return out
}

// doAuthorize drives the middleware directly without opening a socket, which
// keeps the tests usable in restricted sandboxes and under -race.
func doAuthorize(manager *Manager, capture *captureHandler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	manager.AuthorizeMiddleware(capture).ServeHTTP(rec, req)
	return rec
}

func authorizeTarget(ref, extra string) string {
	return "/authorize?request_uri=" + url.QueryEscape(ref) + extra
}

func TestPushSuccessIssuesReference(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	manager, authn := newTestManager(t, func() time.Time { return now }, 0)

	ref, expiresIn := push(t, manager.PushHandler(), validPushForm())

	if !strings.HasPrefix(ref, RequestURIPrefix) {
		t.Fatalf("reference %q lacks prefix %q", ref, RequestURIPrefix)
	}
	if expiresIn != 60 {
		t.Fatalf("expires_in = %d, want 60", expiresIn)
	}
	if authn.calls != 1 {
		t.Fatalf("authenticator calls = %d, want 1", authn.calls)
	}

	entry, err := manager.store.Consume(ref, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if entry.ClientID != "client-a" {
		t.Fatalf("stored client = %q", entry.ClientID)
	}
	if entry.Params["client_secret"] != "" {
		t.Fatalf("client_secret was stored: %#v", entry.Params)
	}
	if entry.Params["client_id"] != "client-a" {
		t.Fatalf("bound client_id = %q", entry.Params["client_id"])
	}
	if entry.Params["state"] != "pushed-state" {
		t.Fatalf("state not stored: %#v", entry.Params)
	}
	wantExpiry := now.Add(60 * time.Second)
	if !entry.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expiry = %v, want %v", entry.ExpiresAt, wantExpiry)
	}
}

func TestPushRejectsRequestBodyRequestURI(t *testing.T) {
	manager, authn := newTestManager(t, time.Now, 0)
	form := validPushForm()
	form.Set("request_uri", RequestURIPrefix+"attacker-chosen")

	rec := postForm(t, manager.PushHandler(), form)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if authn.calls != 0 {
		t.Fatalf("authentication must not run for a malformed push, calls = %d", authn.calls)
	}
}

func TestPushRequiresClientAuthentication(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	form := validPushForm()
	form.Set("client_secret", "wrong")

	rec := postForm(t, manager.PushHandler(), form)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_client") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPushRejectsNonPOST(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	req := httptest.NewRequest(http.MethodGet, "/par", nil)
	rec := httptest.NewRecorder()

	manager.PushHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestPushBindsAuthenticatedClientIDOverBody(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	form := validPushForm()
	// Credentials authenticate client-a even though the body claims client-b.
	form.Set("client_id", "client-b")
	form.Set("client_secret", testSecret)

	rec := postForm(t, manager.PushHandler(), form)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for mismatched credentials", rec.Code)
	}

	ref, _ := push(t, manager.PushHandler(), validPushForm())
	entry, err := manager.store.Consume(ref, time.Now())
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if entry.ClientID != "client-a" || entry.Params["client_id"] != "client-a" {
		t.Fatalf("client binding = %q / %q", entry.ClientID, entry.Params["client_id"])
	}
}

func TestAuthorizeUsesOnlyBoundParameters(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	ref, _ := push(t, manager.PushHandler(), validPushForm())

	capture := &captureHandler{}
	// Attacker appends overriding and extra query parameters.
	rec := doAuthorize(manager, capture, authorizeTarget(ref,
		"&state=forged&scope=admin&extra=injected&client_id=client-a"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	queries := capture.snapshot()
	if len(queries) != 1 {
		t.Fatalf("handler invocations = %d", len(queries))
	}
	got := queries[0]
	if got.Get("state") != "pushed-state" {
		t.Fatalf("state = %q, want pushed value", got.Get("state"))
	}
	if got.Get("scope") != "openid profile" {
		t.Fatalf("scope = %q, want pushed value", got.Get("scope"))
	}
	if got.Has("extra") {
		t.Fatalf("attacker parameter leaked through: %v", got)
	}
	if got.Get("request_uri") != "" {
		t.Fatalf("request_uri must not reach the inner handler: %v", got)
	}
	if got.Get("client_id") != "client-a" {
		t.Fatalf("client_id = %q", got.Get("client_id"))
	}
}

func TestAuthorizeRejectsDifferentClient(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	ref, _ := push(t, manager.PushHandler(), validPushForm())

	capture := &captureHandler{}
	rec := doAuthorize(manager, capture, authorizeTarget(ref, "&client_id=client-b"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_client") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if len(capture.snapshot()) != 0 {
		t.Fatal("inner handler must not run for a cross-client reference")
	}

	// The rejected attempt still consumed the one-time reference.
	_, err := manager.store.Consume(ref, time.Now())
	if err != ErrUsed {
		t.Fatalf("reference state err = %v, want ErrUsed", err)
	}
}

func TestAuthorizeReplayFails(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	ref, _ := push(t, manager.PushHandler(), validPushForm())

	capture := &captureHandler{}
	target := authorizeTarget(ref, "")

	first := doAuthorize(manager, capture, target)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}
	second := doAuthorize(manager, capture, target)
	if second.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want 400", second.Code)
	}
	if !strings.Contains(second.Body.String(), "invalid_request_object") {
		t.Fatalf("replay body = %s", second.Body.String())
	}
	if len(capture.snapshot()) != 1 {
		t.Fatalf("inner handler invocations = %d, want 1", len(capture.snapshot()))
	}

	if _, err := manager.store.Consume(ref, time.Now()); err != ErrUsed {
		t.Fatalf("store lookup err = %v, want ErrUsed", err)
	}
}

func TestAuthorizeExpiredReference(t *testing.T) {
	base := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	current := base
	now := func() time.Time { return current }
	manager, _ := newTestManager(t, now, 30*time.Second)

	ref, expiresIn := push(t, manager.PushHandler(), validPushForm())
	if expiresIn != 30 {
		t.Fatalf("expires_in = %d, want 30", expiresIn)
	}
	// Both references are pushed while the clock is still at base.
	ref2, _ := push(t, manager.PushHandler(), validPushForm())

	capture := &captureHandler{}
	// Still valid just before the TTL elapses.
	current = base.Add(29 * time.Second)
	if rec := doAuthorize(manager, capture, authorizeTarget(ref, "")); rec.Code != http.StatusOK {
		t.Fatalf("valid window status = %d", rec.Code)
	}

	// The second reference is never consumed before the TTL elapses.
	current = base.Add(31 * time.Second)

	expired := doAuthorize(manager, capture, authorizeTarget(ref2, ""))
	if expired.Code != http.StatusBadRequest {
		t.Fatalf("expired status = %d, want 400", expired.Code)
	}
	if !strings.Contains(expired.Body.String(), "invalid_request_object") {
		t.Fatalf("body = %s", expired.Body.String())
	}
	if len(capture.snapshot()) != 1 {
		t.Fatalf("inner handler invocations = %d, want 1", len(capture.snapshot()))
	}
}

func TestMemoryStoreExpiresUnconsumedEntry(t *testing.T) {
	base := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	entry := Entry{
		ClientID:  "client-a",
		Params:    map[string]string{"client_id": "client-a"},
		ExpiresAt: base.Add(60 * time.Second),
	}
	if err := store.Put(RequestURIPrefix+"x", entry); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := store.Consume(RequestURIPrefix+"x", base.Add(60*time.Second)); err != ErrExpired {
		t.Fatalf("at exactly expiry err = %v, want ErrExpired", err)
	}
	if _, err := store.Consume(RequestURIPrefix+"x", base.Add(61*time.Second)); err != ErrNotFound {
		t.Fatalf("after deletion err = %v, want ErrNotFound", err)
	}
}

func TestAuthorizeUnknownReference(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	capture := &captureHandler{}

	rec := doAuthorize(manager, capture,
		"/authorize?request_uri="+url.QueryEscape(RequestURIPrefix+"never-issued"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(capture.snapshot()) != 0 {
		t.Fatal("inner handler must not run for an unknown reference")
	}
}

func TestAuthorizeWithoutRequestURIPassesThrough(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	capture := &captureHandler{}

	rec := doAuthorize(manager, capture,
		"/authorize?response_type=code&client_id=client-a&state=plain")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	queries := capture.snapshot()
	if len(queries) != 1 || queries[0].Get("state") != "plain" {
		t.Fatalf("plain authorize request was altered: %v", queries)
	}
}

func TestConcurrentDistinctReferencesAllSucceed(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)

	const goroutines = 64
	targets := make([]string, goroutines)
	for i := range targets {
		ref, _ := push(t, manager.PushHandler(), validPushForm())
		targets[i] = authorizeTarget(ref, "")
	}

	capture := &captureHandler{}
	var wg sync.WaitGroup
	statuses := make(chan int, goroutines)
	start := make(chan struct{})
	for _, target := range targets {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			<-start
			statuses <- doAuthorize(manager, capture, target).Code
		}(target)
	}
	close(start)
	wg.Wait()
	close(statuses)

	var ok, rejected int
	for status := range statuses {
		switch status {
		case http.StatusOK:
			ok++
		case http.StatusBadRequest:
			rejected++
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if ok != goroutines || rejected != 0 {
		t.Fatalf("ok = %d, rejected = %d; want %d / 0", ok, rejected, goroutines)
	}
	if len(capture.snapshot()) != goroutines {
		t.Fatalf("inner handler invocations = %d, want %d",
			len(capture.snapshot()), goroutines)
	}
}

func TestConcurrentSameReferenceSucceedsOnce(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	ref, _ := push(t, manager.PushHandler(), validPushForm())
	target := authorizeTarget(ref, "")

	const goroutines = 64
	capture := &captureHandler{}
	var wg sync.WaitGroup
	statuses := make(chan int, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			statuses <- doAuthorize(manager, capture, target).Code
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)

	var ok, rejected int
	for status := range statuses {
		switch status {
		case http.StatusOK:
			ok++
		case http.StatusBadRequest:
			rejected++
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if ok != 1 {
		t.Fatalf("successful uses = %d, want exactly 1", ok)
	}
	if rejected != goroutines-1 {
		t.Fatalf("rejections = %d, want %d", rejected, goroutines-1)
	}
	if len(capture.snapshot()) != 1 {
		t.Fatalf("inner handler invocations = %d, want 1", len(capture.snapshot()))
	}

	if _, err := manager.store.Consume(ref, time.Now()); err != ErrUsed {
		t.Fatalf("store lookup err = %v, want ErrUsed", err)
	}
}

func TestPushedReferencesAreUniqueAndOpaque(t *testing.T) {
	manager, _ := newTestManager(t, time.Now, 0)
	const count = 50
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		ref, _ := push(t, manager.PushHandler(), validPushForm())
		if !strings.HasPrefix(ref, RequestURIPrefix) {
			t.Fatalf("reference %q lacks prefix", ref)
		}
		if _, dup := seen[ref]; dup {
			t.Fatalf("duplicate reference %q", ref)
		}
		seen[ref] = struct{}{}
	}
}
