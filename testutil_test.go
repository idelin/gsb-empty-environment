package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"
)

// pipeListener is an in-memory net.Listener used because the sandbox forbids
// binding real sockets. Accept hands back one end of a net.Pipe; the client
// side receives the other through Dial.
type pipeListener struct {
	ch     chan net.Conn
	addr   pipeAddr
	closed chan struct{}
	once   sync.Once
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

func newPipeListener() *pipeListener {
	return &pipeListener{ch: make(chan net.Conn), addr: pipeAddr{}, closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn, ok := <-l.ch:
		if !ok {
			return nil, net.ErrClosed
		}
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

// Dial returns the client end of a fresh net.Pipe and queues the server end.
func (l *pipeListener) Dial(ctx context.Context, _, _ string) (net.Conn, error) {
	server, client := net.Pipe()

	// Hand the server end to Accept in its own goroutine: net.Pipe writes
	// block until a peer reads, so doing this inline would deadlock TLS.
	done := make(chan struct{})
	go func() {
		select {
		case l.ch <- server:
			close(done)
		case <-l.closed:
			_ = server.Close()
			close(done)
		case <-ctx.Done():
			_ = server.Close()
			close(done)
		}
	}()
	select {
	case <-done:
		return client, nil
	case <-ctx.Done():
		_ = server.Close()
		_ = client.Close()
		return nil, ctx.Err()
	}
}

// startTLSPipeServer serves handler over real TLS on in-memory pipes using a
// self-signed certificate valid for serverName. It returns the certificate
// pool clients must trust.
func startTLSPipeServer(tb interface {
	Helper()
	Fatalf(string, ...any)
}, listener *pipeListener, serverName string, handler http.Handler) *x509.CertPool {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: serverName},
		DNSNames:     []string{serverName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatalf("create certificate: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatalf("parse certificate: %v", err)
	}

	server := &http.Server{
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	go func() { _ = server.ServeTLS(listener, "", "") }()

	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return pool
}
