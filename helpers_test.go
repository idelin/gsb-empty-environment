package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	return n
}

func pkixCN(cn string) pkix.Name {
	return pkix.Name{CommonName: cn}
}

// selfSignedLeaf builds and parses a self-signed leaf certificate from a
// template. The template's key usage and validity defaults are filled in.
func selfSignedLeaf(t *testing.T, tmpl *x509.Certificate) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if tmpl.NotBefore.IsZero() {
		tmpl.NotBefore = time.Now().Add(-time.Hour)
	}
	if tmpl.NotAfter.IsZero() {
		tmpl.NotAfter = time.Now().Add(time.Hour)
	}
	if tmpl.KeyUsage == 0 {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
	}
	tmpl.BasicConstraintsValid = true

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return leaf
}
