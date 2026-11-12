package pqpin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

// testCertificate builds a self-signed certificate for in-process TLS.
func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	tmpl.Raw = der
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: tmpl}
}

// pipeAddr is a fixed net.Addr so TLS session keys and pqpin server IDs stay
// stable across a net.Pipe pair.
type pipeAddr struct{ name string }

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return a.name }

// pipeBuffer is a one-direction, condition-variable-backed byte stream. Writes
// never block on a reader (the slice grows), which lets one TLS peer emit
// several records before the other side schedules, and Close broadcasts EOF.
type pipeBuffer struct {
	mu   sync.Mutex
	cond *sync.Cond
	data []byte
	err  error
}

func newPipeBuffer() *pipeBuffer {
	b := &pipeBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *pipeBuffer) write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, b.err
	}
	b.data = append(b.data, p...)
	b.cond.Broadcast()
	return len(p), nil
}

func (b *pipeBuffer) read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.data) == 0 && b.err == nil {
		b.cond.Wait()
	}
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	return 0, b.err
}

func (b *pipeBuffer) close(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

// bufferedConn is one endpoint of an in-memory full-duplex connection.
type bufferedConn struct {
	rx, tx        *pipeBuffer
	local, remote net.Addr
	once          sync.Once
}

func (c *bufferedConn) Read(p []byte) (int, error)  { return c.rx.read(p) }
func (c *bufferedConn) Write(p []byte) (int, error) { return c.tx.write(p) }
func (c *bufferedConn) Close() error {
	c.once.Do(func() {
		c.rx.close(io.EOF)
		c.tx.close(errPipeClosed)
	})
	return nil
}
func (c *bufferedConn) LocalAddr() net.Addr              { return c.local }
func (c *bufferedConn) RemoteAddr() net.Addr             { return c.remote }
func (c *bufferedConn) SetDeadline(time.Time) error      { return nil }
func (c *bufferedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *bufferedConn) SetWriteDeadline(time.Time) error { return nil }

var errPipeClosed = errors.New("buffered pipe: closed")

// bufferedPipe returns two connected, independently buffered connections.
func bufferedPipe(local, remote net.Addr) (net.Conn, net.Conn) {
	a2b := newPipeBuffer()
	b2a := newPipeBuffer()
	a := &bufferedConn{rx: b2a, tx: a2b, local: local, remote: remote}
	b := &bufferedConn{rx: a2b, tx: b2a, local: remote, remote: local}
	return a, b
}

// pipePair returns a client/server pair where the client's remote address is
// "127.0.0.1:<port>", matching how the production Dialer derives serverID.
func pipePair(port string) (net.Conn, net.Conn) {
	serverID := "127.0.0.1:" + port
	return bufferedPipe(pipeAddr{"pipe:client"}, pipeAddr{serverID})
}

// runTLSServer completes one server handshake, signals readiness, and keeps the
// connection open until the client side is done. It returns the server's
// tls.Conn so the test can close it deterministically instead of racing on the
// unbuffered net.Pipe.
func runTLSServer(t *testing.T, conn net.Conn, cfg *tls.Config, ready chan *tls.Conn, done chan error) {
	t.Helper()
	srv := tls.Server(conn, cfg)
	if err := srv.Handshake(); err != nil {
		done <- err
		return
	}
	ready <- srv
	// Hold the connection open; the test closes the server conn explicitly.
	// A read on the pipe unblocks when the peer closes.
	buf := make([]byte, 1)
	_, _ = srv.Read(buf)
	done <- nil
}

// stripConn models an on-path attacker editing the ClientHello in flight. It
// sits on the *server's* read side: the client keeps hashing its original
// ClientHello (as it would against a real network attacker), while the bytes
// handed to the server have every hybrid group removed from supported_groups
// and key_share. Only the first record is rewritten; later records pass
// through unchanged.
type stripConn struct {
	inner   net.Conn
	mu      sync.Mutex
	first   bool
	pending []byte
}

func newServerSideStrip(inner net.Conn) net.Conn {
	return &stripConn{inner: inner, first: true}
}

func (c *stripConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}

	if !c.first {
		return c.inner.Read(p)
	}

	// Accumulate the entire first record so the rewrite never sees a
	// truncated ClientHello (the MLKEM key share makes it large).
	var acc []byte
	header := make([]byte, 5)
	if _, err := io.ReadFull(c.inner, header); err != nil {
		return 0, err
	}
	recordLen := 5 + int(header[3])<<8 + int(header[4])
	acc = make([]byte, recordLen)
	copy(acc, header)
	if _, err := io.ReadFull(c.inner, acc[5:]); err != nil {
		return 0, err
	}
	c.first = false

	rewritten, ok := stripHybridGroups(acc)
	if !ok {
		rewritten = acc
	}
	n := copy(p, rewritten)
	if n < len(rewritten) {
		c.pending = append(c.pending, rewritten[n:]...)
	}
	return n, nil
}

