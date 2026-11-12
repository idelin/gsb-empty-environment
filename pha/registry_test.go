package pha

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func newRegisteredPair(t *testing.T, advertised bool) (*Registry, *frameConn, *frameConn, string) {
	t.Helper()
	server, client := newFramePair()
	reg := NewRegistry()
	id := "conn-1"
	reg.Register(id, server, advertised)
	t.Cleanup(func() {
		reg.Release(id)
		server.Close()
		client.Close()
	})
	return reg, server, client, id
}

func readClientHandshake(t *testing.T, c *frameConn) []byte {
	t.Helper()
	msg, err := c.ReadHandshake()
	if err != nil {
		t.Fatalf("client ReadHandshake: %v", err)
	}
	return msg
}

func isIllegalParameter(t *testing.T, err error) *AlertError {
	t.Helper()
	var alert *AlertError
	if !errors.As(err, &alert) {
		t.Fatalf("want *AlertError, got %T: %v", err, err)
	}
	if alert.Level != 2 || alert.Description != alertIllegalParameter {
		t.Fatalf("want fatal illegal_parameter (47), got level=%d desc=%d", alert.Level, alert.Description)
	}
	return alert
}

func TestCertificateRequestOnlyWhenAdvertised(t *testing.T) {
	reg, _, client, id := newRegisteredPair(t, false)

	if _, err := reg.CertificateRequest(id, testSignatureAlgorithms); !errors.Is(err, ErrPostHandshakeAuthNotAdvertised) {
		t.Fatalf("want ErrPostHandshakeAuthNotAdvertised, got %v", err)
	}
	if got := reg.Outstanding(id); got != 0 {
		t.Fatalf("want 0 outstanding, got %d", got)
	}

	// Nothing must have been written to the client.
	if len(client.raw.in) != 0 {
		t.Fatal("unexpected CertificateRequest sent without post_handshake_auth")
	}
}

func TestRequestContextIsFreshRandomAndEchoed(t *testing.T) {
	reg, _, client, id := newRegisteredPair(t, true)

	seen := make(map[string]bool)
	var contexts [][]byte
	for i := 0; i < MaxOutstanding; i++ {
		ctx, err := reg.CertificateRequest(id, testSignatureAlgorithms)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if len(ctx) < 8 {
			t.Fatalf("context shorter than 8 bytes: %d", len(ctx))
		}
		if seen[string(ctx)] {
			t.Fatalf("duplicate context %x", ctx)
		}
		seen[string(ctx)] = true
		contexts = append(contexts, ctx)

		record := readClientHandshake(t, client)
		wireCtx, algs, err := ParseCertificateRequest(record)
		if err != nil {
			t.Fatalf("parse CertificateRequest: %v", err)
		}
		if string(wireCtx) != string(ctx) {
			t.Fatalf("wire context %x != registry context %x", wireCtx, ctx)
		}
		if len(algs) != len(testSignatureAlgorithms) {
			t.Fatalf("want %d signature algorithms, got %d", len(testSignatureAlgorithms), len(algs))
		}
	}

	if got := reg.Outstanding(id); got != MaxOutstanding {
		t.Fatalf("want %d outstanding, got %d", MaxOutstanding, got)
	}

	// Answering each request exactly once succeeds and releases the slot.
	for i, ctx := range contexts {
		cert, err := MarshalCertificate(ctx, testLeaf)
		if err != nil {
			t.Fatal(err)
		}
		der, err := reg.HandleCertificate(id, cert)
		if err != nil {
			t.Fatalf("valid response %d rejected: %v", i, err)
		}
		if string(der) != string(testLeaf) {
			t.Fatalf("leaf DER mismatch")
		}
	}
	if got := reg.Outstanding(id); got != 0 {
		t.Fatalf("want 0 outstanding after answers, got %d", got)
	}
}

