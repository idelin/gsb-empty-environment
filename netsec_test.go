package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

// makeCert builds a self-signed certificate from the given template.
func makeCert(t *testing.T, tmpl *x509.Certificate) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func baseTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
}

func TestVerifyHostnameEmptySubjectAndEmptySAN(t *testing.T) {
	cert := makeCert(t, baseTemplate())

	for _, host := range []string{"example.com", "www.example.com", "", "*"} {
		err := VerifyHostname(cert, host)
		if !errors.Is(err, ErrEmptyIdentity) {
			t.Errorf("host %q: got %v, want ErrEmptyIdentity", host, err)
		}
	}
}

func TestVerifyHostnameEmptySubjectWithMatchingSAN(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.DNSNames = []string{"example.com"}
	cert := makeCert(t, tmpl)

	if err := VerifyHostname(cert, "example.com"); err != nil {
		t.Errorf("expected match, got %v", err)
	}
	if err := VerifyHostname(cert, "other.example.com"); err == nil {
		t.Error("expected mismatch for other.example.com")
	}
}

func TestVerifyHostnameEmptySubjectWithWildcardSAN(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.DNSNames = []string{"*.example.com"}
	cert := makeCert(t, tmpl)

	if err := VerifyHostname(cert, "www.example.com"); err != nil {
		t.Errorf("expected wildcard match, got %v", err)
	}
}

func TestVerifyHostnameNonEmptySubjectNoSAN(t *testing.T) {
	// Not a double-empty certificate, so the wrapper defers to the standard
	// library, which rejects legacy CN-only certificates.
	tmpl := baseTemplate()
	tmpl.Subject = pkix.Name{CommonName: "legacy.example.com"}
	cert := makeCert(t, tmpl)

	err := VerifyHostname(cert, "legacy.example.com")
	if err == nil {
		t.Error("expected error for CN-only certificate")
	}
	if errors.Is(err, ErrEmptyIdentity) {
		t.Errorf("got %v, want a non-ErrEmptyIdentity error", err)
	}
}

func TestVerifyHostnameSubjectAndMatchingSAN(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.Subject = pkix.Name{CommonName: "legacy.example.com"}
	tmpl.DNSNames = []string{"example.com"}
	cert := makeCert(t, tmpl)

	if err := VerifyHostname(cert, "example.com"); err != nil {
		t.Errorf("expected match, got %v", err)
	}
}

func TestVerifyHostnameNilCertificate(t *testing.T) {
	if err := VerifyHostname(nil, "example.com"); err == nil {
		t.Error("expected error for nil certificate")
	}
}
