package netsec_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	netsec "example.org/netsec/q0035"
)

func newRequest(proto string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://example.test/account?id=42", nil)
	r.Header.Set(netsec.HeaderXForwardedProto, proto)
	r.Header.Set(netsec.HeaderXForwardedFor, "203.0.113.9")
	r.Header.Set(netsec.HeaderXForwardedHost, "internal.example.test")
	r.Header.Set("Accept", "text/html")
	return r
}

func TestSchemeHeaderSeparatesCacheEntries(t *testing.T) {
	httpRequest := newRequest("http")
	httpsRequest := newRequest("https")

	httpKey := netsec.CacheKey(httpRequest)
	httpsKey := netsec.CacheKey(httpsRequest)
	if httpKey == httpsKey {
		t.Fatalf("http and https requests share cache key %q", httpKey)
	}

	cache := map[string]string{}
	cache[httpKey] = "plaintext"
	cache[httpsKey] = "secure"
	if len(cache) != 2 {
		t.Fatalf("cache has %d entries, want 2", len(cache))
	}
}

func TestSchemeHeaderNormalization(t *testing.T) {
	upper := netsec.CacheKey(newRequest("HTTP"))
	lower := netsec.CacheKey(newRequest("http"))
	if upper != lower {
		t.Fatal("scheme header values differing only by case must share a key")
	}

	if upper == netsec.CacheKey(newRequest("https")) {
		t.Fatal("http and https must not share a key")
	}
}

func TestVarySchemeHeaderIsKeyedAndForwarded(t *testing.T) {
	p := netsec.NewPolicy(netsec.HeaderXForwardedProto, false)
	httpRequest := newRequest("http")
	httpsRequest := newRequest("https")
	vary := []string{netsec.HeaderXForwardedProto}

	if p.Key(httpRequest, nil) != p.Key(httpsRequest, nil) {
		t.Fatal("test setup: unkeyed scheme header should initially share a key")
	}
	if p.Key(httpRequest, vary) == p.Key(httpsRequest, vary) {
		t.Fatal("Vary-listed scheme header did not enter the cache key")
	}

	decision := p.Decide(httpRequest, vary)
	if !contains(decision.KeyedHeaders, netsec.HeaderXForwardedProto) {
		t.Fatalf("keyed headers = %v, want %s", decision.KeyedHeaders, netsec.HeaderXForwardedProto)
	}
	if !contains(decision.ForwardedHeaders, netsec.HeaderXForwardedProto) {
		t.Fatalf("forwarded headers = %v, want %s", decision.ForwardedHeaders, netsec.HeaderXForwardedProto)
	}
	if !decision.CanStore {
		t.Fatal("Vary without * should be cacheable")
	}
}

func TestUnkeyedForwardingHeadersAreStripped(t *testing.T) {
	p := netsec.NewPolicy(netsec.HeaderXForwardedProto, false)
	r := newRequest("http")
	r.Header.Set(netsec.HeaderForwarded, "for=203.0.113.9;proto=http")

	forwarded := p.ForwardHeaders(r, nil)
	for _, name := range []string{
		netsec.HeaderXForwardedProto,
		netsec.HeaderXForwardedFor,
		netsec.HeaderXForwardedHost,
		netsec.HeaderForwarded,
	} {
		if values := forwarded.Values(name); len(values) != 0 {
			t.Fatalf("unkeyed forwarding header %s leaked to origin: %v", name, values)
		}
	}
	if got := forwarded.Get("Accept"); got != "text/html" {
		t.Fatalf("Accept = %q, want text/html", got)
	}
}

func TestKeyedForwardingHeaderIsForwarded(t *testing.T) {
	p := netsec.DefaultPolicy()
	r := newRequest("https")

	forwarded := p.ForwardHeaders(r, nil)
	if got := forwarded.Get(netsec.HeaderXForwardedProto); got != "https" {
		t.Fatalf("%s = %q, want https", netsec.HeaderXForwardedProto, got)
	}
	if values := forwarded.Values(netsec.HeaderXForwardedFor); len(values) != 0 {
		t.Fatalf("unkeyed %s leaked to origin: %v", netsec.HeaderXForwardedFor, values)
	}
}

