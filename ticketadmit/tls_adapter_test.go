package ticketadmit_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"example.org/netsec/q0586/ticketadmit"
)

const (
	lenientSNI = "lenient.example"
	strictSNI  = "strict.example"
)

type testPKI struct {
	serverCert tls.Certificate
	clientCert tls.Certificate
	clientPool *x509.CertPool
}

func buildPKI(t *testing.T) testPKI {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}

	mkLeaf := func(cn string, client bool) tls.Certificate {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("leaf key: %v", err)
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(int64(time.Now().UnixNano())),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			DNSNames:     []string{lenientSNI, strictSNI},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatalf("leaf cert: %v", err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return testPKI{
		serverCert: mkLeaf("server", false),
		clientCert: mkLeaf("client", true),
		clientPool: pool,
	}
}

// testServer holds the shared virtual-host configuration used by the in-memory
// pipe-based server side of every handshake.
type testServer struct {
	base *tls.Config
}

// newServer builds two TLS 1.3 virtual hosts selected by SNI. The strict host
// requires and verifies a client certificate; the lenient host accepts none.
// Both hosts share the adapter (and its key ring).
func newServer(pki testPKI, adapter *ticketadmit.TLSAdapter) *testServer {
	lenient := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.NoClientCert,
		NextProtos:   []string{"h2"},
	}
	adapter.Configure(lenient)

	strict := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.clientPool,
		NextProtos:   []string{"h2"},
	}
	adapter.Configure(strict)

	base := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2"},
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			if chi.ServerName == strictSNI {
				return strict, nil
			}
			return lenient, nil
		},
	}
	return &testServer{base: base}
}

// handshakeResult reports both sides of an in-memory TLS handshake.
type handshakeResult struct {
	clientState tls.ConnectionState
	clientErr   error
	serverErr   error
}

// waitForCache polls the client session cache until the ticket for sni is
// stored or the deadline elapses.
func waitForCache(t *testing.T, cache tls.ClientSessionCache, sni string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cache.Get(sni); ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("client session ticket for %q was not cached", sni)
}

