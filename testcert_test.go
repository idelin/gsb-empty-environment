package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

type testIssuer struct {
	cert   *x509.Certificate
	signer crypto.Signer
}

func (i testIssuer) issuer() Issuer {
	return Issuer{Certificate: i.cert, Signer: i.signer}
}

type testCA struct {
	cert   *x509.Certificate
	signer crypto.Signer
	pool   *x509.CertPool
}

func newTestCA(t *testing.T, algorithm string, notBefore, notAfter time.Time) *testCA {
	t.Helper()
	key, publicKey, signatureAlgorithm := testKey(t, algorithm)
	template := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: "q0490 test root"},
		NotBefore:             notBefore.UTC(),
		NotAfter:              notAfter.UTC(),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SignatureAlgorithm:    signatureAlgorithm,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, key)
	if err != nil {
		t.Fatalf("create root certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse root certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, signer: key, pool: pool}
}

func (ca *testCA) issue(t *testing.T, algorithm string, notBefore, notAfter time.Time) testIssuer {
	t.Helper()
	key, publicKey, signatureAlgorithm := testKey(t, algorithm)
	template := &x509.Certificate{
		SerialNumber:       randomSerial(t),
		Subject:            pkix.Name{CommonName: "q0490 timestamp issuer"},
		NotBefore:          notBefore.UTC(),
		NotAfter:           notAfter.UTC(),
		KeyUsage:           x509.KeyUsageDigitalSignature,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
		SignatureAlgorithm: signatureAlgorithm,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, publicKey, ca.signer)
	if err != nil {
		t.Fatalf("create issuer certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse issuer certificate: %v", err)
	}
	return testIssuer{cert: cert, signer: key}
}

func testKey(t *testing.T, algorithm string) (crypto.Signer, crypto.PublicKey, x509.SignatureAlgorithm) {
	t.Helper()
	switch algorithm {
	case "ecdsa":
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate ECDSA key: %v", err)
		}
		return key, &key.PublicKey, x509.ECDSAWithSHA256
	case "ed25519":
		publicKey, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate Ed25519 key: %v", err)
		}
		return key, publicKey, x509.PureEd25519
	case "rsa":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate RSA key: %v", err)
		}
		return key, &key.PublicKey, x509.SHA256WithRSA
	default:
		t.Fatalf("unknown test key algorithm %q", algorithm)
		return nil, nil, 0
	}
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	return serial
}

func utc(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}
