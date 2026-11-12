package authcache

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type recordingTransport struct {
	method  string
	url     string
	auth    string
	cookie  string
	query   string
	counter int
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.counter++
	t.method = req.Method
	t.url = req.URL.String()
	t.auth = req.Header.Get("Authorization")
	t.cookie = req.Header.Get("Cookie")
	t.query = req.URL.RawQuery
	header := http.Header{}
	header.Set("Cache-Control", "max-age=20")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("allowed")),
	}, nil
}

func TestVaryHeaderSelectsVariant(t *testing.T) {
	t.Parallel()

	cache, fake, _ := newTestCache(t, func(r *http.Request) *http.Response {
		header := http.Header{}
		header.Set("Vary", "X-Tenant")
		if r.Header.Get("X-Tenant") == "tenant-a" {
			return makeResponse(http.StatusOK, header, "a")
		}
		return makeResponse(http.StatusForbidden, header, "b")
	})
	target := "https://gateway.example/tenant"

	reqA := newRequest(http.MethodGet, target, "Bearer token")
	reqA.Header.Set("X-Tenant", "tenant-a")
	reqB := newRequest(http.MethodGet, target, "Bearer token")
	reqB.Header.Set("X-Tenant", "tenant-b")

	resp, err := cache.Authorize(reqA)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-a: status=%d err=%v", statusOf(resp), err)
	}
	resp, err = cache.Authorize(reqB)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant-b must not hit tenant-a's cached variant: status=%d err=%v", statusOf(resp), err)
	}

	resp, err = cache.Authorize(reqA.Clone(reqA.Context()))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-a retry: status=%d err=%v", statusOf(resp), err)
	}
	if readBody(t, resp) != "a" {
		t.Fatalf("cached variant body mismatch")
	}
	if fake.calls != 2 {
		t.Fatalf("authorizer calls = %d, want 2 variants", fake.calls)
	}

	base, _ := cache.BaseKey(reqA)
	cache.mu.RLock()
	variants := cache.variants[base]
	cache.mu.RUnlock()
	if len(variants) != 2 {
		t.Fatalf("variant count = %d, want 2", len(variants))
	}
}

func TestMultipleVaryHeadersCanonicalized(t *testing.T) {
	t.Parallel()

	header := http.Header{}
	header.Add("Vary", "x-tenant")
	header.Add("Vary", "X-Tenant, x-region")
	cache, fake, _ := newTestCache(t, makeResponder(http.StatusOK, header))
	target := "https://gateway.example/multi"

	req := newRequest(http.MethodGet, target, "Bearer token")
	req.Header.Set("X-Tenant", "t1")
	req.Header.Set("X-Region", "cn")
	if _, err := cache.Authorize(req); err != nil {
		t.Fatalf("first Authorize: %v", err)
	}
	if _, err := cache.Authorize(req.Clone(req.Context())); err != nil {
		t.Fatalf("second Authorize: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("duplicate/canonical Vary names should collapse to one variant, calls = %d", fake.calls)
	}

	other := newRequest(http.MethodGet, target, "Bearer token")
	other.Header.Set("X-Tenant", "t1")
	other.Header.Set("X-Region", "eu")
	if _, err := cache.Authorize(other); err != nil {
		t.Fatalf("other region Authorize: %v", err)
	}
	if fake.calls != 2 {
		t.Fatalf("changed varied header value must miss the cache, calls = %d", fake.calls)
	}
}

func TestHTTPAuthorizerForwardsAndCaches(t *testing.T) {
	t.Parallel()

	transport := &recordingTransport{}
	authorizer, err := NewHTTPAuthorizer("https://authz.internal/check", &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("NewHTTPAuthorizer: %v", err)
	}
	cache := New(Config{Authorizer: authorizer})

	req := newRequest(http.MethodPost, "https://gateway.example/api?token=raw", "Bearer end-to-end")
	withSession(req, "session-id")

	resp, err := cache.Authorize(req)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if resp.StatusCode != http.StatusOK || readBody(t, resp) != "allowed" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if transport.method != http.MethodPost || transport.auth != "Bearer end-to-end" {
		t.Fatalf("subrequest forwarded method=%q auth=%q", transport.method, transport.auth)
	}
	if transport.cookie == "" || !strings.Contains(transport.cookie, "session-id") {
		t.Fatalf("session cookie not forwarded: %q", transport.cookie)
	}
	if transport.query != "" {
		t.Fatalf("auth subrequest must not forward the original query string, got %q", transport.query)
	}
	if transport.url != "https://authz.internal/check" {
		t.Fatalf("subrequest URL = %q", transport.url)
	}

	resp2, err := cache.Authorize(req.Clone(req.Context()))
	if err != nil {
		t.Fatalf("cached Authorize: %v", err)
	}
	if readBody(t, resp2) != "allowed" {
		t.Fatalf("cached body mismatch")
	}
	if cache.entryCount() != 1 {
		t.Fatalf("entry count = %d, want 1", cache.entryCount())
	}
	if transport.counter != 1 {
		t.Fatalf("subrequest count = %d, want 1 (second call served from cache)", transport.counter)
	}
}
