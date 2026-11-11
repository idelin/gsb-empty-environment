package cors

import (
	"net/http"
	"testing"
)

// A cross-origin redirect changes the target identity: each hop is evaluated
// with its own cache key, and the second hop must not inherit the first hop's
// preflight decision.
func TestRedirectRePreflightsNewTarget(t *testing.T) {
	final := newTestServer()
	defer final.Close()
	final.URL = "http://cors-final.test"
	final.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	entry := newTestServer()
	defer entry.Close()
	entry.URL = "http://cors-entry.test"
	entry.configure(func(c *testServerConfig) {
		c.echo().
			methods(http.MethodPut).
			headers("x-test").
			maxAgeSeconds("60").
			redirectTo(final.URL + "/resource")
	})

	corsTransport := NewCORSTransport(Config{Base: multiServerTransport(entry, final)})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	// First request: preflight against the entry server, redirect, preflight
	// against the final server, then the business request on each hop.
	if _, err := doRequest(t, client, entry, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	if got := entry.preflights.Load(); got != 1 {
		t.Fatalf("entry preflight count = %d, want 1", got)
	}
	if got := final.preflights.Load(); got != 1 {
		t.Fatalf("final preflight count = %d, want 1", got)
	}
	if got := entry.requests.Load(); got != 1 {
		t.Fatalf("entry business count = %d, want 1", got)
	}
	if got := final.requests.Load(); got != 1 {
		t.Fatalf("final business count = %d, want 1", got)
	}

	// Repeat: each target serves its own cached decision, no new preflights.
	if _, err := doRequest(t, client, entry, http.MethodPut, map[string]string{"X-Test": "1"}); err != nil {
		t.Fatal(err)
	}
	if got := entry.preflights.Load(); got != 1 {
		t.Fatalf("entry preflight count = %d, want 1", got)
	}
	if got := final.preflights.Load(); got != 1 {
		t.Fatalf("final preflight count = %d, want 1", got)
	}
	if got := final.requests.Load(); got != 2 {
		t.Fatalf("final business count = %d, want 2", got)
	}

	if corsTransport.EntryCount() != 2 {
		t.Fatalf("cache entries = %d, want 2 (one per target)", corsTransport.EntryCount())
	}
}

// If the redirect target denies the preflight, the request fails even though
// the first hop was allowed: a positive decision must never be carried across
// the redirect boundary.
func TestRedirectDeniedBySecondTarget(t *testing.T) {
	final := newTestServer()
	defer final.Close()
	final.URL = "http://cors-final.test"
	final.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").failPreflight()
	})

	entry := newTestServer()
	defer entry.Close()
	entry.URL = "http://cors-entry.test"
	entry.configure(func(c *testServerConfig) {
		c.echo().
			methods(http.MethodPut).
			headers("x-test").
			maxAgeSeconds("60").
			redirectTo(final.URL + "/resource")
	})

	corsTransport := NewCORSTransport(Config{Base: multiServerTransport(entry, final)})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	_, err := doRequest(t, client, entry, http.MethodPut, map[string]string{"X-Test": "1"})
	expectPreflightError(t, err)

	if got := entry.preflights.Load(); got != 1 {
		t.Fatalf("entry preflight count = %d, want 1", got)
	}
	if got := final.preflights.Load(); got != 1 {
		t.Fatalf("final preflight count = %d, want 1", got)
	}
	if got := final.requests.Load(); got != 0 {
		t.Fatalf("final business count = %d, want 0", got)
	}

	// The denied hop was not cached; retrying re-validates with the final server.
	_, err = doRequest(t, client, entry, http.MethodPut, map[string]string{"X-Test": "1"})
	expectPreflightError(t, err)
	if got := final.preflights.Load(); got != 2 {
		t.Fatalf("final preflight count = %d, want 2", got)
	}
}
