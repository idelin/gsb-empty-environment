package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memoryNetwork lets the tests use the real net/http client and server stacks
// without binding sockets. Each fakeSite is still a separate origin with its
// own host:port and, optionally, its own TLS listener.
type memoryNetwork struct {
	mu    sync.Mutex
	sites map[string]*pipeListener
}

type fakeSite struct {
	server   *http.Server
	listener *pipeListener
	URL      string
}

func newMemoryNetwork() *memoryNetwork {
	return &memoryNetwork{sites: make(map[string]*pipeListener)}
}

var nextTestPort atomic.Int32

func init() {
	nextTestPort.Store(21000)
}

func (n *memoryNetwork) Add(t *testing.T, scheme string, handler http.Handler) *fakeSite {
	t.Helper()

	port := int(nextTestPort.Add(1))
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	listener := newPipeListener(address)

	var serveListener net.Listener = listener
	if scheme == "https" {
		serveListener = tls.NewListener(listener, testTLSConfig(t))
	}

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(serveListener)
	}()

	n.mu.Lock()
	n.sites[address] = listener
	n.mu.Unlock()

	return &fakeSite{
		server:   server,
		listener: listener,
		URL:      scheme + "://" + address,
	}
}

func (s *fakeSite) Close() {
	_ = s.server.Close()
	_ = s.listener.Close()
}

func (n *memoryNetwork) Transport() *http.Transport {
	return &http.Transport{
		DialContext: n.dialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableKeepAlives: true,
	}
}

func (n *memoryNetwork) Client() *http.Client {
	return &http.Client{Transport: n.Transport()}
}

func (n *memoryNetwork) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	n.mu.Lock()
	listener := n.sites[address]
	n.mu.Unlock()
	if listener == nil {
		return nil, fmt.Errorf("memoryNetwork: unknown address %q", address)
	}

	serverConn, clientConn := net.Pipe()
	select {
	case listener.connections <- serverConn:
		return clientConn, nil
	case <-listener.closed:
		_ = serverConn.Close()
		_ = clientConn.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = serverConn.Close()
		_ = clientConn.Close()
		return nil, ctx.Err()
	}
}

type pipeListener struct {
	address     string
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newPipeListener(address string) *pipeListener {
	return &pipeListener{
		address:     address,
		connections: make(chan net.Conn),
		closed:      make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return pipeAddr(l.address)
}

type pipeAddr string

func (a pipeAddr) Network() string { return "memory" }
func (a pipeAddr) String() string  { return string(a) }

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "memory test site"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der},
			PrivateKey:  key,
		}},
	}
}
