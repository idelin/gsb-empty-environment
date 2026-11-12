package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestNewTLSALPN01ConfigUsesReverseName(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	config, err := NewTLSALPN01Config(id, "token.thumbprint")
	if err != nil {
		t.Fatalf("NewTLSALPN01Config: %v", err)
	}
	if config.ServerName != "8.8.8.8.in-addr.arpa" {
		t.Fatalf("ServerName = %q", config.ServerName)
	}
	if len(config.NextProtos) != 1 || config.NextProtos[0] != TLSALPN01Protocol {
		t.Fatalf("NextProtos = %q", config.NextProtos)
	}
	if config.VerifyConnection == nil {
		t.Fatal("VerifyConnection is nil")
	}
}

func TestVerifyTLSALPN01Certificate(t *testing.T) {
	t.Parallel()
	addr := netip.MustParseAddr("8.8.8.8")
	const keyAuth = "token.thumbprint"

	cert, _ := makeChallengeCertificate(t, addr, keyAuth, true, true, false)
	if err := VerifyTLSALPN01Certificate(cert, addr, keyAuth); err != nil {
		t.Fatalf("valid certificate: %v", err)
	}
	if err := VerifyTLSALPN01Certificate(cert, addr, "wrong"); !errors.Is(err, ErrTLSALPN01KeyAuthorization) {
		t.Fatalf("wrong key authorization: got %v", err)
	}

	cert, _ = makeChallengeCertificate(t, addr, keyAuth, true, false, false)
	if err := VerifyTLSALPN01Certificate(cert, addr, keyAuth); !errors.Is(err, ErrTLSALPN01Certificate) {
		t.Fatalf("missing extension: got %v", err)
	}

	cert, _ = makeChallengeCertificate(t, addr, keyAuth, false, true, false)
	if err := VerifyTLSALPN01Certificate(cert, addr, keyAuth); !errors.Is(err, ErrTLSALPN01Certificate) {
		t.Fatalf("non-critical extension: got %v", err)
	}

	cert, _ = makeChallengeCertificate(t, netip.MustParseAddr("1.1.1.1"), keyAuth, true, true, false)
	if err := VerifyTLSALPN01Certificate(cert, addr, keyAuth); !errors.Is(err, ErrTLSALPN01Certificate) {
		t.Fatalf("wrong SAN: got %v", err)
	}

	cert, _ = makeChallengeCertificate(t, addr, keyAuth, true, true, true)
	if err := VerifyTLSALPN01Certificate(cert, addr, keyAuth); !errors.Is(err, ErrTLSALPN01Certificate) {
		t.Fatalf("extra DNS SAN: got %v", err)
	}
}

func TestTLSALPN01ValidatorFullHandshake(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	const keyAuth = "token.thumbprint"
	_, tlsCert := makeChallengeCertificate(t, netip.MustParseAddr("8.8.8.8"), keyAuth, true, true, false)

	sni := make(chan string, 1)
	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{TLSALPN01Protocol},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sni <- hello.ServerName
			return nil, nil
		},
	}
	serverConn, clientConn := net.Pipe()
	serverErr := make(chan error, 1)
	go func() {
		conn := tls.Server(serverConn, serverConfig)
		err := conn.Handshake()
		_ = conn.Close()
		serverErr <- err
	}()

	validator := &TLSALPN01Validator{
		Timeout: 5 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "8.8.8.8:443" {
				t.Errorf("dial address = %q, want 8.8.8.8:443", address)
			}
			return clientConn, nil
		},
	}
	if err := validator.Validate(context.Background(), id, keyAuth); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
	if got := <-sni; got != "8.8.8.8.in-addr.arpa" {
		t.Fatalf("SNI = %q, want 8.8.8.8.in-addr.arpa", got)
	}
}

func TestTLSALPN01ValidatorRequiresProtocol(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	const keyAuth = "token.thumbprint"
	_, tlsCert := makeChallengeCertificate(t, netip.MustParseAddr("8.8.8.8"), keyAuth, true, true, false)

	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}
	serverConn, clientConn := net.Pipe()
	serverErr := make(chan error, 1)
	go func() {
		conn := tls.Server(serverConn, serverConfig)
		err := conn.Handshake()
		_ = conn.Close()
		serverErr <- err
	}()

	validator := &TLSALPN01Validator{
		Timeout: 5 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return clientConn, nil
		},
	}
	err := validator.Validate(context.Background(), id, keyAuth)
	if !errors.Is(err, ErrTLSALPN01Protocol) {
		t.Fatalf("got %v, want ErrTLSALPN01Protocol", err)
	}
	_ = <-serverErr
}

func makeChallengeCertificate(t *testing.T, addr netip.Addr, keyAuth string, critical, includeACME, extraDNS bool) (*x509.Certificate, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: addr.String()},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IP(addr.AsSlice())},
	}
	if extraDNS {
		template.DNSNames = []string{"example.com"}
	}
	if includeACME {
		digest := sha256.Sum256([]byte(keyAuth))
		value, err := asn1.Marshal(digest[:])
		if err != nil {
			t.Fatalf("asn1.Marshal: %v", err)
		}
		template.ExtraExtensions = []pkix.Extension{{
			Id:       acmeIdentifierOID,
			Critical: critical,
			Value:    value,
		}}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
