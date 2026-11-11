package ticketkeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

// --- buffered in-memory transport -------------------------------------------

// memConn is an in-memory net.Conn with buffered writes, so concurrent
// handshake flights cannot deadlock the way they do on net.Pipe.
type memConn struct {
	in         chan []byte
	peerIn     chan<- []byte
	peerClosed <-chan struct{}
	closed     chan struct{}
	closeOnce  sync.Once
	rbuf       []byte
}

func memConnPair() (client, server *memConn) {
	aToB := make(chan []byte, 256)
	bToA := make(chan []byte, 256)
	aClosed := make(chan struct{})
	bClosed := make(chan struct{})
	client = &memConn{in: bToA, peerIn: aToB, peerClosed: bClosed, closed: aClosed}
	server = &memConn{in: aToB, peerIn: bToA, peerClosed: aClosed, closed: bClosed}
	return client, server
}

func (c *memConn) Read(b []byte) (int, error) {
	for len(c.rbuf) == 0 {
		select {
		case data := <-c.in:
			c.rbuf = data
		case <-c.peerClosed:
			// The peer sent its final bytes before closing; drain
			// anything still buffered before reporting EOF.
			select {
			case data := <-c.in:
				c.rbuf = data
			default:
				return 0, io.EOF
			}
		case <-c.closed:
			return 0, io.EOF
		}
	}
	n := copy(b, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

func (c *memConn) Write(b []byte) (int, error) {
	data := make([]byte, len(b))
	copy(data, b)
	select {
	case c.peerIn <- data:
		return len(b), nil
	case <-c.closed:
		return 0, errors.New("memConn: write on closed connection")
	}
}

func (c *memConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

func (c *memConn) LocalAddr() net.Addr              { return memAddr("local") }
func (c *memConn) RemoteAddr() net.Addr             { return memAddr("remote") }
func (c *memConn) SetDeadline(time.Time) error      { return nil }
func (c *memConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memConn) SetWriteDeadline(time.Time) error { return nil }

// --- test PKI -------------------------------------------------------------

func newCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func issueCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, dns []string, eku x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     dns,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{eku},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// --- inspectable client session cache --------------------------------------

// testCache implements tls.ClientSessionCache. When constKey is set, all
// sessions are stored under one key so a ticket obtained for one SNI is
// offered to another (Go's own cache keys by server name and would never
// do that).
type testCache struct {
	mu       sync.Mutex
	m        map[string]*tls.ClientSessionState
	constKey bool
	hits     int // Get calls that returned a session
}

func newTestCache(constKey bool) *testCache {
	return &testCache{m: make(map[string]*tls.ClientSessionState), constKey: constKey}
}

func (c *testCache) key(k string) string {
	if c.constKey {
		return "const"
	}
	return k
}

func (c *testCache) Get(key string) (*tls.ClientSessionState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.m[c.key(key)]
	if ok {
		c.hits++
	}
	return s, ok
}

func (c *testCache) Put(key string, s *tls.ClientSessionState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s == nil {
		delete(c.m, c.key(key))
		return
	}
	c.m[c.key(key)] = s
}

func (c *testCache) stored() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *testCache) hitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// snapshot returns the only stored session, for re-injection later.
func (c *testCache) snapshot(t *testing.T) (string, *tls.ClientSessionState) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.m {
		return k, v
	}
	t.Fatal("no session stored")
	return "", nil
}

func (c *testCache) restore(key string, s *tls.ClientSessionState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = map[string]*tls.ClientSessionState{key: s}
}

// --- fixture ----------------------------------------------------------------

type fixture struct {
	issuer     *Issuer
	policy     *Policy
	srvCfg     *tls.Config
	cliCfg     *tls.Config
	clientLeaf *x509.Certificate
}

// newFixture starts a real TLS 1.3 server requiring client certificates,
// with the issuer installed, and returns a client config template.
// tweak, if non-nil, can adjust the server config before the listener starts.
func newFixture(t *testing.T, tweak func(*tls.Config, *Policy), opts ...Option) *fixture {
	t.Helper()
	caCert, caKey := newCA(t)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	srvCert := issueCert(t, caCert, caKey, "server", []string{"a.example", "b.example"}, x509.ExtKeyUsageServerAuth)
	cliCert := issueCert(t, caCert, caKey, "client", nil, x509.ExtKeyUsageClientAuth)
	leaf, err := x509.ParseCertificate(cliCert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	policy := NewPolicy()
	issuer, err := NewIssuer(policy, opts...)
	if err != nil {
		t.Fatal(err)
	}

	srvCfg := &tls.Config{
		Certificates: []tls.Certificate{srvCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
	issuer.Install(srvCfg)
	if tweak != nil {
		tweak(srvCfg, policy)
	}

	return &fixture{
		issuer: issuer,
		policy: policy,
		srvCfg: srvCfg,
		cliCfg: &tls.Config{
			RootCAs:      pool,
			Certificates: []tls.Certificate{cliCert},
			ServerName:   "a.example",
			MinVersion:   tls.VersionTLS13,
			MaxVersion:   tls.VersionTLS13,
		},
		clientLeaf: leaf,
	}
}

func (f *fixture) clientConfig(cache tls.ClientSessionCache) *tls.Config {
	cfg := f.cliCfg.Clone()
	cfg.ClientSessionCache = cache
	return cfg
}

// dial runs a real TLS handshake between the fixture server and a client
// over an in-memory connection, followed by a one-byte application-data
// round trip. The round trip makes the TLS 1.3 client process the
// post-handshake NewSessionTicket messages, so the session cache is
// populated when dial returns.
func (f *fixture) dial(t *testing.T, cfg *tls.Config) (tls.ConnectionState, error) {
	t.Helper()
	clientConn, serverConn := memConnPair()

	serverErr := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		srv := tls.Server(serverConn, f.srvCfg)
		if err := srv.Handshake(); err != nil {
			serverErr <- err
			return
		}
		if _, err := io.ReadFull(srv, make([]byte, 1)); err != nil {
			serverErr <- err
			return
		}
		if _, err := srv.Write([]byte{'p'}); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	cli := tls.Client(clientConn, cfg)
	if err := cli.Handshake(); err != nil {
		clientConn.Close()
		if serr := <-serverErr; serr != nil {
			return tls.ConnectionState{}, fmt.Errorf("client: %w; server: %v", err, serr)
		}
		return tls.ConnectionState{}, err
	}
	if _, err := cli.Write([]byte{'q'}); err != nil {
		clientConn.Close()
		<-serverErr
		return tls.ConnectionState{}, err
	}
	if _, err := io.ReadFull(cli, make([]byte, 1)); err != nil {
		clientConn.Close()
		<-serverErr
		return tls.ConnectionState{}, err
	}
	st := cli.ConnectionState()
	cli.Close()
	<-serverErr
	return st, nil
}

func (f *fixture) mustDial(t *testing.T, cfg *tls.Config) tls.ConnectionState {
	t.Helper()
	st, err := f.dial(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// --- tests -------------------------------------------------------------------

func TestResumptionBasic(t *testing.T) {
	f := newFixture(t, nil)
	cache := newTestCache(false)
	cfg := f.clientConfig(cache)

	if st := f.mustDial(t, cfg); st.DidResume {
		t.Fatal("first connection unexpectedly resumed")
	}
	if cache.stored() == 0 {
		t.Fatal("client did not store a session ticket")
	}
	if st := f.mustDial(t, cfg); !st.DidResume {
		t.Fatal("second connection did not resume")
	}
}

func TestKeyRotation(t *testing.T) {
	f := newFixture(t, nil)
	cache := newTestCache(false)
	cfg := f.clientConfig(cache)

	f.mustDial(t, cfg)
	key, ticket := cache.snapshot(t)

	// One rotation: the ticket was encrypted under the previous key and
	// must still be accepted.
	if err := f.issuer.Rotate(); err != nil {
		t.Fatal(err)
	}
	if st := f.mustDial(t, cfg); !st.DidResume {
		t.Fatal("ticket under previous key was not accepted")
	}

	// Two rotations: the original key is forgotten, the old ticket must be
	// rejected and the handshake must fall back to a full handshake.
	if err := f.issuer.Rotate(); err != nil {
		t.Fatal(err)
	}
	cache.restore(key, ticket)
	if st := f.mustDial(t, cfg); st.DidResume {
		t.Fatal("ticket under forgotten key was resumed")
	}
	if cache.hitCount() == 0 {
		t.Fatal("client never offered the ticket; test is vacuous")
	}

	// The full handshake above issued a fresh ticket under the newest key,
	// which resumes fine.
	if st := f.mustDial(t, cfg); !st.DidResume {
		t.Fatal("ticket under newest key was not accepted")
	}
}

func TestRevokedCertificate(t *testing.T) {
	t.Run("FallbackToFullHandshake", func(t *testing.T) {
		f := newFixture(t, nil)
		cache := newTestCache(false)
		cfg := f.clientConfig(cache)

		f.mustDial(t, cfg)
		f.policy.Revoke(Fingerprint(f.clientLeaf))

		// Resumption must be rejected; the connection still succeeds via a
		// full handshake (the certificate itself is not TLS-level invalid).
		st := f.mustDial(t, cfg)
		if st.DidResume {
			t.Fatal("resumed with a revoked client certificate")
		}
		if cache.hitCount() == 0 {
			t.Fatal("client never offered the ticket; test is vacuous")
		}
	})

	t.Run("FullHandshakeAlsoBlocked", func(t *testing.T) {
		// Enforce revocation at the TLS layer too, so a revoked client
		// cannot get back in through the fallback full handshake.
		f := newFixture(t, func(cfg *tls.Config, p *Policy) {
			cfg.VerifyPeerCertificate = p.VerifyPeerCertificate
		})
		cache := newTestCache(false)
		cfg := f.clientConfig(cache)

		f.mustDial(t, cfg)
		f.policy.Revoke(Fingerprint(f.clientLeaf))

		if _, err := f.dial(t, cfg); err == nil {
			t.Fatal("revoked client completed a handshake")
		}
	})
}

func TestCrossSNIRejected(t *testing.T) {
	f := newFixture(t, nil)
	cache := newTestCache(true) // offer tickets across server names

	cfgA := f.clientConfig(cache)
	f.mustDial(t, cfgA)

	// The ticket bound to a.example must not resume against b.example.
	cfgB := f.clientConfig(cache)
	cfgB.ServerName = "b.example"
	if st := f.mustDial(t, cfgB); st.DidResume {
		t.Fatal("ticket issued for a.example resumed against b.example")
	}
	if cache.hitCount() == 0 {
		t.Fatal("client never offered the ticket; test is vacuous")
	}

	// The full handshake for b.example issued a b.example-bound ticket,
	// which resumes fine against b.example.
	if st := f.mustDial(t, cfgB); !st.DidResume {
		t.Fatal("ticket bound to b.example did not resume against b.example")
	}

	// ...but not against a.example anymore.
	if st := f.mustDial(t, cfgA); st.DidResume {
		t.Fatal("ticket issued for b.example resumed against a.example")
	}
}

func TestTicketExpiry(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	f := newFixture(t, nil, WithClock(clock), WithLifetime(time.Minute))
	cache := newTestCache(false)
	cfg := f.clientConfig(cache)

	f.mustDial(t, cfg)
	advance(2 * time.Minute)

	if st := f.mustDial(t, cfg); st.DidResume {
		t.Fatal("expired ticket was resumed")
	}
	if cache.hitCount() == 0 {
		t.Fatal("client never offered the ticket; test is vacuous")
	}
}

func TestPolicyGeneration(t *testing.T) {
	f := newFixture(t, nil)
	cache := newTestCache(false)
	cfg := f.clientConfig(cache)

	f.mustDial(t, cfg)
	f.policy.BumpGeneration()

	if st := f.mustDial(t, cfg); st.DidResume {
		t.Fatal("ticket from an older policy generation was resumed")
	}
	if cache.hitCount() == 0 {
		t.Fatal("client never offered the ticket; test is vacuous")
	}

	// The fallback full handshake issued a ticket under the new generation.
	if st := f.mustDial(t, cfg); !st.DidResume {
		t.Fatal("ticket under the new policy generation did not resume")
	}
}
