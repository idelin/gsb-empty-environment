package cors

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// Concurrent identical requests are coalesced: exactly one preflight reaches
// the real server while every business request still runs.
func TestConcurrentIdenticalRequestsCoalesce(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	// Gate the server's preflight responses so all goroutines are in flight at
	// once, exercising the inflight dedup rather than the warm cache.
	gate := make(chan struct{})
	server.handler = gateHandler(server.handle, gate)

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	const n = 25
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"})
		}(i)
	}

	// Give the goroutines time to arrive, then release the preflight handler.
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	expectCounts(t, server, 1, n)
}

// Concurrent requests with different identities must not be coalesced: each
// identity tuple needs its own preflight.
func TestConcurrentDifferentIdentitiesDoNotCoalesce(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	gate := make(chan struct{})
	server.handler = gateHandler(server.handle, gate)

	corsTransport := NewCORSTransport(Config{Base: server.transport()})

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			origin := "https://tenant-" + string(rune('a'+i)) + ".example"
			client := newClient(Policy{Origin: origin, Mode: ModeOmit}, corsTransport)
			_, errs[i] = doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"})
		}(i)
	}

	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	expectCounts(t, server, n, n)
}

// A policy revocation followed by Invalidate forces revalidation: the stale
// decision cannot be reused, and a concurrently revoked cache is likewise
// re-fetched rather than served stale.
func TestRevocationForcesRevalidation(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("600")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 1, 2)

	// Revoke the single identity tuple (e.g. permission withdrawn server side).
	corsTransport.Invalidate(sourceOrigin, server.URL, ModeOmit)
	if corsTransport.EntryCount() != 0 {
		t.Fatalf("cache entries = %d, want 0 after Invalidate", corsTransport.EntryCount())
	}
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 2, 3)

	// Revoke by source origin across all targets and modes.
	corsTransport.InvalidateOrigin(sourceOrigin)
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 3, 4)

	// Clear wipes everything as the last-resort revocation.
	corsTransport.Clear()
	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 4, 5)
}

// A preflight that is denied is never cached: retrying the same request hits
// the server again instead of replaying a stored denial.
func TestDeniedPreflightIsNotCached(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").failPreflight()
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	for i := 0; i < 3; i++ {
		_, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"})
		expectPreflightError(t, err)
	}
	expectCounts(t, server, 3, 0)
	if corsTransport.EntryCount() != 0 {
		t.Fatalf("cache entries = %d, want 0", corsTransport.EntryCount())
	}

	// Once the server starts allowing the request, it succeeds immediately with
	// no stale denial blocking it.
	server.mu.Lock()
	server.failPreflight = false
	server.mu.Unlock()

	if _, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 4, 1)
}

// ACA-headers "*" only authorizes wildcard matching in anonymous mode; the
// credentialed entry must still enumerate each header explicitly.
func TestWildcardHeaderOnlyAppliesWithoutCredentials(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.wildcard().methods(http.MethodPut).headers("*").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	anonClient := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	// First request stores an entry; a second, different custom header is still
	// authorized by the wildcard without another preflight.
	if _, err := doRequest(t, anonClient, server, http.MethodPut, map[string]string{"X-First": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := doRequest(t, anonClient, server, http.MethodPut, map[string]string{"X-Second": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 1, 2)

	// Credentialed mode cannot use wildcard origins here at all, so it fails
	// rather than inheriting the anonymous entry.
	credClient := newClient(Policy{Origin: sourceOrigin, Mode: ModeInclude}, corsTransport)
	_, err := doRequest(t, credClient, server, http.MethodPut, map[string]string{"X-First": "1"})
	expectPreflightError(t, err)
}

func gateHandler(next http.HandlerFunc, gate <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		next(w, r)
	})
}
