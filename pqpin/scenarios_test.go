package pqpin

import (
	"crypto/tls"
	"testing"
	"time"
)

func clientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		// Pin the hybrid group explicitly so tests do not depend on the
		// process-wide tlsmlkem GODEBUG default.
		CurvePreferences: []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256},
	}
}

// 1. Clean hybrid handshake writes a pin; a later stripped ClientHello is
// rejected because the pinned server negotiates a classic group.
func TestScenarioStrippingDetectedAndBlocked(t *testing.T) {
	p := New()
	h := newTestHarness(t, p, clientTLSConfig(), "8443")
	serverCfg := serverConfig(t)

	conn, err := h.dial(serverCfg, false)
	if err != nil {
		t.Fatalf("baseline hybrid handshake failed: %v", err)
	}
	if conn.ConnectionState().Version != tls.VersionTLS13 {
		t.Fatalf("expected TLS 1.3, got %x", conn.ConnectionState().Version)
	}
	_ = conn.Close()

	rec := p.Lookup(h.serverID)
	if rec == nil || !IsHybridGroup(rec.Group) {
		t.Fatalf("hybrid handshake did not pin the server: %+v", rec)
	}

	// Attacker strips hybrid groups from the next ClientHello. The server
	// falls back to X25519 and the pinner must abort.
	conn2, err := h.dial(serverCfg, true)
	if conn2 != nil {
		_ = conn2.Close()
		t.Fatalf("stripped handshake must not return a connection")
	}
	de, ok := AsDowngrade(err)
	if !ok {
		t.Fatalf("want DowngradeError, got %v", err)
	}
	if de.Reason != ReasonServerHello || IsHybridGroup(de.SelectedGroup) {
		t.Fatalf("want classic ServerHello downgrade, got %+v", de)
	}

	// A failed/downgraded handshake must not have altered the pin.
	if got := p.Lookup(h.serverID); got == nil || got.ExpiresAt != rec.ExpiresAt {
		t.Fatalf("pin must be untouched by a blocked handshake")
	}
}

// 2. An unpinned host tolerates a classic negotiation (the post-stripping
// outcome) and never gets pinned by the resulting classic handshake. A
// classic-only server models the world after an attacker successfully removes
// the hybrid group from the ClientHello.
func TestScenarioUnpinnedHostToleratesStripping(t *testing.T) {
	p := New()
	h := newTestHarness(t, p, clientTLSConfig(), "8444")

	// The client still prefers the hybrid group, but the server offers none,
	// so the connection legitimately settles on X25519.
	classicOnly := serverConfig(t, tls.X25519, tls.CurveP256)
	conn, err := h.dial(classicOnly, false)
	if err != nil {
		t.Fatalf("unpinned host must allow classic negotiation, got %v", err)
	}
	if conn.ConnectionState().DidResume {
		t.Fatalf("expected a fresh handshake")
	}
	if group := negotiatedGroupOf(t, h, classicOnly); IsHybridGroup(group) {
		t.Fatalf("expected a classic negotiated group, got %d", group)
	}
	_ = conn.Close()

	if rec := p.Lookup(h.serverID); rec != nil {
		t.Fatalf("classic handshake must not pin an unpinned host: %+v", rec)
	}
}

// negotiatedGroupOf opens a throwaway connection and returns the group the
// snoop observes in the final ServerHello, confirming the negotiated KEX.
func negotiatedGroupOf(t *testing.T, h *testHarness, cfg *tls.Config) uint16 {
	t.Helper()
	clientRaw, serverRaw := pipePair(h.port + "x")
	ready := make(chan *tls.Conn, 1)
	done := make(chan error, 1)
	go runTLSServer(t, serverRaw, cfg, ready, done)
	snoop := newSnoopConn(clientRaw, nil)
	cli := tls.Client(snoop, clientTLSConfig())
	if err := cli.Handshake(); err != nil {
		t.Fatalf("probe handshake: %v", err)
	}
	group := snoop.negotiatedGroup
	_ = cli.Close()
	select {
	case srv := <-ready:
		_ = srv.Close()
	default:
	}
	<-done
	return group
}

