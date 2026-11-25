package ocsp

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

func testCA(t *testing.T, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func testLeaf(t *testing.T, issuer *x509.Certificate, issuerKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestRoundTripGoodAndRevoked(t *testing.T) {
	issuer, issuerKey := testCA(t, "issuer")
	leaf := testLeaf(t, issuer, issuerKey)
	now := time.Now()

	for _, status := range []CertStatus{Good, Revoked, Unknown} {
		der, err := CreateResponse(CreateInput{
			Issuer: issuer, Responder: issuer, Signer: issuerKey, Leaf: leaf,
			Status: status, ThisUpdate: now, NextUpdate: now.Add(time.Hour),
			RevokedAt: now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("create %v: %v", status, err)
		}
		resp, err := Verify(der, issuer, issuer)
		if err != nil {
			t.Fatalf("verify %v: %v", status, err)
		}
		if resp.Status != status {
			t.Fatalf("status = %v, want %v", resp.Status, status)
		}
		if resp.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
			t.Fatal("serial mismatch")
		}
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	issuer, issuerKey := testCA(t, "issuer")
	leaf := testLeaf(t, issuer, issuerKey)
	now := time.Now()
	der, err := CreateResponse(CreateInput{
		Issuer: issuer, Responder: issuer, Signer: issuerKey, Leaf: leaf,
		Status: Good, ThisUpdate: now, NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte in the signed body region; signature verification must fail.
	tampered := make([]byte, len(der))
	copy(tampered, der)
	tampered[len(tampered)-3] ^= 0xFF
	if _, err := Verify(tampered, issuer, issuer); err == nil {
		t.Fatal("tampered response must be rejected")
	}
}

func TestWrongResponderRejected(t *testing.T) {
	issuer, issuerKey := testCA(t, "issuer")
	other, otherKey := testCA(t, "other")
	leaf := testLeaf(t, issuer, issuerKey)
	now := time.Now()
	der, err := CreateResponse(CreateInput{
		Issuer: issuer, Responder: other, Signer: otherKey, Leaf: leaf,
		Status: Good, ThisUpdate: now, NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(der, issuer, issuer); err == nil {
		t.Fatal("response signed by unrelated responder must be rejected")
	}
}

func TestMalformedResponseRejected(t *testing.T) {
	if _, _, err := ParseResponse([]byte{0x30, 0x03, 0x02, 0x01, 0x09}); err == nil {
		t.Fatal("malformed/unexpected status data should error")
	}
	if _, _, err := ParseResponse([]byte{0x00}); err != ErrMalformedResponse {
		t.Fatalf("expected ErrMalformedResponse, got %v", err)
	}
}