func (c *stripConn) Write(p []byte) (int, error)        { return c.inner.Write(p) }
func (c *stripConn) Close() error                       { return c.inner.Close() }
func (c *stripConn) LocalAddr() net.Addr                { return c.inner.LocalAddr() }
func (c *stripConn) RemoteAddr() net.Addr               { return c.inner.RemoteAddr() }
func (c *stripConn) SetDeadline(t time.Time) error      { return c.inner.SetDeadline(t) }
func (c *stripConn) SetReadDeadline(t time.Time) error  { return c.inner.SetReadDeadline(t) }
func (c *stripConn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }

// recordLength reports the total length (5-byte header included) of the first
// TLS record in b once enough bytes have arrived.
func recordLength(b []byte) (int, bool) {
	if len(b) < 5 {
		return 0, false
	}
	total := 5 + int(b[3])<<8 + int(b[4])
	if len(b) < total {
		return 0, false
	}
	return total, true
}

// testHarness bundles a pinner, dialer config and a fixed virtual endpoint.
type testHarness struct {
	t        *testing.T
	pinner   *Pinner
	client   *tls.Config
	port     string
	serverID string
	dialer   *Dialer
	servers  []*tls.Conn
}

func newTestHarness(t *testing.T, pinner *Pinner, clientCfg *tls.Config, port string) *testHarness {
	if clientCfg == nil {
		clientCfg = &tls.Config{InsecureSkipVerify: true}
	}
	h := &testHarness{
		t:        t,
		pinner:   pinner,
		client:   clientCfg,
		port:     port,
		serverID: "127.0.0.1:" + port,
		dialer:   &Dialer{Pinner: pinner, TLSConfig: clientCfg},
	}
	t.Cleanup(h.closeAll)
	return h
}

// dial runs one in-process TLS connection. When strip is true the client's
// first ClientHello passes through the hybrid-group stripper.
func (h *testHarness) dial(serverCfg *tls.Config, strip bool) (*tls.Conn, error) {
	clientRaw, serverRaw := pipePair(h.port)
	if strip {
		// Rewrite only what the server reads; the client's local transcript
		// keeps the original ClientHello, matching a real in-flight attack.
		serverRaw = newServerSideStrip(serverRaw)
	}
	serverReady := make(chan *tls.Conn, 1)
	serverDone := make(chan error, 1)
	go runTLSServer(h.t, serverRaw, serverCfg, serverReady, serverDone)

	conn, err := h.dialer.dialConn(h.serverID, clientRaw)
	select {
	case srv := <-serverReady:
		if srv != nil {
			h.servers = append(h.servers, srv)
		}
	case serr := <-serverDone:
		if err == nil && serr != nil {
			return nil, serr
		}
	}

	// Surface a server-side failure only when the client reported success, so
	// client downgrade errors remain the primary assertion.
	if err == nil {
		// TLS 1.3 delivers NewSessionTicket as a post-handshake message that
		// crypto/tls only processes on Read. Pump once so the session is cached
		// before the connection closes.
		drainTickets(conn)
		if serr := nonBlocking(serverDone); serr != nil {
			return nil, serr
		}
	}
	return conn, err
}

// drainTickets drives a read in the background so crypto/tls processes any
// buffered post-handshake NewSessionTicket and stores the session. The test
// pipe has no real deadlines, so the read simply unblocks when the connection
// is closed during cleanup.
func drainTickets(conn *tls.Conn) {
	go func() {
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
	}()
	// Give the post-handshake message a chance to be processed.
	time.Sleep(20 * time.Millisecond)
}

// closeAll shuts down every server connection accumulated by the harness,
// which unblocks their read goroutines and lets tests finish without timers.
func (h *testHarness) closeAll() {
	for _, srv := range h.servers {
		_ = srv.Close()
	}
	h.servers = nil
}

func nonBlocking(ch chan error) error {
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}

// serverConfig builds a TLS 1.3 server config with optional curve restrictions.
func serverConfig(t *testing.T, curves ...tls.CurveID) *tls.Config {
	t.Helper()
	cfg := &tls.Config{
		Certificates: []tls.Certificate{testCertificate(t)},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
	if len(curves) > 0 {
		cfg.CurvePreferences = curves
	}
	return cfg
}