// 3. HRR selecting a classic group is aborted mid-handshake on a pinned host.
func TestScenarioHRRClassicSelectionBlocked(t *testing.T) {
	p := New()
	h := newTestHarness(t, p, clientTLSConfig(), "8445")
	if err := p.Commit(Observation{ServerID: h.serverID, NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}

	// Go's client orders groups by a fixed global preference, so a Go server
	// cannot be forced into a classic HRR via CurvePreferences. Drive the
	// production snoop + pinner callback directly with a captured HRR record
	// naming a classic group, which is exactly the bytes an attacker would
	// inject on the wire.
	snoop := newSnoopConn(nil, func(group uint16) error {
		return p.Evaluate(Observation{ServerID: h.serverID, HadHRR: true, HRRGroup: group})
	})
	snoop.feed(buildServerHelloRecord(classic, true))
	if !snoop.hadHRR || snoop.hrrGroup != classic {
		t.Fatalf("snoop did not capture the classic HRR: %+v", snoop)
	}
	// Re-run the same verdict the Dialer installs as its HRR callback.
	err := p.Evaluate(Observation{ServerID: h.serverID, HadHRR: true, HRRGroup: classic})
	de, ok := AsDowngrade(err)
	if !ok {
		t.Fatalf("want DowngradeError for classic HRR, got %v", err)
	}
	if de.Reason != ReasonHRR || de.SelectedGroup != classic {
		t.Fatalf("want classic HRR, got %+v", de)
	}
}

// 4. A hybrid HRR on a pinned host must not trip the enforcement callback.
// (The full allow-path after a genuine HRR is covered by TestHRRDowngrade's
// hybrid case; here we assert the production Evaluate verdict directly on the
// same observation shape the snoop callback constructs.)
func TestScenarioHRRHybridSelectionAllowed(t *testing.T) {
	p := New()
	serverID := "127.0.0.1:8446"
	if err := p.Commit(Observation{ServerID: serverID, NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}
	cb := func(group uint16) error {
		return p.Evaluate(Observation{
			ServerID:        serverID,
			HadHRR:          true,
			HRRGroup:        group,
			NegotiatedGroup: hybrid, // final ServerHello after the retry
		})
	}
	if err := cb(hybrid); err != nil {
		t.Fatalf("hybrid HRR must be allowed by the callback, got %v", err)
	}
	if err := cb(classic); err == nil {
		t.Fatalf("classic HRR must be rejected by the callback")
	}
}

// 5. Resuming an old classic-origin session is blocked once the host is
// pinned, while a hybrid-origin session resumes cleanly.
func TestScenarioClassicSessionResumptionBlocked(t *testing.T) {
	p := New()
	sharedCache := tls.NewLRUClientSessionCache(8)
	clientCfg := clientTLSConfig()
	clientCfg.ClientSessionCache = sharedCache
	h := newTestHarness(t, p, clientCfg, "8447")

	classicServer := serverConfig(t, tls.X25519)

	conn, err := h.dial(classicServer, false)
	if err != nil {
		t.Fatalf("classic baseline dial: %v", err)
	}
	if conn.ConnectionState().DidResume {
		t.Fatalf("first connection must be a full handshake")
	}
	_ = conn.Close()

	if rec := p.Lookup(h.serverID); rec != nil {
		t.Fatalf("classic handshake must not create a pin: %+v", rec)
	}
	if origin := ResumeOrigin(h.dialer.clientConfig().ClientSessionCache, h.serverID); origin != OriginClassic {
		t.Fatalf("ticket origin must be classic, got %v", origin)
	}

	// The endpoint is now known to be PQ-capable. Resuming the old classic
	// ticket must be reported as a downgrade.
	if err := p.Commit(Observation{ServerID: h.serverID, NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}
	conn2, err := h.dial(classicServer, false)
	if conn2 != nil {
		_ = conn2.Close()
	}
	de, ok := AsDowngrade(err)
	if !ok {
		t.Fatalf("classic resumption must be blocked, got %v", err)
	}
	if de.Reason != ReasonResume {
		t.Fatalf("want resumption reason, got %s", de.Reason)
	}
}

func TestScenarioHybridSessionResumptionAllowed(t *testing.T) {
	p := New()
	sharedCache := tls.NewLRUClientSessionCache(8)
	clientCfg := clientTLSConfig()
	clientCfg.ClientSessionCache = sharedCache
	h := newTestHarness(t, p, clientCfg, "8448")
	serverCfg := serverConfig(t)

	conn, err := h.dial(serverCfg, false)
	if err != nil {
		t.Fatalf("initial hybrid dial: %v", err)
	}
	_ = conn.Close()

	conn2, err := h.dial(serverCfg, false)
	if err != nil {
		t.Fatalf("hybrid-origin resumption failed: %v", err)
	}
	if !conn2.ConnectionState().DidResume {
		_ = conn2.Close()
		t.Fatalf("second connection must resume the hybrid-origin session")
	}
	_ = conn2.Close()

	if origin := ResumeOrigin(h.dialer.clientConfig().ClientSessionCache, h.serverID); origin != OriginHybrid {
		t.Fatalf("resumed ticket must retain hybrid origin, got %v", origin)
	}
}

// 6. Expiry boundary through the full connection path: blocked while valid,
// allowed after expiry, and re-pinned by a later clean hybrid handshake.
func TestScenarioExpiryBoundaryEndToEnd(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	clock := start
	p := New(WithTTL(10*time.Minute), WithClock(func() time.Time { return clock }))
	h := newTestHarness(t, p, clientTLSConfig(), "8449")
	hybridServer := serverConfig(t)
	classicServer := serverConfig(t, tls.X25519, tls.CurveP256)

	if err := p.Commit(Observation{ServerID: h.serverID, NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}

	// Exactly at the boundary the pin is still valid: stripping is blocked.
	clock = start.Add(10 * time.Minute)
	if conn, err := h.dial(classicServer, false); err == nil {
		_ = conn.Close()
		t.Fatalf("classic negotiation must be blocked at exactly ExpiresAt")
	}

	// One nanosecond later the pin has expired: stripping succeeds but does
	// not pin anything.
	clock = start.Add(10*time.Minute + time.Nanosecond)
	conn, err := h.dial(classicServer, false)
	if err != nil {
		t.Fatalf("expired pin must allow classic negotiation, got %v", err)
	}
	_ = conn.Close()
	if p.Lookup(h.serverID) != nil {
		t.Fatalf("classic handshake after expiry must not rebuild a pin")
	}

	// A clean hybrid handshake rebuilds trust.
	clean, err := h.dial(hybridServer, false)
	if err != nil {
		t.Fatalf("clean hybrid repin failed: %v", err)
	}
	_ = clean.Close()
	if p.Lookup(h.serverID) == nil {
		t.Fatalf("clean hybrid handshake must rebuild the pin")
	}
}