func TestForwardingHelpersShareDefaultPolicy(t *testing.T) {
	r := newRequest("https")

	defaultForwarded := netsec.ForwardHeaders(r)
	if got := defaultForwarded.Get(netsec.HeaderXForwardedProto); got != "https" {
		t.Fatalf("default %s = %q, want https", netsec.HeaderXForwardedProto, got)
	}

	strict := netsec.Policy{KeyedHeaders: nil}.ForwardHeaders(r, nil)
	if values := strict.Values(netsec.HeaderXForwardedProto); len(values) != 0 {
		t.Fatalf("unkeyed scheme header forwarded: %v", values)
	}
}

func TestVarySelectsOrdinaryHeader(t *testing.T) {
	p := netsec.NewPolicy("", false)
	english := newRequest("https")
	english.Header.Set("Accept-Language", "en")
	french := newRequest("https")
	french.Header.Set("Accept-Language", "fr")

	if p.Key(english, nil) != p.Key(french, nil) {
		t.Fatal("test setup: unkeyed Accept-Language should initially share a key")
	}
	vary := []string{"Accept-Language"}
	if p.Key(english, vary) == p.Key(french, vary) {
		t.Fatal("Vary-listed Accept-Language did not enter the cache key")
	}
}

func TestAbsentVaryHeaderIsDistinctFromEmptyValue(t *testing.T) {
	p := netsec.NewPolicy("", false)
	vary := []string{"X-Tenant"}

	absent := newRequest("https")
	empty := newRequest("https")
	empty.Header["X-Tenant"] = []string{""}

	if p.Key(absent, vary) == p.Key(empty, vary) {
		t.Fatal("absent Vary header must not collide with an explicitly empty value")
	}
}

func TestVaryStarIsNotCacheable(t *testing.T) {
	if netsec.VaryAllowsStorage("Accept, *") {
		t.Fatal("Vary: * must forbid shared caching")
	}
	decision := netsec.DefaultPolicy().Decide(newRequest("https"), []string{"*"})
	if decision.CanStore {
		t.Fatal("Decision.CanStore must be false for Vary: *")
	}
	names := netsec.VaryNames("Accept, *")
	if contains(names, "*") {
		t.Fatalf("VaryNames returned *: %v", names)
	}
	if !contains(names, "Accept") {
		t.Fatalf("VaryNames = %v, want Accept", names)
	}
}

func TestHopByHopHeadersAreNeverForwarded(t *testing.T) {
	r := newRequest("https")
	r.Header.Set("Connection", "X-Internal")
	r.Header.Set("X-Internal", "secret")
	r.Header.Set("Te", "trailers")

	forwarded := netsec.DefaultPolicy().ForwardHeaders(r, nil)
	for _, name := range []string{"Connection", "X-Internal", "Te"} {
		if values := forwarded.Values(name); len(values) != 0 {
			t.Fatalf("hop-by-hop header %s leaked to origin: %v", name, values)
		}
	}
}

func TestPrepareRequestDoesNotMutateOriginal(t *testing.T) {
	p := netsec.NewPolicy("", false)
	r := newRequest("http")
	prepared := p.PrepareRequest(r, nil)

	if values := r.Header.Values(netsec.HeaderXForwardedProto); len(values) != 1 {
		t.Fatalf("original request was mutated: %v", values)
	}
	if values := prepared.Header.Values(netsec.HeaderXForwardedProto); len(values) != 0 {
		t.Fatalf("unkeyed header survived PrepareRequest: %v", values)
	}
}

func TestShouldForwardHeader(t *testing.T) {
	if !netsec.IsForwardingHeader("x-forwarded-proto") {
		t.Fatal("x-forwarded-proto should be classified as forwarding")
	}
	if netsec.ShouldForwardHeader(netsec.HeaderXForwardedFor, nil) {
		t.Fatal("unkeyed X-Forwarded-For must not be forwarded")
	}
	if !netsec.ShouldForwardHeader(netsec.HeaderXForwardedFor, []string{"x-forwarded-for"}) {
		t.Fatal("keyed X-Forwarded-For should be forwarded")
	}
	if !netsec.ShouldForwardHeader("Accept", nil) {
		t.Fatal("ordinary headers should be forwarded")
	}
}

func TestScheme(t *testing.T) {
	tlsRequest := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	if got := netsec.Scheme(tlsRequest); got != "https" {
		t.Fatalf("Scheme = %q, want https", got)
	}
	plaintext := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	if got := netsec.Scheme(plaintext); got != "http" {
		t.Fatalf("Scheme = %q, want http", got)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
