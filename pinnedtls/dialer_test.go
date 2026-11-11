package pinnedtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type testTopology struct {
	rootA *testCA
	rootB *testCA

	intermediateA *testCert
	intermediateB *testCert

	// oldIntermediate and newIntermediate are a key rotation: distinct keys,
	// both issued under the same root.
	oldIntermediate *testCert
	newIntermediate *testCert

	oldLeaf *testCert
	newLeaf *testCert
}

func buildTopology() testTopology {
	rootA := newRootCA("Root A")
	rootB := newRootCA("Root B")

	sharedIntermediateKey := mustKey()
	intermediateA := newIntermediate("Intermediate", rootA, sharedIntermediateKey)
	intermediateB := newIntermediate("Intermediate", rootB, sharedIntermediateKey)

	oldIntermediate := newIntermediate("Intermediate-Old", rootA, nil)
	newIntermediate := newIntermediate("Intermediate-New", rootA, nil)

	oldLeaf := newLeaf(testHost, oldIntermediate)
	newLeaf := newLeaf(testHost, newIntermediate)

	return testTopology{
		rootA:           rootA,
		rootB:           rootB,
		intermediateA:   intermediateA,
		intermediateB:   intermediateB,
		oldIntermediate: oldIntermediate,
		newIntermediate: newIntermediate,
		oldLeaf:         oldLeaf,
		newLeaf:         newLeaf,
	}
}

func clientConfig(top testTopology, minVersion, maxVersion uint16) *tls.Config {
	return &tls.Config{
		ServerName: testHost,
		RootCAs:    rootPool(top.rootA, top.rootB),
		MinVersion: minVersion,
		MaxVersion: maxVersion,
	}
}

func pinRule(name string, pins ...SPKIPin) Rule {
	return Rule{
		Name:        name,
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		PrimaryPins: pins,
	}
}

func dialOnce(t *testing.T, dialer *Dialer, server *testServer) *tls.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "pipe", server.address)
	if err != nil {
		t.Fatalf("DialContext: %v (server error: %v)", err, server.error())
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		t.Fatalf("expected *tls.Conn, got %T", conn)
	}
	t.Cleanup(func() { _ = tlsConn.Close() })
	return tlsConn
}

// waitForTicket pumps the client read loop until a session ticket lands in
// the cache. crypto/tls processes the post-handshake NewSessionTicket only
// while reading, so an idle test connection would never populate the cache.
func waitForTicket(t *testing.T, dialer *Dialer, conn *tls.Conn) {
	t.Helper()
	cache := dialer.config.ClientSessionCache.(*sessionCache)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cache.Get(testHost); ok {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		buf := make([]byte, 16)
		_, err := conn.Read(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			// EOF means the peer closed; keep polling briefly so a ticket
			// already buffered in the record layer still gets processed.
			if errors.Is(err, net.ErrClosed) || err.Error() == "EOF" {
				continue
			}
		}
	}
	if _, ok := cache.Get(testHost); !ok {
		t.Fatal("session ticket was never stored")
	}
}

// routePipes wires a dialer's in-memory connections to the given servers,
// keyed by the synthetic address assigned in startTestServer.
func routePipes(dialer *Dialer, servers ...*testServer) {
	byAddress := make(map[string]*testServer, len(servers))
	for _, server := range servers {
		byAddress[server.address] = server
	}
	dialer.netDial = func(_ context.Context, _, addr string) (net.Conn, error) {
		server, ok := byAddress[addr]
		if !ok {
			return nil, &net.OpError{Op: "dial", Net: "pipe", Err: errors.New("unknown test server")}
		}
		return server.listener.dial()
	}
}

