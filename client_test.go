package netsec

import (
	"bufio"
	"context"
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

// Buffered in-memory net.Conn pair. net.Pipe is unbuffered and can deadlock
// during a TLS 1.3 handshake, where both peers write certificate flights
// before either reads.
type memPipeBuffer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	closed bool
}

func newMemPipeBuffer() *memPipeBuffer {
	b := &memPipeBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *memPipeBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.data) == 0 && !b.closed {
		b.cond.Wait()
	}
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *memPipeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	b.data = append(b.data, p...)
	b.cond.Broadcast()
	return len(p), nil
}

func (b *memPipeBuffer) Close() {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

type memConn struct {
	reader *memPipeBuffer
	writer *memPipeBuffer
}

func (c *memConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *memConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *memConn) Close() error {
	c.reader.Close()
	c.writer.Close()
	return nil
}
func (c *memConn) LocalAddr() net.Addr              { return memAddr("local") }
func (c *memConn) RemoteAddr() net.Addr             { return memAddr("remote") }
func (c *memConn) SetDeadline(time.Time) error      { return nil }
func (c *memConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memConn) SetWriteDeadline(time.Time) error { return nil }

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

func newMemPipe() (net.Conn, net.Conn) {
	ab := newMemPipeBuffer()
	ba := newMemPipeBuffer()
	return &memConn{reader: ba, writer: ab}, &memConn{reader: ab, writer: ba}
}

type serverBind struct {
	DN       string
	Password string
	OverTLS  bool
}

type testLDAPServer struct {
	tlsConfig    *tls.Config
	startTLS     bool
	startTLSFail bool
	binds        chan serverBind
	done         chan struct{}
}

func newTestLDAPServer(t *testing.T, cert tls.Certificate, startTLS, startTLSFail bool, serverConn net.Conn) *testLDAPServer {
	t.Helper()
	s := &testLDAPServer{
		tlsConfig:    &tls.Config{Certificates: []tls.Certificate{cert}},
		startTLS:     startTLS,
		startTLSFail: startTLSFail,
		binds:        make(chan serverBind, 16),
		done:         make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		s.serve(serverConn)
	}()
	t.Cleanup(func() {
		serverConn.Close()
		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Error("test LDAP server did not stop")
		}
	})
	return s
}

func (s *testLDAPServer) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	overTLS := false

	if !s.startTLS {
		tlsConn := tls.Server(conn, s.tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
		reader = bufio.NewReader(tlsConn)
		writer = bufio.NewWriter(tlsConn)
		overTLS = true
	}

	for {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		id, tag, content, err := readTestLDAPMessage(reader)
		if err != nil {
			return
		}
		switch tag {
		case 0x77: // StartTLS
			code := 0
			if s.startTLSFail {
				code = 53
			}
			if err := writeTestLDAPMessage(writer, id, berElement(0x78, testLDAPResult(code, ""))); err != nil {
				return
			}
			if code != 0 {
				return
			}
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(tlsConn)
			writer = bufio.NewWriter(tlsConn)
			overTLS = true
		case 0x60: // BindRequest
			dn, password, err := parseTestBindRequest(content)
			if err != nil {
				return
			}
			s.binds <- serverBind{DN: dn, Password: password, OverTLS: overTLS}
			if err := writeTestLDAPMessage(writer, id, berElement(0x61, testLDAPResult(0, ""))); err != nil {
				return
			}
		case 0x63: // SearchRequest
			entry := berElement(0x64, concat(
				berOctetString("uid=alice,ou=users"),
				berSequence(berSequence(
					berOctetString("uid"),
					berSet(berOctetString("alice")),
				)),
			))
			if err := writeTestLDAPMessage(writer, id, entry); err != nil {
				return
			}
			if err := writeTestLDAPMessage(writer, id, berElement(0x65, testLDAPResult(0, ""))); err != nil {
				return
			}
		case 0x42: // Unbind
			return
		default:
			return
		}
	}
}

func testLDAPResult(code int, diagnostic string) []byte {
	return concat(berEnumerated(code), berOctetString(""), berOctetString(diagnostic))
}

func readTestLDAPMessage(r *bufio.Reader) (int32, byte, []byte, error) {
	tag, content, err := readBERElement(r)
	if err != nil {
		return 0, 0, nil, err
	}
	if tag != berTagSequence {
		return 0, 0, nil, fmt.Errorf("not a sequence")
	}
	tag, idContent, rest, err := readBERElementBytes(content)
	if err != nil || tag != berTagInteger {
		return 0, 0, nil, fmt.Errorf("missing message id")
	}
	id, err := parseBERInt(idContent)
	if err != nil {
		return 0, 0, nil, err
	}
	tag, op, trailing, err := readBERElementBytes(rest)
	if err != nil || len(trailing) != 0 {
		return 0, 0, nil, fmt.Errorf("malformed message")
	}
	return int32(id), tag, op, nil
}

func writeTestLDAPMessage(w *bufio.Writer, id int32, op []byte) error {
	if _, err := w.Write(berSequence(berInteger(int(id)), op)); err != nil {
		return err
	}
	return w.Flush()
}

func parseTestBindRequest(content []byte) (string, string, error) {
	tag, _, rest, err := readBERElementBytes(content)
	if err != nil || tag != berTagInteger {
		return "", "", fmt.Errorf("missing version")
	}
	tag, dn, rest, err := readBERElementBytes(rest)
	if err != nil || tag != berTagOctet {
		return "", "", fmt.Errorf("missing DN")
	}
	tag, password, _, err := readBERElementBytes(rest)
	if err != nil || tag != 0x80 {
		return "", "", fmt.Errorf("missing simple password")
	}
	return string(dn), string(password), nil
}

