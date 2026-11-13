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
	"math/big"
	"net"
	"testing"
	"time"
)

type testCA struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

type testLeaf struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	der         []byte
}

var testSerial int64

func nextSerial() *big.Int {
	testSerial++
	return big.NewInt(testSerial)
}

func newTestCA(t *testing.T, commonName string, parent *testCA) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            2,
	}
	if parent == nil {
		parent = &testCA{certificate: template, key: key}
		template.SubjectKeyId = []byte{1, 2, 3, 4}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.certificate, &key.PublicKey, parent.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{certificate: certificate, key: key}
}

func newTestLeaf(t *testing.T, commonName, dnsName string, issuer *testCA) *testLeaf {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if dnsName != "" {
		template.DNSNames = []string{dnsName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.certificate, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testLeaf{certificate: certificate, key: key, der: der}
}

func (l *testLeaf) tlsCertificate(chain ...*testCA) tls.Certificate {
	encoded := [][]byte{l.der}
	for _, authority := range chain {
		encoded = append(encoded, authority.certificate.Raw)
	}
	return tls.Certificate{
		Certificate: encoded,
		PrivateKey:  l.key,
		Leaf:        l.certificate,
	}
}

func TestParseCertificateAuthorities(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)

	names := Authorities{root.certificate.RawSubject, intermediate.certificate.RawSubject}
	encoded, err := MarshalCertificateAuthorities(names)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCertificateAuthorities(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != len(names) {
		t.Fatalf("got %d authorities, want %d", len(parsed), len(names))
	}
	for i := range names {
		if !bytes.Equal(parsed[i], names[i]) {
			t.Fatalf("authority %d differs", i)
		}
	}

	empty, err := ParseCertificateAuthorities([]byte{0, 0})
	if err != nil {
		t.Fatalf("empty extension should parse: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty extension = %#v, want non-nil empty list", empty)
	}

	fromTLV, err := ParseCertificateAuthoritiesExtension(wrapExtension(ExtensionCertificateAuthorities, encoded))
	if err != nil {
		t.Fatalf("complete extension: %v", err)
	}
	if len(fromTLV) != len(names) {
		t.Fatalf("complete extension returned %d authorities", len(fromTLV))
	}
}

func TestParseCertificateAuthoritiesRejectsMalformedData(t *testing.T) {
	root := newTestCA(t, "Root", nil)
	valid, err := MarshalCertificateAuthorities(Authorities{root.certificate.RawSubject})
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"short payload":        {},
		"wrong total length":   append(valid, 0),
		"truncated name":       {0, 4, 0, 8, 0x30},
		"zero-length name":     {0, 2, 0, 0},
		"not a DER sequence":   {0, 2, 0, 1, 0x04},
		"trailing DER content": append(append(append([]byte{}, valid...), 0), 0),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCertificateAuthorities(data); !errors.Is(err, ErrMalformedCertificateAuthorities) {
				t.Fatalf("error = %v, want ErrMalformedCertificateAuthorities", err)
			}
		})
	}
}

func TestIssuerFiltering(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)
	rogueRoot := newTestCA(t, "Rogue Root", nil)
	allowedLeaf := newTestLeaf(t, "allowed.example", "allowed.example", intermediate)
	rogueLeaf := newTestLeaf(t, "rogue.example", "rogue.example", rogueRoot)

	authorities := Authorities{intermediate.certificate.RawSubject, root.certificate.RawSubject}
	if err := ValidateCertificateAuthorities(allowedLeaf.certificate, authorities); err != nil {
		t.Fatalf("allowed leaf rejected: %v", err)
	}
	if err := ValidateCertificateAuthorities(rogueLeaf.certificate, authorities); !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("rogue leaf error = %v, want ErrIssuerNotAuthorized", err)
	}
	if err := ValidateCertificateAuthorities(allowedLeaf.certificate, nil); err != nil {
		t.Fatalf("absent extension should not filter: %v", err)
	}
	if err := ValidateCertificateAuthorities(allowedLeaf.certificate, Authorities{}); !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("empty extension error = %v, want ErrIssuerNotAuthorized", err)
	}
	if err := ValidateCertificateAuthorities(allowedLeaf.certificate, Extension{Present: true}); !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("present-but-empty extension error = %v, want ErrIssuerNotAuthorized", err)
	}
	if err := ValidateCertificateAuthorities(allowedLeaf.certificate, Authorities{root.certificate.RawSubject}); !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("a matching root in the chain must not authorize the leaf: %v", err)
	}

	if !IssuerAuthorized(intermediate.certificate.RawSubject, authorities) {
		t.Fatal("intermediate issuer should be authorized")
	}
	if IssuerAuthorized(rogueRoot.certificate.RawSubject, authorities) {
		t.Fatal("rogue issuer should not be authorized")
	}
}

