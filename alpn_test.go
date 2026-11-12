package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const smtpBanner = "220 mail.example.org ESMTP ready\r\n"

func TestPolicySelect(t *testing.T) {
	policy, err := NewPolicy([]string{"smtp", "http/1.1"}, "smtp")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		offered  []string
		want     string
		wantsErr bool
	}{
		{name: "legacy no ALPN", want: "smtp"},
		{name: "single match", offered: []string{"smtp"}, want: "smtp"},
		{name: "multi-value intersection", offered: []string{"http/1.1", "smtp", "ftp"}, want: "smtp"},
		{name: "server preference", offered: []string{"http/1.1", "smtp"}, want: "smtp"},
		{name: "no intersection", offered: []string{"ftp"}, wantsErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := policy.Select(test.offered)
			if test.wantsErr {
				if !errors.Is(err, ErrNoApplicationProtocol) {
					t.Fatalf("Select(%q) error = %v, want ErrNoApplicationProtocol", test.offered, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select(%q): %v", test.offered, err)
			}
			if got != test.want {
				t.Fatalf("Select(%q) = %q, want %q", test.offered, got, test.want)
			}
		})
	}

	noLegacy, err := NewPolicy([]string{"http/1.1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noLegacy.Select(nil); !errors.Is(err, ErrNoApplicationProtocol) {
		t.Fatalf("Select(nil) error = %v, want ErrNoApplicationProtocol", err)
	}
}

func TestPolicyValidation(t *testing.T) {
	if _, err := NewPolicy(nil, ""); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewPolicy(nil, \"\") error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := NewPolicy([]string{"smtp", "smtp"}, ""); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("duplicate protocol error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := NewPolicy([]string{""}, ""); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty protocol error = %v, want ErrInvalidPolicy", err)
	}
}

func TestSMTPRejectsHTTP11WithNoApplicationProtocol(t *testing.T) {
	policy, err := NewPolicy([]string{"smtp"}, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, smtpBanner)

	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tlsVersionName(version), func(t *testing.T) {
			wire, err := rejectedHandshake(server, &tls.Config{
				ServerName:         "mail.example.org",
				InsecureSkipVerify: true,
				NextProtos:         []string{"http/1.1"},
				MinVersion:         version,
				MaxVersion:         version,
			})
			requireNoApplicationProtocol(t, err, wire)

			if bytes.Contains(wire, []byte("220")) {
				t.Fatalf("SMTP banner appeared before ALPN rejection: %q", wire)
			}
			if writes := server.bannerWrites.Load(); writes != 0 {
				t.Fatalf("SMTP banner was written %d times for a rejected HTTP/1.1 client", writes)
			}
		})
	}
}

func TestSMTPNoALPNLegacyAllowed(t *testing.T) {
	policy, err := NewPolicy([]string{"smtp"}, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, smtpBanner)

	raw := server.dial()
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
	})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("legacy SMTP handshake failed: %v", err)
	}

	if got := conn.ConnectionState().NegotiatedProtocol; got != "" {
		t.Fatalf("NegotiatedProtocol = %q, want empty for a client without ALPN", got)
	}
	first := readFirstByte(t, conn)
	if first != '2' {
		t.Fatalf("first application byte = %q, want '2' from SMTP banner", first)
	}
}

func TestSMTPMultiValueIntersection(t *testing.T) {
	policy, err := NewPolicy([]string{"smtp"}, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, smtpBanner)

	raw := server.dial()
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1", "smtp", "ftp"},
	})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("multi-value ALPN handshake failed: %v", err)
	}

	if got := conn.ConnectionState().NegotiatedProtocol; got != "smtp" {
		t.Fatalf("NegotiatedProtocol = %q, want smtp", got)
	}
	if first := readFirstByte(t, conn); first != '2' {
		t.Fatalf("first application byte = %q, want '2' from SMTP banner", first)
	}
}

func TestSMTPValidALPN(t *testing.T) {
	policy, err := NewPolicy([]string{"smtp"}, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, smtpBanner)

	raw := server.dial()
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
		NextProtos:         []string{"smtp"},
	})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("valid SMTP ALPN handshake failed: %v", err)
	}

	if got := conn.ConnectionState().NegotiatedProtocol; got != "smtp" {
		t.Fatalf("NegotiatedProtocol = %q, want smtp", got)
	}
	if first := readFirstByte(t, conn); first != '2' {
		t.Fatalf("first application byte = %q, want '2' from SMTP banner", first)
	}
}

func TestHTTPSRejectsNoALPN(t *testing.T) {
	policy, err := NewPolicy([]string{"h2", "http/1.1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, "HTTP/1.1 200 OK\r\n")

	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tlsVersionName(version), func(t *testing.T) {
			wire, err := rejectedHandshake(server, &tls.Config{
				ServerName:         "mail.example.org",
				InsecureSkipVerify: true,
				MinVersion:         version,
				MaxVersion:         version,
			})
			requireNoApplicationProtocol(t, err, wire)
		})
	}
}

func TestHTTPSDoesNotUseGoHTTP11Fallback(t *testing.T) {
	policy, err := NewPolicy([]string{"h2"}, "")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, "")

	wire, err := rejectedHandshake(server, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	requireNoApplicationProtocol(t, err, wire)
}

