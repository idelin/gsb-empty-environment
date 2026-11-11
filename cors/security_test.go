package cors

import (
	"net/http"
	"testing"
	"time"
)

// TestCrossIdentityReuseRegression is the direct regression test for the
// original vulnerability: a preflight decision obtained by one browsing
// identity must never authorize a request made by another identity.
//
// It walks every axis of the cache key and the stored permissions, asserting
// that each change forces a fresh preflight against the real server.
func TestCrossIdentityReuseRegression(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().credentials().
			methods(http.MethodPut, http.MethodDelete).
			headers("x-tenant-a", "x-tenant-b").
			maxAgeSeconds("600")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})

	scenarios := []struct {
		name   string
		policy Policy
	}{
		{"baseline tenant A omit", Policy{Origin: "https://a.example", Mode: ModeOmit}},
		{"different source origin", Policy{Origin: "https://b.example", Mode: ModeOmit}},
		{"different credentials mode", Policy{Origin: "https://a.example", Mode: ModeInclude}},
		{"third source origin", Policy{Origin: "https://c.example", Mode: ModeOmit}},
		{"third origin credentialed", Policy{Origin: "https://c.example", Mode: ModeInclude}},
	}

	// First establish the baseline.
	baseline := newClient(scenarios[0].policy, corsTransport)
	if _, err := doRequest(t, baseline, server, http.MethodPut, map[string]string{"X-Tenant-A": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 1, 1)

	// Each subsequent scenario must trigger its own preflight rather than reuse
	// the baseline's cached decision.
	for i, sc := range scenarios[1:] {
		client := newClient(sc.policy, corsTransport)
		if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Tenant-A": "1"}); err != nil {
			t.Fatalf("%s: %v", sc.name, err)
		}
		wantPreflights := int64(i + 2)
		if got := server.preflights.Load(); got != wantPreflights {
			t.Fatalf("%s: preflight count = %d, want %d (decision was reused across identities)", sc.name, got, wantPreflights)
		}
	}

	// Replaying the baseline still uses its own single cached entry.
	if _, err := doRequest(t, baseline, server, http.MethodPut, map[string]string{"X-Tenant-A": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 5, 6)

	// Five distinct identity/permission tuples are held independently.
	if corsTransport.EntryCount() != 5 {
		t.Fatalf("cache entries = %d, want 5", corsTransport.EntryCount())
	}
}

// Same-origin requests are not CORS requests: no Origin header, no preflight,
// regardless of the method or headers used.
func TestSameOriginNeedsNoPreflight(t *testing.T) {
	server := newTestServer()
	defer server.Close()

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: server.URL, Mode: ModeInclude}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodDelete, map[string]string{"X-Custom": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 0, 1)
	if corsTransport.EntryCount() != 0 {
		t.Fatalf("cache entries = %d, want 0", corsTransport.EntryCount())
	}
}

// A non-2xx preflight response is a hard failure and is not cached.
func TestPreflightNonOKStatusFails(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").failPreflight()
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	_, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"})
	expectPreflightError(t, err)
	expectCounts(t, server, 1, 0)
}

// An overly large max-age is capped rather than trusted blindly.
func TestMaxAgeIsCapped(t *testing.T) {
	clock := newFakeClock()
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("999999999")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport(), Clock: clock.Now})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(maxPreflightTTL - time.Nanosecond)
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	// At the cap boundary the entry is still valid.
	expectCounts(t, server, 1, 2)

	clock.Advance(time.Nanosecond)
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 2, 3)
}
