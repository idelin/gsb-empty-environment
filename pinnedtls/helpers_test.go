package pinnedtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func mustKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}

func newRootCA(commonName string) *testCA {
	key := mustKey()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &testCA{cert: cert, key: key}
}

// newIntermediate issues an intermediate under parent. Passing a non-nil key
// reuses an existing key, which is how the cross-signed intermediate shares one
// key across two certificates with different issuers.
func newIntermediate(commonName string, parent *testCA, key *ecdsa.PrivateKey) *testCert {
	if key == nil {
		key = mustKey()
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, &key.PublicKey, parent.key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &testCert{cert: cert, key: key}
}

func newLeaf(commonName string, parent *testCert) *testCert {
	key := mustKey()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, &key.PublicKey, parent.key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &testCert{cert: cert, key: key}
}

func rootPool(roots ...*testCA) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, root := range roots {
		pool.AddCert(root.cert)
	}
	return pool
}

func tlsCert(leaf *testCert, intermediates ...*testCert) tls.Certificate {
	c := tls.Certificate{
		Certificate: [][]byte{leaf.cert.Raw},
		PrivateKey:  leaf.key,
		Leaf:        leaf.cert,
	}
	for _, intermediate := range intermediates {
		c.Certificate = append(c.Certificate, intermediate.cert.Raw)
	}
	return c
}

type testServer struct {
	listener *pipeListener
	address  string
	config   *tls.Config

	handshakes    atomic.Int64
	resumptions   atomic.Int64
	fullHandshake atomic.Int64
	lastError     error
	mu            sync.Mutex
}

func startTestServer(serverCert tls.Certificate) *testServer {
	return startTestServerWithConfig(&tls.Config{Certificates: []tls.Certificate{serverCert}})
}

func startTestServerWithConfig(config *tls.Config) *testServer {
	listener := newPipeListener()
	server := &testServer{
		listener: listener,
		address:  listener.Addr().String(),
		config:   config,
	}

	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go func(raw net.Conn) {
				defer func() {
					if recovered := recover(); recovered != nil {
						server.mu.Lock()
						server.lastError = fmt.Errorf("server panic: %v", recovered)
						server.mu.Unlock()
					}
				}()
				server.serve(raw)
			}(raw)
		}
	}()
	return server
}

func (s *testServer) serve(raw net.Conn) {
	conn := tls.Server(raw, s.config)
	if err := conn.Handshake(); err != nil {
		_ = conn.Close()
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return
	}
	defer conn.Close()

	s.handshakes.Add(1)
	if conn.ConnectionState().DidResume {
		s.resumptions.Add(1)
	} else {
		s.fullHandshake.Add(1)
	}

	// Keep the connection open until the client tears it down. With the
	// synchronous net.Pipe transport this is required for post-handshake
	// messages such as the TLS 1.3 NewSessionTicket to reach the client.
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
}

func (s *testServer) error() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastError
}

func (s *testServer) close() { _ = s.listener.Close() }

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func base64Raw(pin SPKIPin) string {
	return base64.RawStdEncoding.EncodeToString(pin[:])
}

// pipeListener is an in-memory net.Listener backed by net.Pipe so tests need
// no real socket permissions.
type pipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		ch:     make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// dial creates a connected pair and hands the server end to the listener.
func (l *pipeListener) dial() (net.Conn, error) {
	client, server := newConnPair()
	select {
	case l.ch <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "piped:example.test" }

// bufferedConn is an in-memory, buffered net.Conn. Unlike net.Pipe its writes
// never block waiting for a peer read, which TLS handshakes and post-handshake
// messages such as close_notify and NewSessionTicket require.
type bufferedConn struct {
	recv          chan []byte
	peer          *bufferedConn
	closed        chan struct{}
	readBuf       []byte
	deadlineMu    sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
}

func newConnPair() (net.Conn, net.Conn) {
	a := &bufferedConn{recv: make(chan []byte, 64), closed: make(chan struct{})}
	b := &bufferedConn{recv: make(chan []byte, 64), closed: make(chan struct{})}
	a.peer, b.peer = b, a
	return a, b
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	for len(c.readBuf) == 0 {
		timeout := c.readTimer()
		select {
		case data, ok := <-c.recv:
			if !ok {
				return 0, net.ErrClosed
			}
			c.readBuf = data
		case <-c.closed:
			return 0, net.ErrClosed
		case <-c.peer.closed:
			// Drain anything still queued before reporting EOF.
			select {
			case data, ok := <-c.recv:
				if ok {
					c.readBuf = data
					break
				}
				return 0, net.ErrClosed
			default:
				return 0, net.ErrClosed
			}
		case <-timeout:
			return 0, errTimeout{}
		}
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *bufferedConn) Write(p []byte) (int, error) {
	timeout := c.writeTimer()
	copyOf := append([]byte(nil), p...)
	select {
	case c.peer.recv <- copyOf:
		return len(p), nil
	case <-c.closed:
		return 0, net.ErrClosed
	case <-c.peer.closed:
		return 0, net.ErrClosed
	case <-timeout:
		return 0, errTimeout{}
	}
}

func (c *bufferedConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *bufferedConn) LocalAddr() net.Addr  { return pipeAddr{} }
func (c *bufferedConn) RemoteAddr() net.Addr { return pipeAddr{} }

func (c *bufferedConn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.readDeadline, c.writeDeadline = t, t
	return nil
}

func (c *bufferedConn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.readDeadline = t
	return nil
}

func (c *bufferedConn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.writeDeadline = t
	return nil
}

func (c *bufferedConn) readTimer() <-chan time.Time {
	c.deadlineMu.Lock()
	deadline := c.readDeadline
	c.deadlineMu.Unlock()
	if deadline.IsZero() {
		return nil
	}
	return time.After(time.Until(deadline))
}

func (c *bufferedConn) writeTimer() <-chan time.Time {
	c.deadlineMu.Lock()
	deadline := c.writeDeadline
	c.deadlineMu.Unlock()
	if deadline.IsZero() {
		return nil
	}
	return time.After(time.Until(deadline))
}

type errTimeout struct{}

func (errTimeout) Error() string   { return "i/o timeout" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }
