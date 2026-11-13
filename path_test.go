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

var testNow = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

func makeKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func makeCert(t *testing.T, tmpl *x509.Certificate, parent *x509.Certificate, pub, signer any) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatalf("create certificate %q: %v", tmpl.Subject.CommonName, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %q: %v", tmpl.Subject.CommonName, err)
	}
	return cert
}

func baseTemplate(cn string, serial int64) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             testNow.Add(-time.Hour),
		NotAfter:              testNow.Add(24 * time.Hour),
		BasicConstraintsValid: true,
	}
}

func makeRoot(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := makeKey(t)
	tmpl := baseTemplate("test root", 1)
	tmpl.IsCA = true
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	return makeCert(t, tmpl, tmpl, &key.PublicKey, key), key
}

// makeIntermediate issues an intermediate from the root. ca controls the
// basicConstraints cA bit of the intermediate.
func makeIntermediate(t *testing.T, root *x509.Certificate, rootKey *ecdsa.PrivateKey, ca bool) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := makeKey(t)
	tmpl := baseTemplate("test intermediate", 2)
	tmpl.IsCA = ca
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	return makeCert(t, tmpl, root, &key.PublicKey, rootKey), key
}

// makeLeaf issues an end-entity certificate. ca controls whether the leaf
// itself claims to be a CA that can keep issuing.
func makeLeaf(t *testing.T, issuer *x509.Certificate, issuerKey *ecdsa.PrivateKey, ca bool, usages []x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	key := makeKey(t)
	tmpl := baseTemplate("www.example.org", 3)
	tmpl.DNSNames = []string{"www.example.org"}
	tmpl.IsCA = ca
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	if ca {
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	}
	tmpl.ExtKeyUsage = usages
	return makeCert(t, tmpl, issuer, &key.PublicKey, issuerKey)
}

func rootPool(root *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return pool
}

// 末端 cA 为假且用途符合时放行。
func TestValidatePathValidChainAccepted(t *testing.T) {
	root, rootKey := makeRoot(t)
	inter, interKey := makeIntermediate(t, root, rootKey, true)
	leaf := makeLeaf(t, inter, interKey, false, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})

	chains, err := ValidatePath(leaf, []*x509.Certificate{inter}, PathOptions{
		Roots:       rootPool(root),
		DNSName:     "www.example.org",
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: testNow,
	})
	if err != nil {
		t.Fatalf("ValidatePath rejected a valid chain: %v", err)
	}
	if len(chains) == 0 {
		t.Fatal("ValidatePath returned no chains")
	}
}

// 把中间证书的 cA 改成假：非 CA 证书出现在路径中间，必须失败。
func TestValidatePathIntermediateWithCAFalseRejected(t *testing.T) {
	root, rootKey := makeRoot(t)
	inter, interKey := makeIntermediate(t, root, rootKey, false)
	leaf := makeLeaf(t, inter, interKey, false, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})

	_, err := ValidatePath(leaf, []*x509.Certificate{inter}, PathOptions{
		Roots:       rootPool(root),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: testNow,
	})
	if !errors.Is(err, ErrIntermediateNotCA) {
		t.Fatalf("expected ErrIntermediateNotCA, got %v", err)
	}
}

// 末端证书 cA 为真且还能继续签发时失败。
func TestValidatePathLeafWithCATrueRejected(t *testing.T) {
	root, rootKey := makeRoot(t)
	inter, interKey := makeIntermediate(t, root, rootKey, true)
	leaf := makeLeaf(t, inter, interKey, true, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})

	_, err := ValidatePath(leaf, []*x509.Certificate{inter}, PathOptions{
		Roots:       rootPool(root),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: testNow,
	})
	if !errors.Is(err, ErrLeafIsCA) {
		t.Fatalf("expected ErrLeafIsCA, got %v", err)
	}
}

// 末端 cA 为假但用途不符合时失败。
func TestValidatePathLeafWrongUsageRejected(t *testing.T) {
	root, rootKey := makeRoot(t)
	inter, interKey := makeIntermediate(t, root, rootKey, true)
	leaf := makeLeaf(t, inter, interKey, false, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	_, err := ValidatePath(leaf, []*x509.Certificate{inter}, PathOptions{
		Roots:       rootPool(root),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: testNow,
	})
	if err == nil {
		t.Fatal("expected key-usage failure, got nil")
	}
	if errors.Is(err, ErrIntermediateNotCA) || errors.Is(err, ErrLeafIsCA) {
		t.Fatalf("expected x509 usage error, got %v", err)
	}
}
