package workloadid

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"
)

// tlsFixture builds two managers/verifiers representing the client and server
// workloads sharing the same mesh root bundle.
type tlsFixture struct {
	clientLeaf *leafBundle
	serverLeaf *leafBundle
	root       *ca
	inter      *ca
	clock      *fakeClock
}

func newTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	base := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	// crypto/tls validates against the real wall clock, so the integration
	// certificates use real time; clock-sensitive behavior is covered by the
	// fake-clock unit tests.
	testNow = time.Now

	root, err := newRoot("mesh-root")
	if err != nil {
		t.Fatal(err)
	}
	inter, err := newIntermediate(root, "mesh-inter", leafOptions{serial: 2, ttl: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newLeaf(inter, leafOptions{
		serial: 10, dns: []string{"client.prod.example.com"},
		uris: []*url.URL{mustURI("spiffe://prod.example.com/ns/payments/svc/client")},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := newLeaf(inter, leafOptions{
		serial: 11, dns: []string{"server.prod.example.com"},
		ekus: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &tlsFixture{clientLeaf: client, serverLeaf: server, root: root, inter: inter, clock: clock}
}

func (tf *tlsFixture) managerWith(cred *leafBundle) *Manager {
	manager := NewManager(RealClock{})
	cfg := BundleConfig{
		TrustDomain:   prodTD,
		MaxChainDepth: 5,
		MaxLeafTTL:    24 * time.Hour,
		Revocation: RevocationConfig{
			OCSPGrace: 30 * time.Second, CRLGrace: time.Minute, MaxSkew: time.Minute,
		},
	}
	if _, err := manager.Activate(1, cfg, RootBundle{
		Roots:         []*x509.Certificate{tf.root.cert},
		Intermediates: []*x509.Certificate{tf.inter.cert},
	}); err != nil {
		panic(err)
	}
	if _, err := manager.RotateCredential(2, WorkloadCredential{Leaf: cred.cert, PrivateKey: cred.key}); err != nil {
		panic(err)
	}
	return manager
}

// TestEndToEndMutualTLS runs a real mTLS handshake over net.Pipe and asserts
// both peers authorize through VerifyConnection with full re-authentication.
func TestEndToEndMutualTLS(t *testing.T) {
	tf := newTLSFixture(t)

	clientManager := tf.managerWith(tf.clientLeaf)
	serverManager := tf.managerWith(tf.serverLeaf)

	clientVerifier := NewVerifier(clientManager)
	serverVerifier := NewVerifier(serverManager)

	clientAuth := &TLSAuthorizer{
		Verifier:     clientVerifier,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attest:       attestationClaim,
		ServerName:   "server.prod.example.com",
	}
	serverAuth := &TLSAuthorizer{
		Verifier:     serverVerifier,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attest:       attestationClaim,
	}

	clientTLS, err := clientAuth.TLSConfig(clientManager)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := serverAuth.TLSConfig(serverManager)
	if err != nil {
		t.Fatal(err)
	}

	clientTLSConn, serverTLSConn := pumpHandshake(t, clientTLS, serverTLS)

	if clientTLSConn.ConnectionState().DidResume || serverTLSConn.ConnectionState().DidResume {
		t.Fatal("fresh connections must not be resumed sessions")
	}
	if !clientTLSConn.ConnectionState().HandshakeComplete || !serverTLSConn.ConnectionState().HandshakeComplete {
		t.Fatal("handshake must be complete")
	}

	// Both peers must see an authenticated workload identity and negotiated
	// certificates through the component's VerifyConnection path.
	if len(clientTLSConn.ConnectionState().PeerCertificates) == 0 ||
		len(serverTLSConn.ConnectionState().PeerCertificates) == 0 {
		t.Fatal("both peers must present a verified peer certificate")
	}
	if clientTLSConn.ConnectionState().Version < tls.VersionTLS12 {
		t.Fatal("TLS 1.2+ required")
	}
	_ = clientTLSConn.Close()
	_ = serverTLSConn.Close()
}

// TestEndToEndRejectedPeer confirms a peer that fails policy is refused at the
// TLS layer rather than connected and checked later.
func TestEndToEndRejectedPeer(t *testing.T) {
	tf := newTLSFixture(t)

	clientManager := tf.managerWith(tf.clientLeaf)
	serverManager := tf.managerWith(tf.serverLeaf)

	// A caller policy that the client SPIFFE path cannot satisfy.
	denyCaller := callerPolicy()
	denyCaller.AllowedPathPrefixes = [][]string{{"ns", "forbidden"}}

	serverVerifier := NewVerifier(serverManager)
	serverAuth := &TLSAuthorizer{
		Verifier:     serverVerifier,
		CallerPolicy: denyCaller,
		TargetPolicy: targetPolicy(),
		Attest:       attestationClaim,
	}
	serverTLS, err := serverAuth.TLSConfig(serverManager)
	if err != nil {
		t.Fatal(err)
	}
	clientVerifier := NewVerifier(clientManager)
	clientAuth := &TLSAuthorizer{
		Verifier:     clientVerifier,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attest:       attestationClaim,
		ServerName:   "server.prod.example.com",
	}
	clientTLS, err := clientAuth.TLSConfig(clientManager)
	if err != nil {
		t.Fatal(err)
	}

	err = pumpHandshakeExpectError(t, clientTLS, serverTLS)
	if !errors.Is(err, ErrHandshakeDenied) {
		t.Fatalf("expected a denied handshake at the TLS layer, got %v", err)
	}
}

// pumpHandshake runs both TLS handshakes over a net.Pipe with dedicated reader
// goroutines so neither side blocks waiting for its full record to be drained.
// It returns the two established connections, or fails the test when a peer
// rejects the handshake (for example via VerifyConnection).
func pumpHandshake(t *testing.T, clientCfg, serverCfg *tls.Config) (*tls.Conn, *tls.Conn) {
	t.Helper()
	clientRaw, serverRaw := net.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	_ = clientRaw.SetDeadline(deadline)
	_ = serverRaw.SetDeadline(deadline)
	clientConn := tls.Client(clientRaw, clientCfg)
	serverConn := tls.Server(serverRaw, serverCfg)

	errCh := make(chan error, 2)
	go func() { errCh <- serverConn.Handshake() }()
	go func() { errCh <- clientConn.Handshake() }()

	var firstErr error
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil && firstErr == nil {
				firstErr = err
			}
		case <-time.After(6 * time.Second):
			t.Fatal("handshake timed out")
		}
	}
	if firstErr != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatalf("handshake failed: %v", firstErr)
	}
	_ = clientConn.SetDeadline(time.Time{})
	_ = serverConn.SetDeadline(time.Time{})
	return clientConn, serverConn
}

// pumpHandshakeExpectError is the denial variant of pumpHandshake.
func pumpHandshakeExpectError(t *testing.T, clientCfg, serverCfg *tls.Config) error {
	t.Helper()
	clientRaw, serverRaw := net.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	_ = clientRaw.SetDeadline(deadline)
	_ = serverRaw.SetDeadline(deadline)
	clientConn := tls.Client(clientRaw, clientCfg)
	serverConn := tls.Server(serverRaw, serverCfg)

	errCh := make(chan error, 2)
	go func() { errCh <- serverConn.Handshake() }()
	go func() { errCh <- clientConn.Handshake() }()

	var firstErr error
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil && firstErr == nil {
				firstErr = err
			}
		case <-time.After(6 * time.Second):
			t.Fatal("handshake timed out")
		}
	}
	_ = clientConn.Close()
	_ = serverConn.Close()
	return firstErr
}
