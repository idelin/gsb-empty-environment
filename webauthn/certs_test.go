package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

type certAuthority struct {
	cert    *x509.Certificate
	der     []byte
	private *ecdsa.PrivateKey
}

func mustGenerateECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	return key
}

func createRootCA(t *testing.T) *certAuthority {
	t.Helper()
	key := mustGenerateECKey(t)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Enterprise FIDO Root", OrganizationalUnit: []string{"Root CA"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	return &certAuthority{cert: cert, der: der, private: key}
}

func createIntermediate(t *testing.T, parent *certAuthority) *certAuthority {
	t.Helper()
	key := mustGenerateECKey(t)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Enterprise FIDO Intermediate", OrganizationalUnit: []string{"Intermediate CA"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.cert, &key.PublicKey, parent.private)
	if err != nil {
		t.Fatalf("create intermediate: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &certAuthority{cert: cert, der: der, private: key}
}

// createAttestationCert builds a leaf certificate carrying the AAGUID
// extension and the required "Authenticator Attestation" OU. The mutate
// callback lets individual tests violate one property at a time.
func createAttestationCert(t *testing.T, parent *certAuthority, aaguid AAGUID,
	leafKey *ecdsa.PrivateKey, mutate func(*x509.Certificate)) *certAuthority {

	t.Helper()
	octetDER, err := asn1.Marshal(aaguid[:])
	if err != nil {
		t.Fatalf("marshal aaguid octet: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject: pkix.Name{
			CommonName:         "Enterprise Security Key",
			OrganizationalUnit: []string{authenticatorAttestationOU},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
		ExtraExtensions: []pkix.Extension{
			{Id: oidFIDOAAGUID, Critical: false, Value: octetDER},
		},
	}
	if mutate != nil {
		mutate(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.cert, &leafKey.PublicKey, parent.private)
	if err != nil {
		t.Fatalf("create attestation cert: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &certAuthority{cert: cert, der: der, private: leafKey}
}