// drainConn services a TLS connection's record loop until an error (typically
// a pipe close) so post-handshake messages such as NewSessionTicket are
// processed without assuming a fixed read/write ordering.
func drainConn(conn *tls.Conn) {
	buf := make([]byte, 16)
	for {
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

// handshake runs one TLS 1.3 handshake over an in-memory buffered pipe and
// returns the client- and server-side outcomes so tests can assert on either.
// On success the connections stay open long enough for the post-handshake
// NewSessionTicket to be delivered and cached, then both sides are closed.
func (s *testServer) handshake(t *testing.T, clientCfg *tls.Config) handshakeResult {
	t.Helper()
	clientPipe, serverPipe := newBufferedPipePair()
	deadline := time.Now().Add(15 * time.Second)
	_ = clientPipe.SetDeadline(deadline)
	_ = serverPipe.SetDeadline(deadline)
	serverConn := tls.Server(serverPipe, s.base)
	clientConn := tls.Client(clientPipe, clientCfg)

	type sideResult struct {
		state tls.ConnectionState
		err   error
	}
	serverHS := make(chan sideResult, 1)
	clientHS := make(chan sideResult, 1)
	serverDone := make(chan struct{}, 1)
	clientDone := make(chan struct{}, 1)

	go func() {
		err := serverConn.Handshake()
		result := sideResult{state: serverConn.ConnectionState(), err: err}
		serverHS <- result
		if err == nil {
			// Drain until the main goroutine closes the pipe, keeping the
			// read side serviced for the close_notify exchange.
			drainConn(serverConn)
		}
		_ = serverConn.Close()
		serverDone <- struct{}{}
	}()
	go func() {
		err := clientConn.Handshake()
		result := sideResult{state: clientConn.ConnectionState(), err: err}
		clientHS <- result
		if err == nil {
			// Reading drives the record loop through the NewSessionTicket
			// message and into the session cache, then until pipe close.
			drainConn(clientConn)
		}
		_ = clientConn.Close()
		clientDone <- struct{}{}
	}()

	server := <-serverHS
	client := <-clientHS

	// On a mutually successful handshake, wait for the client to have cached
	// the NewSessionTicket before closing its side of the pipe.
	if server.err == nil && client.err == nil {
		waitForCache(t, clientCfg.ClientSessionCache, clientCfg.ServerName)
	}
	// Closing the client pipe ends its read loop; the server pipe close ends
	// the server drain. Both goroutines then return regardless of outcome.
	_ = clientPipe.Close()
	_ = serverPipe.Close()
	<-serverDone
	<-clientDone

	return handshakeResult{
		clientState: client.state,
		clientErr:   client.err,
		serverErr:   server.err,
	}
}

func clientConfig(pki testPKI, withClientCert bool) *tls.Config {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h2"},
		RootCAs:            pki.clientPool,
		InsecureSkipVerify: false,
	}
	if withClientCert {
		cfg.Certificates = []tls.Certificate{pki.clientCert}
	}
	return cfg
}

// forSNI returns a clone of cfg targeted at the given virtual host.
func forSNI(cfg *tls.Config, sni string) *tls.Config {
	clone := cfg.Clone()
	clone.ServerName = sni
	return clone
}

// rekeyCache moves a cached ClientSessionState from one SNI key to another,
// modeling an attacker replaying a ticket onto a different virtual host.
func rekeyCache(cache tls.ClientSessionCache, from, to string) {
	if cs, ok := cache.Get(from); ok {
		cache.Put(to, cs)
	}
}

func newAdapter(t *testing.T) (*ticketadmit.Admitter, *ticketadmit.TLSAdapter, *ticketadmit.KeyRing) {
	t.Helper()
	current, err := ticketadmit.GenerateKey(1)
	if err != nil {
		t.Fatalf("current key: %v", err)
	}
	previous, err := ticketadmit.GenerateKey(0)
	if err != nil {
		t.Fatalf("previous key: %v", err)
	}
	ring, err := ticketadmit.NewKeyRing(current, previous)
	if err != nil {
		t.Fatalf("key ring: %v", err)
	}
	admitter, err := ticketadmit.NewAdmitter(ring)
	if err != nil {
		t.Fatalf("admitter: %v", err)
	}
	policy := func(sni string) ticketadmit.VhostPolicy {
		if sni == strictSNI {
			return ticketadmit.VhostPolicy{RequireClientCert: true}
		}
		return ticketadmit.VhostPolicy{ExpectNoClientCert: true}
	}
	adapter, err := ticketadmit.NewTLSAdapter(admitter, policy)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	return admitter, adapter, ring
}

func TestSameVhostResumes(t *testing.T) {
	pki := buildPKI(t)
	_, adapter, _ := newAdapter(t)
	server := newServer(pki, adapter)

	cache := tls.NewLRUClientSessionCache(8)
	cfg := clientConfig(pki, false)
	cfg.ClientSessionCache = cache

	first := server.handshake(t, forSNI(cfg, lenientSNI))
	if first.clientErr != nil || first.serverErr != nil {
		t.Fatalf("first handshake: client=%v server=%v", first.clientErr, first.serverErr)
	}
	if first.clientState.DidResume {
		t.Fatalf("first handshake unexpectedly resumed")
	}
	second := server.handshake(t, forSNI(cfg, lenientSNI))
	if second.clientErr != nil || second.serverErr != nil {
		t.Fatalf("second handshake: client=%v server=%v", second.clientErr, second.serverErr)
	}
	if !second.clientState.DidResume {
		t.Fatalf("second handshake on same vhost did not resume")
	}
}

func TestCrossSNITicketMigrationFallsBackToFullHandshake(t *testing.T) {
	pki := buildPKI(t)
	_, adapter, _ := newAdapter(t)
	server := newServer(pki, adapter)

	cache := tls.NewLRUClientSessionCache(8)
	cfg := clientConfig(pki, false)
	cfg.ClientSessionCache = cache

	// Obtain a ticket on the lenient virtual host.
	first := server.handshake(t, forSNI(cfg, lenientSNI))
	if first.clientErr != nil || first.serverErr != nil {
		t.Fatalf("first handshake: client=%v server=%v", first.clientErr, first.serverErr)
	}
	if first.clientState.DidResume {
		t.Fatalf("first handshake unexpectedly resumed")
	}

	// Attacker moves the ticket to the strict virtual host's cache key.
	rekeyCache(cache, lenientSNI, strictSNI)

	// The strict host must refuse the cross-SNI ticket and do a full
	// handshake. Use a client with a valid cert so the full handshake can
	// complete and DidResume==false is the observable signal that the moved
	// ticket was refused.
	strictClient := clientConfig(pki, true)
	strictClient.ClientSessionCache = cache
	state := server.handshake(t, forSNI(strictClient, strictSNI))
	if state.clientErr != nil || state.serverErr != nil {
		t.Fatalf("strict handshake: client=%v server=%v", state.clientErr, state.serverErr)
	}
	if state.clientState.DidResume {
		t.Fatalf("cross-SNI ticket was accepted for resumption")
	}
}

func TestCertlessTicketCannotBypassClientAuth(t *testing.T) {
	pki := buildPKI(t)
	_, adapter, _ := newAdapter(t)
	server := newServer(pki, adapter)

	cache := tls.NewLRUClientSessionCache(8)

	// A client without a certificate gets a cert-less ticket on lenient.
	lenientCfg := clientConfig(pki, false)
	lenientCfg.ClientSessionCache = cache
	first := server.handshake(t, forSNI(lenientCfg, lenientSNI))
	if first.clientErr != nil || first.serverErr != nil {
		t.Fatalf("lenient handshake: client=%v server=%v", first.clientErr, first.serverErr)
	}
	if first.clientState.DidResume {
		t.Fatalf("first handshake unexpectedly resumed")
	}

	// Replay the cert-less ticket against the strict host. The missing
	// fingerprint forces a full handshake; under RequireAndVerifyClientCert a
	// client that sends an empty certificate must then be rejected.
	strictCfg := clientConfig(pki, false)
	strictCfg.ClientSessionCache = cache
	strictCfg.ServerName = strictSNI

	rejected := server.handshake(t, strictCfg)
	if rejected.clientErr == nil && rejected.serverErr == nil {
		t.Fatalf("strict host accepted a cert-less client (full handshake should have failed)")
	}

	// A client holding a real certificate completes a full handshake instead.
	authed := clientConfig(pki, true)
	authed.ClientSessionCache = tls.NewLRUClientSessionCache(8)
	state := server.handshake(t, forSNI(authed, strictSNI))
	if state.clientErr != nil || state.serverErr != nil {
		t.Fatalf("authed strict handshake: client=%v server=%v", state.clientErr, state.serverErr)
	}
	if state.clientState.DidResume {
		t.Fatalf("strict host resumed instead of demanding a client certificate")
	}
}

func TestResumptionWithClientCertAfterFullHandshake(t *testing.T) {
	pki := buildPKI(t)
	_, adapter, _ := newAdapter(t)
	server := newServer(pki, adapter)

	cache := tls.NewLRUClientSessionCache(8)
	cfg := clientConfig(pki, true)
	cfg.ClientSessionCache = cache

	// First full handshake on strict presents and verifies the client cert.
	first := server.handshake(t, forSNI(cfg, strictSNI))
	if first.clientErr != nil || first.serverErr != nil {
		t.Fatalf("first strict handshake: client=%v server=%v", first.clientErr, first.serverErr)
	}
	if first.clientState.DidResume || len(first.clientState.PeerCertificates) == 0 {
		t.Fatalf("first strict handshake should be a full, authenticated handshake")
	}
	// Second connection legitimately resumes with the fingerprinted ticket.
	second := server.handshake(t, forSNI(cfg, strictSNI))
	if second.clientErr != nil || second.serverErr != nil {
		t.Fatalf("second strict handshake: client=%v server=%v", second.clientErr, second.serverErr)
	}
	if !second.clientState.DidResume {
		t.Fatalf("fingerprinted ticket should resume on the strict vhost")
	}
}

func TestUnknownTicketDoesNotTerminateHandshake(t *testing.T) {
	pki := buildPKI(t)
	_, adapter, _ := newAdapter(t)
	server := newServer(pki, adapter)

	cache := tls.NewLRUClientSessionCache(8)
	cfg := clientConfig(pki, false)
	cfg.ClientSessionCache = cache
	first := server.handshake(t, forSNI(cfg, lenientSNI))
	if first.clientErr != nil || first.serverErr != nil {
		t.Fatalf("first handshake: client=%v server=%v", first.clientErr, first.serverErr)
	}
	if first.clientState.DidResume {
		t.Fatalf("first handshake unexpectedly resumed")
	}

	cs, ok := cache.Get(lenientSNI)
	if !ok {
		t.Fatalf("session not cached")
	}
	ticket, state, err := cs.ResumptionState()
	if err != nil || state == nil {
		t.Fatalf("ResumptionState: %v", err)
	}
	// Flip a byte of the opaque identity so neither deployed key generation
	// can authenticate it.
	corrupted := append([]byte(nil), ticket...)
	corrupted[len(corrupted)-1] ^= 0x01
	broken, err := tls.NewResumptionState(corrupted, state)
	if err != nil {
		t.Fatalf("NewResumptionState: %v", err)
	}
	corruptCache := &corruptingCache{session: broken}
	corruptCfg := clientConfig(pki, false)
	corruptCfg.ClientSessionCache = corruptCache

	connState := server.handshake(t, forSNI(corruptCfg, lenientSNI))
	if connState.clientErr != nil || connState.serverErr != nil {
		t.Fatalf("unknown ticket should fall back to a full handshake: client=%v server=%v",
			connState.clientErr, connState.serverErr)
	}
	if connState.clientState.DidResume {
		t.Fatalf("corrupted ticket unexpectedly resumed")
	}
	// Reaching a completed handshake proves the unknown ticket was ignored
	// rather than treated as a fatal alert.
}

// corruptingCache serves a ticket whose bytes have been flipped.
type corruptingCache struct {
	session *tls.ClientSessionState
}

func (c *corruptingCache) Get(string) (*tls.ClientSessionState, bool) {
	return c.session, true
}

func (c *corruptingCache) Put(string, *tls.ClientSessionState) {}
