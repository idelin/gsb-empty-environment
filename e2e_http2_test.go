package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type pki struct {
	caCert    *x509.Certificate
	caKey     *ecdsa.PrivateKey
	serverTLS *tls.Certificate
	clientTLS *tls.Certificate
	pool      *x509.CertPool
}

func (p pki) clientTLSConfig(sni string) *tls.Config {
	cfg := &tls.Config{
		RootCAs:    p.pool,
		ServerName: sni,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
	}
	if p.clientTLS != nil {
		cfg.Certificates = []tls.Certificate{*p.clientTLS}
	}
	return cfg
}

func newPKI(t *testing.T, serverNames []string, serverIPs []string, issueClient bool) pki {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serverTmpl := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkix.Name{CommonName: serverNames[0]},
		DNSNames:     serverNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	for _, ip := range serverIPs {
		serverTmpl.IPAddresses = append(serverTmpl.IPAddresses, net.ParseIP(ip))
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	serverLeaf, _ := x509.ParseCertificate(serverDER)
	serverTLS := &tls.Certificate{
		Certificate: [][]byte{serverDER, caDER},
		PrivateKey:  serverKey,
		Leaf:        serverLeaf,
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	out := pki{caCert: caCert, caKey: caKey, serverTLS: serverTLS, pool: pool}

	if issueClient {
		clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		clientTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(42),
			Subject:      pkix.Name{CommonName: "test-client"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("client cert: %v", err)
		}
		clientLeaf, _ := x509.ParseCertificate(clientDER)
		out.clientTLS = &tls.Certificate{
			Certificate: [][]byte{clientDER, caDER},
			PrivateKey:  clientKey,
			Leaf:        clientLeaf,
		}
	}
	return out
}

type testServer struct {
	clientConn net.Conn
	addr       string
}

// pipeListener hands a single pre-accepted TLS connection to http.Server.
type pipeListener struct {
	conn      net.Conn
	closeOnce sync.Once
	done      chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn := l.conn
	if conn == nil {
		<-l.done
		return nil, net.ErrClosed
	}
	l.conn = nil
	return conn, nil
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr("listener") }

// startH2Server starts an HTTP/2 TLS server whose client verifies against the
// given binder. Because the sandbox forbids real sockets, the transport runs
// over an in-memory net.Pipe with a genuine TLS handshake and ALPN "h2"
// negotiation, so all requests multiplex over one connection exactly as on the
// wire. The returned dialer feeds the client side of that single connection.
func startH2Server(t *testing.T, pki pki, binder *Binder, mtls bool, sni string) (*testServer, func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	handler := binder.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Served-Host", r.Host)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*pki.serverTLS},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if mtls {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConfig.ClientCAs = pki.pool
	}

	rawServer, rawClient := net.Pipe()
	serverTLS := tls.Server(rawServer, tlsConfig)

	listener := &pipeListener{conn: serverTLS, done: make(chan struct{})}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()

	clientTLSConfig := pki.clientTLSConfig(sni)
	var clientTLS *tls.Conn
	var handshakeOnce sync.Once
	var handshakeErr error
	dialer := func(_ context.Context, _, _ string) (net.Conn, error) {
		handshakeOnce.Do(func() {
			clientTLS = tls.Client(rawClient, clientTLSConfig)
			handshakeErr = clientTLS.Handshake()
		})
		return clientTLS, handshakeErr
	}

	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = rawServer.Close()
		_ = rawClient.Close()
	})

	return &testServer{clientConn: clientTLS, addr: sni + ":443"}, dialer
}

type pipeAddr string

func (p pipeAddr) Network() string { return "pipe" }
func (p pipeAddr) String() string  { return string(p) }