func TestSessionResumptionAndRotation(t *testing.T) {
	for _, version := range []struct {
		name       string
		minVersion uint16
		maxVersion uint16
	}{
		{"TLS1.2", tls.VersionTLS12, tls.VersionTLS12},
		{"TLS1.3", tls.VersionTLS13, tls.VersionTLS13},
	} {
		t.Run(version.name, func(t *testing.T) {
			top := buildTopology()

			// One logical service rotating its certificate. A swappable
			// certificate selector models the cutover point.
			var currentCert atomic.Pointer[tls.Certificate]
			oldServerCert := tlsCert(top.oldLeaf, top.oldIntermediate)
			newServerCert := tlsCert(top.newLeaf, top.newIntermediate)
			currentCert.Store(&oldServerCert)
			serverConfig := &tls.Config{
				GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
					return currentCert.Load(), nil
				},
			}
			server := startTestServerWithConfig(serverConfig)
			defer server.close()

			// Pre-rotation rule: current key primary, next key backup.
			oldRule := Rule{
				Name:        testHost,
				NotBefore:   time.Now().Add(-time.Hour),
				NotAfter:    time.Now().Add(time.Hour),
				PrimaryPins: []SPKIPin{SPKISum(top.oldIntermediate.cert)},
				BackupPins:  []SPKIPin{SPKISum(top.newIntermediate.cert)},
			}
			cfg, err := NewConfig([]Rule{oldRule})
			if err != nil {
				t.Fatalf("NewConfig: %v", err)
			}
			verifier, err := NewVerifier(cfg)
			if err != nil {
				t.Fatalf("NewVerifier: %v", err)
			}
			dialer, err := NewDialer(verifier, clientConfig(top, version.minVersion, version.maxVersion))
			if err != nil {
				t.Fatalf("NewDialer: %v", err)
			}
			routePipes(dialer, server)

			// Full handshake, then a resumption that re-verifies the restored
			// chain against the still-active pins.
			first := dialOnce(t, dialer, server)
			if first.ConnectionState().DidResume {
				t.Fatal("first connection must be a full handshake")
			}
			waitForTicket(t, dialer, first)

			second := dialOnce(t, dialer, server)
			if !second.ConnectionState().DidResume {
				t.Fatalf("second connection should resume the cached session (version=0x%x server: handshakes=%d full=%d resumed=%d err=%v)",
					second.ConnectionState().Version, server.handshakes.Load(), server.fullHandshake.Load(), server.resumptions.Load(), server.error())
			}

			// Rotation: old key retires, backup key becomes the sole primary,
			// and the service cuts over to the new chain.
			newRule := pinRule(testHost+"-rotated", SPKISum(top.newIntermediate.cert))
			newCfg, err := NewConfig([]Rule{newRule})
			if err != nil {
				t.Fatalf("NewConfig rotated: %v", err)
			}
			if err := verifier.Update(newCfg); err != nil {
				t.Fatalf("Update: %v", err)
			}
			currentCert.Store(&newServerCert)

			// The cached ticket holds the old chain. Its resumption fails the
			// new pins; DialContext evicts the ticket and retries once with a
			// full handshake against the rotated chain, which succeeds.
			third, err := dialer.DialContext(context.Background(), "pipe", server.address)
			if err != nil {
				t.Fatalf("post-rotation full handshake should succeed after eviction: %v", err)
			}
			defer third.Close()
			if third.(*tls.Conn).ConnectionState().DidResume {
				t.Fatal("stale ticket must be evicted and retried as a full handshake")
			}

			// A fresh dialer performs a full handshake against the rotated
			// service, stores a new ticket, and then resumes under the new pins.
			freshDialer, err := NewDialer(verifier, clientConfig(top, version.minVersion, version.maxVersion))
			if err != nil {
				t.Fatalf("fresh NewDialer: %v", err)
			}
			routePipes(freshDialer, server)

			full := dialOnce(t, freshDialer, server)
			if full.ConnectionState().DidResume {
				t.Fatal("fresh dialer first connection must be a full handshake")
			}
			waitForTicket(t, freshDialer, full)

			conn2, err := freshDialer.DialContext(context.Background(), "pipe", server.address)
			if err != nil {
				t.Fatalf("post-rotation resume dial: %v", err)
			}
			defer conn2.Close()
			if !conn2.(*tls.Conn).ConnectionState().DidResume {
				t.Fatal("post-rotation connection should resume successfully")
			}
		})
	}
}

func TestFullHandshakeMismatchIsNotRetried(t *testing.T) {
	top := buildTopology()

	serverCert := tlsCert(top.oldLeaf, top.oldIntermediate)
	server := startTestServer(serverCert)
	defer server.close()

	// Pin a key that the server never presents.
	rule := pinRule(testHost, SPKISum(top.newLeaf.cert))
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	dialer, err := NewDialer(verifier, clientConfig(top, tls.VersionTLS13, tls.VersionTLS13))
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	routePipes(dialer, server)

	_, err = dialer.DialContext(context.Background(), "pipe", server.address)
	if err != nil {
		t.Logf("client error: %v; server error: %v", err, server.error())
	}
	var pinErr *PinError
	if !errors.As(err, &pinErr) {
		t.Fatalf("expected PinError, got %v", err)
	}
	if pinErr.DidResume {
		t.Fatal("full-handshake failure must not be marked as resumption")
	}
	// A full-handshake pin failure must not trigger a client-side retry: the
	// server observes exactly one aborted handshake rather than a second
	// connection attempt.
	if server.handshakes.Load() != 0 || server.resumptions.Load() != 0 {
		t.Fatalf("full-handshake pin failure must not trigger a retry; handshakes=%d resumptions=%d",
			server.handshakes.Load(), server.resumptions.Load())
	}
}

func TestCrossSignedChainEndToEnd(t *testing.T) {
	top := buildTopology()

	// Server chain under Root B via the cross-signed intermediate certificate,
	// while the pin targets the shared intermediate key (cert under Root A).
	crossLeaf := newLeaf(testHost, top.intermediateB)
	serverCert := tlsCert(crossLeaf, top.intermediateB)
	server := startTestServer(serverCert)
	defer server.close()

	rule := pinRule(testHost, SPKISum(top.intermediateA.cert))
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	dialer, err := NewDialer(verifier, clientConfig(top, tls.VersionTLS13, tls.VersionTLS13))
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	routePipes(dialer, server)

	conn := dialOnce(t, dialer, server)
	state := conn.ConnectionState()
	if len(state.VerifiedChains) == 0 {
		t.Fatal("expected verified chains on full handshake")
	}
}

