package authcache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

type fakeAuthorizer struct {
	calls     int
	responder func(r *http.Request) *http.Response
}

func (f *fakeAuthorizer) Authorize(r *http.Request) (*http.Response, error) {
	f.calls++
	return f.responder(r), nil
}

func newTestCache(t *testing.T, responder func(r *http.Request) *http.Response) (*Cache, *fakeAuthorizer, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	fake := &fakeAuthorizer{responder: responder}
	cache := New(Config{
		Authorizer:    fake,
		SessionCookie: "session",
		Now:           clock.now,
	})
	return cache, fake, clock
}

func makeResponse(status int, header http.Header, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func allowResponder(header http.Header) func(r *http.Request) *http.Response {
	return func(r *http.Request) *http.Response {
		return makeResponse(http.StatusOK, header, "ok")
	}
}

func newRequest(method, target, auth string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

func withSession(r *http.Request, value string) *http.Request {
	r.AddCookie(&http.Cookie{Name: "session", Value: value})
	return r
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	resp.Body.Close()
	return string(data)
}

func statusOf(resp *http.Response) int {
	if resp == nil {
		return -1
	}
	return resp.StatusCode
}

func (c *Cache) entryCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, variants := range c.variants {
		n += len(variants)
	}
	return n
}

func TestAuthorizeCachesAndReusesBody(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, allowResponder(http.Header{}))
	target := "https://gateway.example/admin"

	first, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer alice-token"))
	if err != nil {
		t.Fatalf("first Authorize: %v", err)
	}
	if first.StatusCode != http.StatusOK || readBody(t, first) != "ok" {
		t.Fatalf("unexpected first response: %+v", first)
	}

	second, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer alice-token"))
	if err != nil {
		t.Fatalf("second Authorize: %v", err)
	}
	if readBody(t, second) != "ok" {
		t.Fatalf("cached body must remain readable")
	}
	if fake.calls != 1 {
		t.Fatalf("authorizer calls = %d, want 1", fake.calls)
	}
}

func TestSwitchingUserDoesNotReuseDecision(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, func(r *http.Request) *http.Response {
		if r.Header.Get("Authorization") == "Bearer alice-token" {
			return makeResponse(http.StatusOK, http.Header{}, "ok")
		}
		return makeResponse(http.StatusForbidden, http.Header{}, "forbidden")
	})
	target := "https://gateway.example/admin"

	resp, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer alice-token"))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("alice allow: status=%d err=%v", statusOf(resp), err)
	}

	resp, err = cache.Authorize(newRequest(http.MethodGet, target, "Bearer bob-token"))
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bob must not inherit alice's cached allow: status=%d err=%v", statusOf(resp), err)
	}
	if fake.calls != 2 {
		t.Fatalf("authorizer calls = %d, want 2", fake.calls)
	}
	if cache.entryCount() != 2 {
		t.Fatalf("entry count = %d, want 2 per-user entries", cache.entryCount())
	}
}

func TestSwitchingMethodDoesNotReuseDecision(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, allowResponder(http.Header{}))
	target := "https://gateway.example/resources/1"

	if resp, err := cache.Authorize(newRequest(http.MethodGet, target, "Bearer alice-token")); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status=%d err=%v", statusOf(resp), err)
	}
	if resp, err := cache.Authorize(newRequest(http.MethodPost, target, "Bearer alice-token")); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST: status=%d err=%v", statusOf(resp), err)
	}
	if fake.calls != 2 {
		t.Fatalf("authorizer calls = %d, want 2 (method is part of the key)", fake.calls)
	}

	keyGet, _ := cache.BaseKey(newRequest(http.MethodGet, target, "Bearer alice-token"))
	keyPost, _ := cache.BaseKey(newRequest(http.MethodPost, target, "Bearer alice-token"))
	if keyGet == keyPost {
		t.Fatalf("GET and POST produced identical cache keys")
	}
}

