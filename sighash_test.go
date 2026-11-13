package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
}

func newTestCA(t *testing.T, name string, parent *testCA, sigAlg x509.SignatureAlgorithm) *testCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key for %s: %v", name, err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		SignatureAlgorithm:    sigAlg,
	}
	issuer := tmpl
	var issuerKey *rsa.PrivateKey = key
	if parent != nil {
		issuer = parent.cert
		issuerKey = parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("create certificate %s: %v", name, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %s: %v", name, err)
	}
	return &testCA{cert: cert, key: key}
}

func newTestLeaf(t *testing.T, parent *testCA, sigAlg x509.SignatureAlgorithm) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:       big.NewInt(time.Now().UnixNano()),
		Subject:            pkix.Name{CommonName: "leaf"},
		NotBefore:          time.Now().Add(-time.Hour),
		NotAfter:           time.Now().Add(time.Hour),
		KeyUsage:           x509.KeyUsageDigitalSignature,
		SignatureAlgorithm: sigAlg,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, &key.PublicKey, parent.key)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return cert
}

// All SHA-256 signatures in the path: accepted.
func TestVerifyPathSignaturesAllSHA256(t *testing.T) {
	root := newTestCA(t, "root", nil, x509.SHA256WithRSA)
	inter := newTestCA(t, "intermediate", root, x509.SHA256WithRSA)
	leaf := newTestLeaf(t, inter, x509.SHA256WithRSA)

	if err := VerifyPathSignatures([]*x509.Certificate{leaf, inter.cert, root.cert}); err != nil {
		t.Fatalf("all-SHA-256 path rejected: %v", err)
	}
}

// Only the intermediate is switched to SHA-1; the leaf stays SHA-256.
// The whole path must be rejected.
func TestVerifyPathSignaturesSHA1Intermediate(t *testing.T) {
	root := newTestCA(t, "root", nil, x509.SHA256WithRSA)
	inter := newTestCA(t, "intermediate", root, x509.SHA1WithRSA)
	leaf := newTestLeaf(t, inter, x509.SHA256WithRSA)

	err := VerifyPathSignatures([]*x509.Certificate{leaf, inter.cert, root.cert})
	if err == nil {
		t.Fatal("path with SHA-1 intermediate accepted")
	}
	var weak *WeakSignatureError
	if !errors.As(err, &weak) {
		t.Fatalf("expected WeakSignatureError, got %T: %v", err, err)
	}
	if weak.Index != 1 || weak.Algorithm != x509.SHA1WithRSA {
		t.Fatalf("unexpected error details: %+v", weak)
	}
}

// MD5 anywhere in the path is rejected as well.
func TestVerifyPathSignaturesMD5Leaf(t *testing.T) {
	root := newTestCA(t, "root", nil, x509.SHA256WithRSA)
	inter := newTestCA(t, "intermediate", root, x509.SHA256WithRSA)
	// Go refuses to sign with MD5, so relabel a valid leaf's algorithm.
	leaf := *newTestLeaf(t, inter, x509.SHA256WithRSA)
	leaf.SignatureAlgorithm = x509.MD5WithRSA

	if err := VerifyPathSignatures([]*x509.Certificate{&leaf, inter.cert, root.cert}); err == nil {
		t.Fatal("path with MD5 leaf accepted")
	}
}

func TestVerifyPathSignaturesEmpty(t *testing.T) {
	if err := VerifyPathSignatures(nil); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("expected ErrEmptyPath, got %v", err)
	}
}