func TestVerifyChain(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)
	rogueRoot := newTestCA(t, "Rogue Root", nil)
	leaf := newTestLeaf(t, "server.example", "server.example", intermediate)
	rogueLeaf := newTestLeaf(t, "rogue.example", "rogue.example", rogueRoot)
	chain := []*x509.Certificate{leaf.certificate, intermediate.certificate, root.certificate}
	rogueChain := []*x509.Certificate{rogueLeaf.certificate, rogueRoot.certificate}

	roots := x509.NewCertPool()
	roots.AddCert(root.certificate)
	options := x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "server.example",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	chains, err := VerifyChain(chain, Authorities{intermediate.certificate.RawSubject}, options)
	if err != nil {
		t.Fatalf("matching issuer should verify: %v", err)
	}
	if len(chains) == 0 {
		t.Fatal("expected at least one verified chain")
	}

	_, err = VerifyChain(chain, Authorities{rogueRoot.certificate.RawSubject}, options)
	if !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("mismatched issuer error = %v, want ErrIssuerNotAuthorized", err)
	}

	rogueRoots := x509.NewCertPool()
	rogueRoots.AddCert(rogueRoot.certificate)
	_, err = VerifyChain(rogueChain, Authorities{intermediate.certificate.RawSubject}, x509.VerifyOptions{
		Roots:     rogueRoots,
		DNSName:   "rogue.example",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("issuer filtering should happen before path validation: %v", err)
	}

	// The issuer matches the list, but the chain is not anchored in roots. The
	// certificate must still fail normal path validation.
	_, err = VerifyChain(chain, Authorities{intermediate.certificate.RawSubject}, x509.VerifyOptions{
		Roots:     rogueRoots,
		DNSName:   "server.example",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil || errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("matching issuer with untrusted chain error = %v, want path validation error", err)
	}
}

func TestFilterCertificates(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)
	rogueRoot := newTestCA(t, "Rogue Root", nil)
	allowedLeaf := newTestLeaf(t, "allowed.example", "allowed.example", intermediate)
	rogueLeaf := newTestLeaf(t, "rogue.example", "rogue.example", rogueRoot)

	certificates := []tls.Certificate{
		allowedLeaf.tlsCertificate(intermediate, root),
		rogueLeaf.tlsCertificate(rogueRoot),
	}
	filtered, err := FilterCertificates(certificates, Authorities{intermediate.certificate.RawSubject})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Leaf != allowedLeaf.certificate {
		t.Fatalf("filtered %d certificates, want only the allowed leaf", len(filtered))
	}

	if _, err := FilterCertificates(certificates, Authorities{}); !errors.Is(err, ErrNoUsableCertificate) {
		t.Fatalf("empty list error = %v, want ErrNoUsableCertificate", err)
	}
}

func TestParseClientHello(t *testing.T) {
	intermediate := newTestCA(t, "Allowed Intermediate", nil)
	helloBytes := buildClientHello(t, "server.example", Authorities{intermediate.certificate.RawSubject}, true)
	hello, err := ParseClientHello(helloBytes)
	if err != nil {
		t.Fatal(err)
	}
	if hello.ServerName != "server.example" {
		t.Fatalf("ServerName = %q", hello.ServerName)
	}
	if !hello.Authorities.Present {
		t.Fatal("certificate_authorities should be present")
	}
	if len(hello.Authorities.Authorities) != 1 ||
		!bytes.Equal(hello.Authorities.Authorities[0], intermediate.certificate.RawSubject) {
		t.Fatalf("unexpected authorities: %#v", hello.Authorities)
	}

	withoutAuthorities := buildClientHello(t, "server.example", nil, false)
	hello, err = ParseClientHello(withoutAuthorities)
	if err != nil {
		t.Fatal(err)
	}
	if hello.Authorities.Present {
		t.Fatal("certificate_authorities should be absent")
	}

	// The same bytes wrapped in TLS handshake records must parse identically.
	record := wrapTLSRecord(helloBytes)
	if _, err := ParseClientHello(record); err != nil {
		t.Fatalf("record-wrapped ClientHello: %v", err)
	}
}