// h2Transport returns a transport that forces HTTP/2, trusts the test CA and
// dials the single in-memory connection while letting each request set its own
// Host header (and therefore its own ":authority").
func h2Transport(t *testing.T, pki pki, sni string, clientCert *tls.Certificate, dialer func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	t.Helper()
	tlsConfig := &tls.Config{
		RootCAs:            pki.pool,
		ServerName:         sni,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	}
	if clientCert != nil {
		tlsConfig.Certificates = []tls.Certificate{*clientCert}
	}

	tr := &http.Transport{
		TLSClientConfig:     tlsConfig,
		DialTLSContext:      dialer,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		DisableKeepAlives:   false,
	}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

func doRequest(t *testing.T, tr *http.Transport, server *testServer, host string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://"+server.addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip host=%q: %v", host, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestHTTP2CoalescingEndToEnd(t *testing.T) {
	pki := newPKI(t, []string{"a.example", "b.example", "*.wild.example"}, nil, false)
	tracker := NewSNICertTracker()
	if err := tracker.Register(pki.serverTLS, "a.example", "b.example", "www.wild.example"); err != nil {
		t.Fatal(err)
	}

	t.Run("coalesce allowed and SAN covers both hosts", func(t *testing.T) {
		binder := New(
			WithLeafResolver(tracker),
			WithCoalescePolicy(func(_, _ string) bool { return true }),
		)
		server, dialer := startH2Server(t, pki, binder, false, "a.example")
		tr := h2Transport(t, pki, "a.example", nil, dialer)

		if code := doRequest(t, tr, server, "a.example"); code != http.StatusOK {
			t.Fatalf("same host = %d, want 200", code)
		}
		if code := doRequest(t, tr, server, "b.example"); code != http.StatusOK {
			t.Fatalf("coalesced host = %d, want 200", code)
		}
	})

	t.Run("coalesce denied returns 421 on shared conn", func(t *testing.T) {
		binder := New(WithLeafResolver(tracker)) // no coalesce policy
		server, dialer := startH2Server(t, pki, binder, false, "a.example")
		tr := h2Transport(t, pki, "a.example", nil, dialer)

		if code := doRequest(t, tr, server, "a.example"); code != http.StatusOK {
			t.Fatalf("same host = %d, want 200", code)
		}
		if code := doRequest(t, tr, server, "b.example"); code != http.StatusMisdirectedRequest {
			t.Fatalf("cross host = %d, want 421", code)
		}
	})

	t.Run("SAN mismatch returns 421 even when policy allows", func(t *testing.T) {
		sanTracker := NewSNICertTracker()
		// Register the deep wildcard host against the same cert. The cert's
		// *.wild.example SAN matches only a single left-most label, so a
		// two-level-deep request must still be rejected on SAN grounds.
		if err := sanTracker.Register(pki.serverTLS, "a.example", "y.x.wild.example", "www.wild.example"); err != nil {
			t.Fatal(err)
		}
		binder := New(
			WithLeafResolver(sanTracker),
			WithCoalescePolicy(func(_, _ string) bool { return true }),
		)
		server, dialer := startH2Server(t, pki, binder, false, "a.example")
		tr := h2Transport(t, pki, "a.example", nil, dialer)

		// y.x.wild.example needs two wildcard labels; only *.wild.example is issued.
		if code := doRequest(t, tr, server, "y.x.wild.example"); code != http.StatusMisdirectedRequest {
			t.Fatalf("two-label wildcard = %d, want 421", code)
		}
		// One-label wildcard does match and coalescing is permitted.
		if code := doRequest(t, tr, server, "www.wild.example"); code != http.StatusOK {
			t.Fatalf("one-label wildcard = %d, want 200", code)
		}
	})
}

func TestHTTP2ClientCertOwnHandshake(t *testing.T) {
	pki := newPKI(t, []string{"a.example", "secure.example"}, nil, true)
	tracker := NewSNICertTracker()
	if err := tracker.Register(pki.serverTLS, "a.example", "secure.example"); err != nil {
		t.Fatal(err)
	}

	binder := New(
		WithLeafResolver(tracker),
		WithCoalescePolicy(func(_, _ string) bool { return true }),
		WithClientCertPolicy(func(host string) bool { return host == "secure.example" }),
	)
	t.Run("dedicated handshake to mTLS vhost succeeds", func(t *testing.T) {
		server, dialer := startH2Server(t, pki, binder, true, "secure.example")
		tr := h2Transport(t, pki, "secure.example", pki.clientTLS, dialer)
		if code := doRequest(t, tr, server, "secure.example"); code != http.StatusOK {
			t.Fatalf("own mTLS handshake = %d, want 200", code)
		}
	})

	t.Run("coalescing onto mTLS vhost over another handshake is 421", func(t *testing.T) {
		// The connection handshakes as a.example with a client cert, then tries
		// to address secure.example over the very same multiplexed connection.
		server, dialer := startH2Server(t, pki, binder, true, "a.example")
		tr := h2Transport(t, pki, "a.example", pki.clientTLS, dialer)
		if code := doRequest(t, tr, server, "a.example"); code != http.StatusOK {
			t.Fatalf("a.example = %d, want 200", code)
		}
		if code := doRequest(t, tr, server, "secure.example"); code != http.StatusMisdirectedRequest {
			t.Fatalf("cross-host mTLS reuse = %d, want 421", code)
		}
	})
}