func TestOutstandingLimit(t *testing.T) {
	reg, _, _, id := newRegisteredPair(t, true)

	for i := 0; i < MaxOutstanding; i++ {
		if _, err := reg.CertificateRequest(id, testSignatureAlgorithms); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if _, err := reg.CertificateRequest(id, testSignatureAlgorithms); !errors.Is(err, ErrTooManyOutstanding) {
		t.Fatalf("want ErrTooManyOutstanding, got %v", err)
	}

	// After one answer, a slot frees up and a new request is allowed.
	first, _ := reg.snapshotContexts(id)
	cert, err := MarshalCertificate(first, testLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.HandleCertificate(id, cert); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if _, err := reg.CertificateRequest(id, testSignatureAlgorithms); err != nil {
		t.Fatalf("request after slot freed: %v", err)
	}
}

func TestReplayRejectedWithIllegalParameter(t *testing.T) {
	reg, _, client, id := newRegisteredPair(t, true)

	ctx, err := reg.CertificateRequest(id, testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	record := readClientHandshake(t, client)
	if wireCtx, _, err := ParseCertificateRequest(record); err != nil || string(wireCtx) != string(ctx) {
		t.Fatalf("wire/context mismatch: %v", err)
	}

	cert, err := MarshalCertificate(ctx, testLeaf)
	if err != nil {
		t.Fatal(err)
	}

	// First presentation is consumed.
	if _, err := reg.HandleCertificate(id, cert); err != nil {
		t.Fatalf("first presentation: %v", err)
	}

	// Replaying the identical Certificate message must fail and alert.
	_, err = reg.HandleCertificate(id, cert)
	isIllegalParameter(t, err)
	assertPeerAlert(t, client, alertIllegalParameter)
}

func TestEmptyUnknownAndMismatchedContexts(t *testing.T) {
	reg, _, client, id := newRegisteredPair(t, true)

	ctx, err := reg.CertificateRequest(id, testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	readClientHandshake(t, client)

	cases := map[string][]byte{
		"empty":    {},
		"unknown":  []byte("never-issued-context-1234"),
		"tampered": append(append([]byte(nil), ctx...), 0x01),
	}
	for name, badCtx := range cases {
		cert, err := MarshalCertificate(badCtx, testLeaf)
		if err != nil {
			t.Fatal(err)
		}
		_, err = reg.HandleCertificate(id, cert)
		isIllegalParameter(t, err)
		assertPeerAlert(t, client, alertIllegalParameter)

		// The legitimate request must remain outstanding and still answerable.
		if got := reg.Outstanding(id); got != 1 {
			t.Fatalf("%s: outstanding = %d, want 1", name, got)
		}
	}

	good, err := MarshalCertificate(ctx, testLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.HandleCertificate(id, good); err != nil {
		t.Fatalf("legitimate request after rejected garbage: %v", err)
	}
}

func TestContextNotReusableAcrossConnections(t *testing.T) {
	serverA, clientA := newFramePair()
	serverB, clientB := newFramePair()
	reg := NewRegistry()
	reg.Register("A", serverA, true)
	reg.Register("B", serverB, true)
	t.Cleanup(func() {
		reg.Release("A")
		reg.Release("B")
		serverA.Close()
		clientA.Close()
		serverB.Close()
		clientB.Close()
	})

	ctxA, err := reg.CertificateRequest("A", testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	readClientHandshake(t, clientA)

	ctxB, err := reg.CertificateRequest("B", testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	readClientHandshake(t, clientB)
	if string(ctxA) == string(ctxB) {
		t.Fatal("contexts of two connections collided")
	}

	// A context stolen from connection A presented on connection B is unknown.
	stolen, err := MarshalCertificate(ctxA, testLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.HandleCertificate("B", stolen); err == nil {
		t.Fatal("cross-connection context accepted")
	} else {
		isIllegalParameter(t, err)
	}
	assertPeerAlert(t, clientB, alertIllegalParameter)

	// The original owner can still answer its own request.
	own, err := MarshalCertificate(ctxA, testLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.HandleCertificate("A", own); err != nil {
		t.Fatalf("owner answer rejected: %v", err)
	}
}

func TestConcurrentResponsesSingleWinner(t *testing.T) {
	reg, _, client, id := newRegisteredPair(t, true)

	ctx, err := reg.CertificateRequest(id, testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	readClientHandshake(t, client)

	cert, err := MarshalCertificate(ctx, testLeaf)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	var winners, rejected int64
	var mu sync.Mutex
	start := make(chan struct{})
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := reg.HandleCertificate(id, cert)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners++
			} else {
				rejected++
			}
		}()
	}
	close(start)
	wg.Wait()

	if winners != 1 {
		t.Fatalf("want exactly 1 winner, got %d", winners)
	}
	if rejected != goroutines-1 {
		t.Fatalf("want %d rejected replays, got %d", goroutines-1, rejected)
	}
	if got := reg.Outstanding(id); got != 0 {
		t.Fatalf("want 0 outstanding, got %d", got)
	}
	assertPeerAlert(t, client, alertIllegalParameter)
}

func TestServeAcceptsAndRejects(t *testing.T) {
	reg, _, client, id := newRegisteredPair(t, true)
	results, err := reg.Serve(id)
	if err != nil {
		t.Fatal(err)
	}

	ctx, err := reg.CertificateRequest(id, testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	readClientHandshake(t, client)

	good, err := MarshalCertificate(ctx, testLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteHandshake(good); err != nil {
		t.Fatal(err)
	}

	select {
	case got, ok := <-results:
		if !ok {
			t.Fatal("results channel closed before delivery")
		}
		if string(got.Context) != string(ctx) || string(got.DER) != string(testLeaf) {
			t.Fatal("delivered certificate mismatch")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for accepted certificate")
	}

	// A replay through the Serve loop triggers the alert and ends the loop.
	if err := client.WriteHandshake(good); err != nil {
		t.Fatal(err)
	}
	assertPeerAlert(t, client, alertIllegalParameter)
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("results channel should be closed after fatal alert")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve loop did not stop after replay")
	}
}

func TestUnknownConnectionRejected(t *testing.T) {
	reg, server, client, _ := newRegisteredPair(t, true)

	cert, err := MarshalCertificate([]byte("some-context-1234"), testLeaf)
	if err != nil {
		t.Fatal(err)
	}

	// A Certificate on an id the registry never enrolled is an illegal parameter.
	_, err = reg.HandleCertificate("ghost", cert)
	isIllegalParameter(t, err)

	// And requesting on an unknown connection fails rather than sending anything.
	if _, err := reg.CertificateRequest("ghost", testSignatureAlgorithms); !errors.Is(err, ErrUnknownConnection) {
		t.Fatalf("want ErrUnknownConnection, got %v", err)
	}
	if len(client.raw.in) != 0 || len(server.raw.in) != 0 {
		t.Fatal("unknown connection produced wire traffic")
	}
}

func TestContextMinimumLength(t *testing.T) {
	reg, _, _, id := newRegisteredPair(t, true)
	ctx, err := reg.CertificateRequest(id, testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) < 8 {
		t.Fatalf("context must be at least 8 bytes, got %d", len(ctx))
	}
}

func TestMessageRoundTrip(t *testing.T) {
	ctx := []byte("0123456789abcdef")
	req, err := MarshalCertificateRequest(ctx, testSignatureAlgorithms)
	if err != nil {
		t.Fatal(err)
	}
	gotCtx, gotAlgs, err := ParseCertificateRequest(req)
	if err != nil || string(gotCtx) != string(ctx) || len(gotAlgs) != len(testSignatureAlgorithms) {
		t.Fatalf("CertificateRequest round trip: ctx=%x algs=%v err=%v", gotCtx, gotAlgs, err)
	}

	cert, err := MarshalCertificate(ctx, testLeaf)
	if err != nil {
		t.Fatal(err)
	}
	gotCtx2, gotDER, err := ParseCertificate(cert)
	if err != nil || string(gotCtx2) != string(ctx) || string(gotDER) != string(testLeaf) {
		t.Fatalf("Certificate round trip: ctx=%x der=%q err=%v", gotCtx2, gotDER, err)
	}
}

func assertPeerAlert(t *testing.T, c *frameConn, wantDescription byte) {
	t.Helper()
	_, err := c.ReadHandshake()
	var alert *peerAlert
	if !errors.As(err, &alert) {
		t.Fatalf("want peerAlert, got %v", err)
	}
	if alert.level != 2 || alert.description != wantDescription {
		t.Fatalf("want alert desc %d, got level=%d desc=%d", wantDescription, alert.level, alert.description)
	}
}

func (r *Registry) snapshotContexts(id string) ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.conns[id]
	if c == nil {
		return nil, false
	}
	for _, ctx := range c.outstanding {
		return ctx, true
	}
	return nil, false
}