func TestParseCertificateRequestCertificateAuthorities(t *testing.T) {
	intermediate := newTestCA(t, "Allowed Intermediate", nil)
	payload, err := MarshalCertificateAuthorities(Authorities{intermediate.certificate.RawSubject})
	if err != nil {
		t.Fatal(err)
	}

	tls13Extensions := wrapExtension(ExtensionCertificateAuthorities, payload)
	tls13Body := append([]byte{0}, byte(len(tls13Extensions)>>8), byte(len(tls13Extensions)))
	tls13Body = append(tls13Body, tls13Extensions...)
	tls13Message := wrapHandshake(tlsHandshakeCertificateRequest, tls13Body)
	extension, err := ParseCertificateRequestCertificateAuthorities(tls13Message)
	if err != nil {
		t.Fatal(err)
	}
	if !extension.Present || !extension.Authorities.Contains(intermediate.certificate.RawSubject) {
		t.Fatalf("unexpected TLS 1.3 extension: %#v", extension)
	}

	tls12Body := []byte{1, 1, 0, 2, 0x04, 0x03}
	tls12Body = append(tls12Body, payload...)
	tls12Message := wrapHandshake(tlsHandshakeCertificateRequest, tls12Body)
	extension, err = ParseCertificateRequestCertificateAuthorities(tls12Message)
	if err != nil {
		t.Fatal(err)
	}
	if !extension.Present || !extension.Authorities.Contains(intermediate.certificate.RawSubject) {
		t.Fatalf("unexpected TLS 1.2 field: %#v", extension)
	}
}

type fakeHelloConn struct {
	hello []byte
}

func (c *fakeHelloConn) Read([]byte) (int, error)         { return 0, nil }
func (c *fakeHelloConn) Write([]byte) (int, error)        { return 0, nil }
func (c *fakeHelloConn) Close() error                     { return nil }
func (c *fakeHelloConn) LocalAddr() net.Addr              { return fakeAddr("local") }
func (c *fakeHelloConn) RemoteAddr() net.Addr             { return fakeAddr("remote") }
func (c *fakeHelloConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeHelloConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeHelloConn) SetWriteDeadline(time.Time) error { return nil }
func (c *fakeHelloConn) ClientHelloBytes() []byte         { return c.hello }

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }
func (a fakeAddr) String() string  { return string(a) }

func TestWrapServerConfig(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)
	rogueRoot := newTestCA(t, "Rogue Root", nil)
	allowedLeaf := newTestLeaf(t, "allowed.example", "allowed.example", intermediate)
	rogueLeaf := newTestLeaf(t, "rogue.example", "rogue.example", rogueRoot)

	authorities := Authorities{intermediate.certificate.RawSubject}
	helloBytes := buildClientHello(t, "allowed.example", authorities, true)
	hello := &tls.ClientHelloInfo{
		ServerName: "allowed.example",
		Extensions: []uint16{0, ExtensionCertificateAuthorities},
		Conn:       &fakeHelloConn{hello: helloBytes},
	}

	rogueConfig := WrapServerConfig(&tls.Config{Certificates: []tls.Certificate{rogueLeaf.tlsCertificate(rogueRoot)}})
	if _, err := rogueConfig.GetCertificate(hello); !errors.Is(err, ErrIssuerNotAuthorized) {
		t.Fatalf("out-of-list issuer error = %v, want ErrIssuerNotAuthorized", err)
	}

	mixedConfig := WrapServerConfig(&tls.Config{Certificates: []tls.Certificate{
		rogueLeaf.tlsCertificate(rogueRoot),
		allowedLeaf.tlsCertificate(intermediate, root),
	}})
	selected, err := mixedConfig.GetCertificate(hello)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Leaf != allowedLeaf.certificate {
		t.Fatal("selected an out-of-list certificate")
	}

	noExtensionHello := &tls.ClientHelloInfo{Conn: &fakeHelloConn{}}
	selected, err = mixedConfig.GetCertificate(noExtensionHello)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Leaf != rogueLeaf.certificate {
		t.Fatal("absent extension should preserve the normal first-certificate fallback")
	}

	advertisedWithoutCapture := &tls.ClientHelloInfo{
		Extensions: []uint16{ExtensionCertificateAuthorities},
		Conn:       &fakeHelloConn{},
	}
	if _, err := mixedConfig.GetCertificate(advertisedWithoutCapture); !errors.Is(err, ErrAuthoritiesUnavailable) {
		t.Fatalf("missing captured extension error = %v, want ErrAuthoritiesUnavailable", err)
	}
}

