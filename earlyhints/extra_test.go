package earlyhints

import (
	"net/http"
	"testing"
)

func TestNonGETAndNon2xxAreNotCached(t *testing.T) {
	cache := newMemoryCache()

	postReq, err := http.NewRequest(http.MethodPost, "https://example.test/submit", nil)
	if err != nil {
		t.Fatal(err)
	}
	postTransport := &scriptTransport{script: upstreamScript{
		finalHeader: http.Header{},
		finalBody:   "created",
	}}
	NewHandler(cache, map[string]bool{"cdn.example.com": true}, 0).
		WithTransport(postTransport).
		ServeHTTP(newHintResponseWriter(), postReq)
	if cache.stores != 0 {
		t.Fatalf("POST response must not be cached by the default policy: %d stores", cache.stores)
	}

	getReq := newProxyRequest(1, 1)
	errorTransport := &scriptTransport{script: upstreamScript{
		finalStatus: http.StatusNotFound,
		finalHeader: http.Header{},
		finalBody:   "missing",
	}}
	NewHandler(cache, map[string]bool{"cdn.example.com": true}, 0).
		WithTransport(errorTransport).
		ServeHTTP(newHintResponseWriter(), getReq)
	if cache.stores != 0 {
		t.Fatalf("non-2xx response must not be cached: %d stores", cache.stores)
	}
}

func TestCustomCachePolicyControlsStore(t *testing.T) {
	cache := newMemoryCache()
	transport := &scriptTransport{script: upstreamScript{
		finalStatus: http.StatusNotFound,
		finalHeader: http.Header{},
		finalBody:   "missing",
	}}
	policy := func(req *http.Request, resp *http.Response, body []byte) (string, bool) {
		if resp.StatusCode == http.StatusNotFound {
			key, _ := defaultKey(req)
			return key, true
		}
		return "", false
	}
	handler := NewHandler(cache, map[string]bool{"cdn.example.com": true}, 0).
		WithTransport(transport).
		WithCachePolicy(policy)

	first := newHintResponseWriter()
	handler.ServeHTTP(first, newProxyRequest(1, 1))
	if cache.stores != 1 {
		t.Fatalf("custom policy should cache the 404, got %d stores", cache.stores)
	}

	// The second request is served from the cache without touching transport.
	called := false
	handler.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errBoom
	}))
	second := newHintResponseWriter()
	handler.ServeHTTP(second, newProxyRequest(1, 1))
	if called {
		t.Fatal("cache lookup must short-circuit the transport")
	}
	if second.statusCode != http.StatusNotFound || second.body.String() != "missing" {
		t.Fatalf("cached 404 mismatch: %d %q", second.statusCode, second.body.String())
	}
}

func TestWithTransportNilFallsBackToDefault(t *testing.T) {
	handler := NewHandler(newMemoryCache(), map[string]bool{"cdn.example.com": true}, 0)
	handler.WithTransport(nil)
	if handler.transport != http.DefaultTransport {
		t.Fatalf("nil transport must fall back to http.DefaultTransport, got %v", handler.transport)
	}
}

func TestQuotedAndEscapedLinkParameters(t *testing.T) {
	links := ParseLinkHeader([]string{
		`<https://cdn.example.com/a.js>; rel=preload; title="a; b\"c"`,
	})
	if len(links) != 1 {
		t.Fatalf("expected one link, got %+v", links)
	}
	if links[0].Params["title"] != `a; b"c` {
		t.Fatalf("escaped quoted parameter mishandled: %+v", links[0])
	}
	safe := FilterLinkValues(links, map[string]bool{"cdn.example.com": true})
	if len(safe) != 1 {
		t.Fatalf("expected one safe link, got %v", safe)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

var errBoom = http.ErrServerClosed
