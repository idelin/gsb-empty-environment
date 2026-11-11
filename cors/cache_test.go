package cors

import (
	"net/http"
	"testing"
	"time"
)

const sourceOrigin = "https://browser.example"

// A safelisted GET never triggers a preflight, and the business request is
// still executed with its counts tracked by the real server.
func TestSafelistedRequestSkipsPreflight(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) { c.echo() })

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	body, err := doRequest(t, client, server, http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if body != "ok:/resource" {
		t.Fatalf("unexpected body %q", body)
	}
	expectCounts(t, server, 0, 1)
}

// Anonymous mode accepts ACAO "*" and reuses one preflight for repeated
// requests sharing the same identity, method and header tuple.
func TestAnonymousWildcardIsCached(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.wildcard().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	for i := 0; i < 3; i++ {
		if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	expectCounts(t, server, 1, 3)
	if corsTransport.EntryCount() != 1 {
		t.Fatalf("cache entries = %d, want 1", corsTransport.EntryCount())
	}
}

// A wildcard origin must never be accepted for credentialed requests: this is
// the cross-identity reuse boundary enforced by the cache key's credentials
// component.
func TestCredentialedRejectsWildcardAndUsesSeparateEntry(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.wildcard().credentials().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})

	anonClient := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)
	if _, err := doRequest(t, anonClient, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}

	credClient := newClient(Policy{Origin: sourceOrigin, Mode: ModeInclude}, corsTransport)
	_, err := doRequest(t, credClient, server, http.MethodPut, map[string]string{"X-Test": "1"})
	expectPreflightError(t, err)

	// The credentialed failure must not have poisoned the anonymous decision.
	if _, err := doRequest(t, anonClient, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 2, 2)
}

// Credentialed mode requires both an echoed origin and ACA-credentials, and
// its cached decision is partitioned from the anonymous one.
func TestCredentialedEchoCachesSeparately(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().credentials().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	anonClient := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)
	credClient := newClient(Policy{Origin: sourceOrigin, Mode: ModeInclude}, corsTransport)

	for i := 0; i < 2; i++ {
		if _, err := doRequest(t, anonClient, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := doRequest(t, credClient, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
			t.Fatal(err)
		}
	}

	// One preflight per credentials mode; four business requests.
	expectCounts(t, server, 2, 4)
	if corsTransport.EntryCount() != 2 {
		t.Fatalf("cache entries = %d, want 2", corsTransport.EntryCount())
	}
}

// Different source origins are different identities even against the same
// target, so each gets its own preflight and cache entry.
func TestOriginsArePartitioned(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodDelete).headers("x-test").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	clientA := newClient(Policy{Origin: "https://a.example", Mode: ModeOmit}, corsTransport)
	clientB := newClient(Policy{Origin: "https://b.example", Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, clientA, server, http.MethodDelete, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := doRequest(t, clientB, server, http.MethodDelete, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := doRequest(t, clientA, server, http.MethodDelete, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}

	expectCounts(t, server, 2, 3)
	if corsTransport.EntryCount() != 2 {
		t.Fatalf("cache entries = %d, want 2", corsTransport.EntryCount())
	}
}

// Method and header permissions are part of the cached decision: a decision
// for PUT/x-test cannot authorize DELETE or another header.
func TestMethodAndHeaderPermissionsAreEnforced(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	// Same identity, but a new method: a fresh preflight is required and the
	// server rejects DELETE.
	_, err := doRequest(t, client, server, http.MethodDelete, map[string]string{"X-Test": "1"})
	expectPreflightError(t, err)

	// Same method, different header: another fresh preflight, also rejected.
	_, err = doRequest(t, client, server, http.MethodPut, map[string]string{"X-Other": "1"})
	expectPreflightError(t, err)

	// The original, still-valid decision remains usable.
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}

	expectCounts(t, server, 3, 2)
}

// Expired entries are not reused: after max-age elapses a new preflight runs.
func TestExpiryWithInjectedClock(t *testing.T) {
	clock := newFakeClock()
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("10")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport(), Clock: clock.Now})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(9 * time.Second)
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 1, 2)

	clock.Advance(2 * time.Second)
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 2, 3)
}

// Expiry is also verified against a real wall clock and real server, without
// injecting a clock, to prove the TTL path works end to end.
func TestExpiryWithRealClock(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("1")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 2, 2)
}
