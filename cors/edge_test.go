package cors

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// A safelisted content-type on a POST stays a "simple" request; an unsupported
// media type upgrades it to a preflight.
func TestContentTypeSafelist(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPost).headers("content-type").maxAgeSeconds("60")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	simpleReq, err := http.NewRequest(http.MethodPost, server.URL+"/form", nil)
	if err != nil {
		t.Fatal(err)
	}
	simpleReq.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(simpleReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	expectCounts(t, server, 0, 1)

	complexReq, err := http.NewRequest(http.MethodPost, server.URL+"/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	complexReq.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(complexReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	expectCounts(t, server, 1, 2)
}

// When the server omits ACA-methods/ACA-headers, the requested method and
// headers are implicitly permitted for this entry, per the Fetch standard. An
// omitted ACA-headers header is equivalent to "*" and therefore authorizes any
// header for subsequent requests sharing the same identity tuple.
func TestImplicitPermissions(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) { c.echo().maxAgeSeconds("60") })

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)

	if _, err := doRequest(t, client, server, http.MethodPatch, map[string]string{"X-Implicit": "1"}); err != nil {
		t.Fatal(err)
	}
	// A different header still rides the same cached entry thanks to the
	// implicit wildcard; the first entry only granted PATCH, so a different
	// method forces a fresh preflight (which the server likewise implicitly
	// grants for that requested method).
	if _, err := doRequest(t, client, server, http.MethodPatch, map[string]string{"X-Other": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := doRequest(t, client, server, http.MethodDelete, map[string]string{"X-Implicit": "1"}); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, server, 2, 3)
}

// A request whose context is cancelled while waiting on a coalesced preflight
// returns the context error instead of blocking forever.
func TestCoalescedWaiterCancellation(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("60")
	})

	gate := make(chan struct{})
	server.handler = gateHandler(server.handle, gate)

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	policy := Policy{Origin: sourceOrigin, Mode: ModeOmit}

	leaderStarted := make(chan struct{})
	go func() {
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/resource", nil)
		req.Header.Set("X-Test", "1")
		ctx := WithPolicy(context.Background(), policy)
		close(leaderStarted)
		_, _ = corsTransport.RoundTrip(req.WithContext(ctx))
	}()
	<-leaderStarted
	// Let the leader reach the gated preflight.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(WithPolicy(context.Background(), policy))
	waiterReq, _ := http.NewRequest(http.MethodPut, server.URL+"/resource", nil)
	waiterReq.Header.Set("X-Test", "1")
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := corsTransport.RoundTrip(waiterReq.WithContext(ctx)); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	close(gate)
}

// Requests without a policy bypass CORS entirely.
func TestNoPolicyBypassesCORS(t *testing.T) {
	server := newTestServer()
	defer server.Close()

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Custom", "1")
	resp, err := corsTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	expectCounts(t, server, 0, 1)
}

// A malformed Access-Control-Max-Age fails the preflight instead of being
// treated as the default.
func TestInvalidMaxAgeFails(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	server.configure(func(c *testServerConfig) {
		c.echo().methods(http.MethodPut).headers("x-test").maxAgeSeconds("not-a-number")
	})

	corsTransport := NewCORSTransport(Config{Base: server.transport()})
	client := newClient(Policy{Origin: sourceOrigin, Mode: ModeOmit}, corsTransport)
	_, err := doRequest(t, client, server, http.MethodPut, map[string]string{"X-Test": "1"})
	expectPreflightError(t, err)
	expectCounts(t, server, 1, 0)
}