func TestHTTPSValidALPN(t *testing.T) {
	policy, err := NewPolicy([]string{"h2", "http/1.1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, "HTTP/1.1 200 OK\r\n")

	raw := server.dial()
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("valid HTTPS handshake failed: %v", err)
	}
	if got := conn.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("NegotiatedProtocol = %q, want http/1.1", got)
	}
	if first := readFirstByte(t, conn); first != 'H' {
		t.Fatalf("first application byte = %q, want 'H' from HTTP banner", first)
	}
}

func TestImplicitFTPAllowsOnlyLegacyWithoutALPN(t *testing.T) {
	policy, err := NewPolicy(nil, "ftp")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, "220 FTP ready\r\n")

	wire, err := rejectedHandshake(server, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	requireNoApplicationProtocol(t, err, wire)
	if writes := server.bannerWrites.Load(); writes != 0 {
		t.Fatalf("FTP banner was written %d times for an ALPN client", writes)
	}

	raw := server.dial()
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName:         "mail.example.org",
		InsecureSkipVerify: true,
	})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("legacy implicit-FTP handshake failed: %v", err)
	}
	if got := conn.ConnectionState().NegotiatedProtocol; got != "" {
		t.Fatalf("NegotiatedProtocol = %q, want empty", got)
	}
	if first := readFirstByte(t, conn); first != '2' {
		t.Fatalf("first application byte = %q, want '2' from FTP banner", first)
	}
}

func TestNoBannerBeforeHandshake(t *testing.T) {
	policy, err := NewPolicy([]string{"smtp"}, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, policy, smtpBanner)

	conn := server.dial()
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(make([]byte, 1))
	if n != 0 {
		t.Fatalf("server sent %d byte(s) before a TLS ClientHello", n)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("pre-handshake read error = %v, want timeout", err)
	}
	if writes := server.bannerWrites.Load(); writes != 0 {
		t.Fatalf("SMTP banner was written %d times before handshake", writes)
	}
}

func rejectedHandshake(server *testServer, config *tls.Config) ([]byte, error) {
	raw := server.dial()
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))

	recorded := &recordingConn{Conn: raw}
	client := tls.Client(recorded, config)
	err := client.Handshake()
	return recorded.Bytes(), err
}

func requireNoApplicationProtocol(t *testing.T, err error, wire []byte) {
	t.Helper()
	if err == nil {
		t.Fatal("handshake unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "no application protocol") {
		t.Fatalf("handshake error = %v, want no_application_protocol", err)
	}
	requireAlertRecord(t, wire, 120)
}

func requireAlertRecord(t *testing.T, wire []byte, want byte) {
	t.Helper()
	for len(wire) >= 5 {
		length := int(binary.BigEndian.Uint16(wire[3:5]))
		if length > len(wire)-5 {
			break
		}
		payload := wire[5 : 5+length]
		if wire[0] == 21 && len(payload) == 2 && payload[0] == 2 && payload[1] == want {
			return
		}
		wire = wire[5+length:]
	}
	t.Fatalf("TLS wire data %q does not contain fatal alert %d", wire, want)
}

func readFirstByte(t *testing.T, conn net.Conn) byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if _, err := io.ReadFull(conn, buf[:]); err != nil {
		t.Fatalf("reading first application byte: %v", err)
	}
	return buf[0]
}

func tlsVersionName(version uint16) string {
	if version == tls.VersionTLS12 {
		return "TLS1.2"
	}
	return "TLS1.3"
}

type testServer struct {
	ln           *Listener
	pipe         *pipeListener
	bannerWrites atomic.Int64
	wg           sync.WaitGroup
}

func newTestServer(t *testing.T, policy Policy, banner string) *testServer {
	t.Helper()

	pipe := newPipeListener()
	ln, err := NewListener(pipe, &tls.Config{
		Certificates: []tls.Certificate{newTestCertificate(t)},
		MinVersion:   tls.VersionTLS12,
	}, policy)
	if err != nil {
		pipe.Close()
		t.Fatal(err)
	}

	server := &testServer{ln: ln, pipe: pipe}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			server.wg.Add(1)
			go func(conn net.Conn) {
				defer server.wg.Done()
				defer conn.Close()
				if banner == "" {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				if _, err := conn.Write([]byte(banner)); err == nil {
					server.bannerWrites.Add(1)
				}
			}(conn)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		server.wg.Wait()
	})
	return server
}

func (s *testServer) dial() net.Conn {
	return s.pipe.Dial()
}

func newTestCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "mail.example.org",
		},
		DNSNames:              []string{"mail.example.org"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
	}
}

type recordingConn struct {
	net.Conn
	mu   sync.Mutex
	read []byte
}

type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn, 1),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return pipeAddr{}
}

func (l *pipeListener) Dial() net.Conn {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
	case <-l.closed:
		server.Close()
		client.Close()
		return nil
	}
	return client
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.read = append(c.read, p[:n]...)
		c.mu.Unlock()
	}
	return n, err
}

func (c *recordingConn) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.read...)
}