func makeTestCertificate(t *testing.T, dnsNames []string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: dnsNames[0]},
		DNSNames:              dnsNames,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
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
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return cert, pool
}

func waitForServerBind(t *testing.T, ch <-chan serverBind) serverBind {
	t.Helper()
	select {
	case bind := <-ch:
		return bind
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bind")
		return serverBind{}
	}
}

func assertNoServerBind(t *testing.T, ch <-chan serverBind) {
	t.Helper()
	select {
	case bind := <-ch:
		t.Fatalf("unexpected bind: %#v", bind)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestLDAPSValidatesCertificateAndHost(t *testing.T) {
	cert, roots := makeTestCertificate(t, []string{"localhost"})
	clientConn, serverConn := newMemPipe()
	server := newTestLDAPServer(t, cert, false, false, serverConn)
	dialer := &LDAPDialer{
		TLSConfig: &tls.Config{RootCAs: roots},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	}

	f, err := NewFollower(Config{
		TrustedDomains:            []string{"localhost"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://localhost/ou=users",
		BindDN:   "cn=gateway",
		Password: "secret",
		Source:   "client",
		Filter:   "(uid=alice)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Attributes["uid"][0] != "alice" {
		t.Fatalf("entries = %#v", result.Entries)
	}
	bind := waitForServerBind(t, server.binds)
	if !bind.OverTLS || bind.DN != "cn=gateway" || bind.Password != "secret" {
		t.Fatalf("bind = %#v", bind)
	}

	wrongCert, wrongRoots := makeTestCertificate(t, []string{"wrong.example"})
	wrongClient, wrongServerConn := newMemPipe()
	wrongServer := newTestLDAPServer(t, wrongCert, false, false, wrongServerConn)
	wrongDialer := &LDAPDialer{
		TLSConfig: &tls.Config{RootCAs: wrongRoots},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return wrongClient, nil
		},
	}
	f, err = NewFollower(Config{
		TrustedDomains:            []string{"localhost"},
		AllowCredentialForwarding: true,
		Dialer:                    wrongDialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Follow(context.Background(), Request{
		Server:   "ldaps://localhost/ou=users",
		BindDN:   "cn=gateway",
		Password: "secret",
	})
	if err == nil {
		t.Fatal("LDAPS with a certificate for another host succeeded")
	}
	assertNoServerBind(t, wrongServer.binds)
}

func TestStartTLSUpgradeBeforeBind(t *testing.T) {
	cert, roots := makeTestCertificate(t, []string{"localhost"})
	clientConn, serverConn := newMemPipe()
	server := newTestLDAPServer(t, cert, true, false, serverConn)
	audit := &MemoryAudit{}
	dialer := &LDAPDialer{
		TLSConfig: &tls.Config{RootCAs: roots},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"localhost"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
		Audit:                     audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Follow(context.Background(), Request{
		Server:   "ldap://localhost/ou=users",
		BindDN:   "cn=gateway",
		Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	bind := waitForServerBind(t, server.binds)
	if !bind.OverTLS {
		t.Fatal("bind was sent before the StartTLS upgrade")
	}
	var foundTransport bool
	for _, event := range audit.Events() {
		if event.Kind == "bind" && event.Transport == TLSModeStartTLS {
			foundTransport = true
		}
	}
	if !foundTransport {
		t.Fatal("audit did not record the StartTLS transport")
	}
}

func TestStartTLSFailureNeverFallsBackToCleartext(t *testing.T) {
	cert, roots := makeTestCertificate(t, []string{"localhost"})
	clientConn, serverConn := newMemPipe()
	server := newTestLDAPServer(t, cert, true, true, serverConn)
	dialer := &LDAPDialer{
		TLSConfig: &tls.Config{RootCAs: roots},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"localhost"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Follow(context.Background(), Request{
		Server:   "ldap://localhost/ou=users",
		BindDN:   "cn=gateway",
		Password: "secret",
	})
	if err == nil {
		t.Fatal("StartTLS failure was ignored")
	}
	assertNoServerBind(t, server.binds)
}

func TestStartTLSValidatesHostIdentity(t *testing.T) {
	cert, roots := makeTestCertificate(t, []string{"wrong.example"})
	clientConn, serverConn := newMemPipe()
	server := newTestLDAPServer(t, cert, true, false, serverConn)
	dialer := &LDAPDialer{
		TLSConfig: &tls.Config{RootCAs: roots},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"localhost"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Follow(context.Background(), Request{
		Server:   "ldap://localhost/ou=users",
		BindDN:   "cn=gateway",
		Password: "secret",
	})
	if err == nil {
		t.Fatal("StartTLS with a certificate for another host succeeded")
	}
	assertNoServerBind(t, server.binds)
}

func TestInsecureSkipVerifyIsRejected(t *testing.T) {
	if _, err := NewFollower(Config{
		TrustedDomains: []string{"corp.example.com"},
		TLSConfig:      &tls.Config{InsecureSkipVerify: true},
	}); err == nil {
		t.Fatal("InsecureSkipVerify was accepted")
	}
}

func TestDialerRequiresExplicitCleartext(t *testing.T) {
	d := &LDAPDialer{}
	_, err := d.Dial(context.Background(), Target{
		Scheme: SchemeLDAP,
		Host:   "localhost",
		Port:   389,
		Mode:   TLSModeNone,
	})
	if !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("cleartext dial error = %v, want ErrTLSRequired", err)
	}
}