func TestWrapClientConfig(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)
	rogueRoot := newTestCA(t, "Rogue Root", nil)
	allowedLeaf := newTestLeaf(t, "client.example", "", intermediate)
	rogueLeaf := newTestLeaf(t, "rogue-client.example", "", rogueRoot)

	config := WrapClientConfig(&tls.Config{Certificates: []tls.Certificate{
		rogueLeaf.tlsCertificate(rogueRoot),
		allowedLeaf.tlsCertificate(intermediate, root),
	}})
	request := &tls.CertificateRequestInfo{
		AcceptableCAs: [][]byte{intermediate.certificate.RawSubject},
	}
	selected, err := config.GetClientCertificate(request)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Leaf != allowedLeaf.certificate {
		t.Fatal("selected an out-of-list client certificate")
	}

	request.AcceptableCAs = [][]byte{root.certificate.RawSubject}
	if _, err := config.GetClientCertificate(request); !errors.Is(err, ErrNoUsableCertificate) {
		t.Fatalf("out-of-list client issuer error = %v, want ErrNoUsableCertificate", err)
	}
}

func TestCompatibilityEntrypoints(t *testing.T) {
	root := newTestCA(t, "Allowed Root", nil)
	intermediate := newTestCA(t, "Allowed Intermediate", root)
	leaf := newTestLeaf(t, "server.example", "server.example", intermediate)
	chain := []*x509.Certificate{leaf.certificate, intermediate.certificate, root.certificate}
	authorities := Authorities{intermediate.certificate.RawSubject}

	roots := x509.NewCertPool()
	roots.AddCert(root.certificate)

	if err := ValidateCertificateAuthorities(authorities, chain, roots); err != nil {
		t.Fatalf("reordered validate arguments: %v", err)
	}
	if err := VerifyCertificateChain(chain, roots, authorities, "server.example", x509.ExtKeyUsageServerAuth); err != nil {
		t.Fatalf("reordered verify arguments: %v", err)
	}

	certificates := []tls.Certificate{leaf.tlsCertificate(intermediate, root)}
	filtered, err := FilterCertificateAuthorities(authorities, certificates)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered %d certificates", len(filtered))
	}
}

func buildClientHello(t *testing.T, serverName string, authorities Authorities, includeAuthorities bool) []byte {
	t.Helper()
	body := make([]byte, 0, 128)
	body = append(body, 0x03, 0x03)
	body = append(body, make([]byte, 32)...)
	body = append(body, 0)                // session ID
	body = append(body, 0, 2, 0x13, 0x01) // one TLS 1.3 cipher suite
	body = append(body, 1, 0)             // null compression method

	var extensions []byte
	if serverName != "" {
		name := []byte(serverName)
		sni := []byte{0, byte(len(name) + 3), 0, 0, byte(len(name))}
		sni = append(sni, name...)
		extensions = append(extensions, wrapExtension(0, sni)...)
	}
	if includeAuthorities {
		payload, err := MarshalCertificateAuthorities(authorities)
		if err != nil {
			t.Fatal(err)
		}
		extensions = append(extensions, wrapExtension(ExtensionCertificateAuthorities, payload)...)
	}

	var extensionLength [2]byte
	binary.BigEndian.PutUint16(extensionLength[:], uint16(len(extensions)))
	body = append(body, extensionLength[:]...)
	body = append(body, extensions...)
	return wrapHandshake(tlsHandshakeClientHello, body)
}

func wrapExtension(extensionType uint16, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint16(out[0:2], extensionType)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(payload)))
	copy(out[4:], payload)
	return out
}

func wrapHandshake(messageType byte, body []byte) []byte {
	out := make([]byte, 4+len(body))
	out[0] = messageType
	out[1] = byte(len(body) >> 16)
	out[2] = byte(len(body) >> 8)
	out[3] = byte(len(body))
	copy(out[4:], body)
	return out
}

func wrapTLSRecord(handshake []byte) []byte {
	out := make([]byte, 5+len(handshake))
	out[0] = tlsRecordTypeHandshake
	out[1] = 0x03
	out[2] = 0x01
	binary.BigEndian.PutUint16(out[3:5], uint16(len(handshake)))
	copy(out[5:], handshake)
	return out
}