func TestPathEncodingBypass(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, allowResponder(http.Header{}))

	plain := newRequest(http.MethodGet, "https://gateway.example/a/b/../c", "Bearer alice-token")
	encoded := newRequest(http.MethodGet, "https://gateway.example/a/b/%2e%2e/c", "Bearer alice-token")

	if resp, err := cache.Authorize(plain); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("plain path: status=%d err=%v", statusOf(resp), err)
	}
	if resp, err := cache.Authorize(encoded); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("encoded path: status=%d err=%v", statusOf(resp), err)
	}
	if fake.calls != 1 {
		t.Fatalf("authorizer calls = %d, want 1 (encoded dot segments normalize together)", fake.calls)
	}

	keyPlain, _ := cache.BaseKey(plain)
	keyEncoded, _ := cache.BaseKey(encoded)
	if keyPlain != keyEncoded {
		t.Fatalf("normalized keys differ:\n%q\n%q", keyPlain, keyEncoded)
	}

	slash := newRequest(http.MethodGet, "https://gateway.example/a%2Fb", "Bearer alice-token")
	if _, err := cache.Authorize(slash); err != ErrEncodedSlash {
		t.Fatalf("encoded slash error = %v, want ErrEncodedSlash", err)
	}
	if fake.calls != 1 {
		t.Fatalf("rejected request must not reach the authorizer, calls = %d, want 1", fake.calls)
	}
}

func TestRequestWithoutCredentialsIsNotCached(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, allowResponder(http.Header{}))
	target := "https://gateway.example/public"

	if resp, err := cache.Authorize(newRequest(http.MethodGet, target, "")); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous: status=%d err=%v", statusOf(resp), err)
	}
	if resp, err := cache.Authorize(newRequest(http.MethodGet, target, "")); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous retry: status=%d err=%v", statusOf(resp), err)
	}
	if fake.calls != 2 {
		t.Fatalf("authorizer calls = %d, want 2 (credential-less requests bypass cache)", fake.calls)
	}
	if cache.entryCount() != 0 {
		t.Fatalf("entry count = %d, want 0", cache.entryCount())
	}

	sessionTarget := "https://gateway.example/session-only"
	if resp, err := cache.Authorize(withSession(newRequest(http.MethodGet, sessionTarget, ""), "sess-1")); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("session: status=%d err=%v", statusOf(resp), err)
	}
	if resp, err := cache.Authorize(withSession(newRequest(http.MethodGet, sessionTarget, ""), "sess-1")); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("session retry: status=%d err=%v", statusOf(resp), err)
	}
	if fake.calls != 3 {
		t.Fatalf("authorizer calls = %d, want 3 (session cookie is a cacheable credential)", fake.calls)
	}
}

func TestHostQueryAndSessionIsolation(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, allowResponder(http.Header{}))

	base := newRequest(http.MethodGet, "https://gateway.example/a?x=1", "Bearer t")
	otherHost := newRequest(http.MethodGet, "https://evil.example/a?x=1", "Bearer t")
	otherQuery := newRequest(http.MethodGet, "https://gateway.example/a?x=2", "Bearer t")

	keyBase, _ := cache.BaseKey(base)
	keyHost, _ := cache.BaseKey(otherHost)
	keyQuery, _ := cache.BaseKey(otherQuery)
	if keyBase == keyHost || keyBase == keyQuery {
		t.Fatalf("host and query must be part of the key")
	}

	sessionA := withSession(newRequest(http.MethodGet, "https://gateway.example/a", ""), "sess-a")
	sessionB := withSession(newRequest(http.MethodGet, "https://gateway.example/a", ""), "sess-b")
	keyA, _ := cache.BaseKey(sessionA)
	keyB, _ := cache.BaseKey(sessionB)
	if keyA == keyB {
		t.Fatalf("different session cookies produced identical keys")
	}

	for _, r := range []*http.Request{base, otherHost, otherQuery, sessionA, sessionB} {
		if _, err := cache.Authorize(r); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
	}
	if fake.calls != 5 {
		t.Fatalf("authorizer calls = %d, want 5 distinct keys", fake.calls)
	}
}

func TestKeyHashesCredentials(t *testing.T) {
	t.Parallel()

	cache, _, _ := newTestCache(t, allowResponder(http.Header{}))
	token := "super-secret-token-value"
	key, err := cache.BaseKey(newRequest(http.MethodGet, "https://gateway.example/a", "Bearer "+token))
	if err != nil {
		t.Fatalf("BaseKey: %v", err)
	}
	if strings.Contains(key, token) {
		t.Fatalf("cache key must store a hash, not the raw Authorization value: %q", key)
	}
	if !strings.Contains(key, fingerprint("Bearer "+token)) {
		t.Fatalf("cache key must contain the SHA-256 fingerprint of the credential")
	}
}