func TestBindRejectsInsecureConfig(t *testing.T) {
	top := buildTopology()
	rule := pinRule(testHost, SPKISum(top.oldLeaf.cert))
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	insecure := clientConfig(top, tls.VersionTLS13, tls.VersionTLS13)
	insecure.InsecureSkipVerify = true
	if _, err := verifier.Bind(insecure); err == nil {
		t.Fatal("Bind must reject InsecureSkipVerify")
	}
	if _, err := verifier.Bind(nil); err == nil {
		t.Fatal("Bind must reject nil config")
	}
}

func TestSessionCacheEviction(t *testing.T) {
	cache := newSessionCache(nil)
	cache.Put(testHost, &tls.ClientSessionState{})
	if _, ok := cache.Get(testHost); !ok {
		t.Fatal("expected cached session")
	}
	cache.Evict(testHost)
	if _, ok := cache.Get(testHost); ok {
		t.Fatal("expected session to be evicted")
	}
}

func TestNewBoundSessionCacheWrapsAndEvicts(t *testing.T) {
	cache := NewBoundSessionCache(tls.NewLRUClientSessionCache(8))
	state := &tls.ClientSessionState{}
	cache.Put(testHost, state)
	got, ok := cache.Get(testHost)
	if !ok || got != state {
		t.Fatal("wrapped cache must store and return the session")
	}
	cache.Put(testHost, nil)
	if _, ok := cache.Get(testHost); ok {
		t.Fatal("Put(key, nil) must evict the session")
	}
}

func TestNewDialerValidationAndHelpers(t *testing.T) {
	top := buildTopology()
	rule := pinRule(testHost, SPKISum(top.oldIntermediate.cert))
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if _, err := NewDialer(nil, nil); err == nil {
		t.Fatal("nil verifier must be rejected")
	}

	dialer, err := NewDialer(verifier, nil)
	if err != nil {
		t.Fatalf("NewDialer with nil config: %v", err)
	}
	if dialer.Config() == nil {
		t.Fatal("Config() must return the bound config")
	}
	if got := serverNameFromAddr("service.example:443"); got != "service.example" {
		t.Fatalf("serverNameFromAddr host:port = %q", got)
	}
	if got := serverNameFromAddr("no-port"); got != "no-port" {
		t.Fatalf("serverNameFromAddr bare = %q", got)
	}
}

func TestPinErrorUnwrap(t *testing.T) {
	top := buildTopology()
	rule := pinRule(testHost, SPKISum(top.newIntermediate.cert))
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	err = verifier.VerifyConnection(tls.ConnectionState{
		ServerName:     testHost,
		VerifiedChains: [][]*x509.Certificate{{top.oldLeaf.cert}},
	})
	var pinErr *PinError
	if !errors.As(err, &pinErr) {
		t.Fatalf("expected PinError, got %v", err)
	}
	if pinErr.Unwrap() != ErrNoMatch {
		t.Fatalf("Unwrap = %v, want ErrNoMatch", pinErr.Unwrap())
	}
	if pinErr.Error() == "" || !errors.Is(err, ErrNoMatch) {
		t.Fatalf("error must render and match sentinel: %v", err)
	}
}

func TestDecodeSPKIPinRejectsGarbage(t *testing.T) {
	if _, err := DecodeSPKIPin("not base64!!"); err == nil {
		t.Fatal("invalid base64 must be rejected")
	}
	short, err := base64.StdEncoding.DecodeString("AAAA")
	if err != nil {
		t.Fatalf("setup decode: %v", err)
	}
	if _, err := NewSPKIPin(short); err == nil {
		t.Fatal("short digest must be rejected")
	}
}

func TestUnpinnedAllowedWhenDisabled(t *testing.T) {
	top := buildTopology()
	serverCert := tlsCert(top.oldLeaf, top.oldIntermediate)
	server := startTestServer(serverCert)
	defer server.close()

	cfg, err := NewConfig(nil)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	cfg = cfg.WithRequirePins(false)
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	dialer, err := NewDialer(verifier, clientConfig(top, tls.VersionTLS13, tls.VersionTLS13))
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	routePipes(dialer, server)

	conn := dialOnce(t, dialer, server)
	if conn.ConnectionState().DidResume {
		t.Fatal("first connection is full handshake")
	}

	// Fail-closed default still applies to a fresh verifier.
	strictCfg, err := NewConfig(nil)
	if err != nil {
		t.Fatalf("NewConfig strict: %v", err)
	}
	strictVerifier, err := NewVerifier(strictCfg)
	if err != nil {
		t.Fatalf("NewVerifier strict: %v", err)
	}
	if err := strictVerifier.VerifyConnection(tls.ConnectionState{
		ServerName:     testHost,
		VerifiedChains: [][]*x509.Certificate{{top.oldLeaf.cert}},
	}); err == nil || !errors.Is(err, ErrNoActiveRule) {
		t.Fatalf("expected fail-closed ErrNoActiveRule, got %v", err)
	}
}
