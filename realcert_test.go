package netsec

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

func makeTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func makeTestCert(t *testing.T, template, parent *x509.Certificate, pub any, signer *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	if template.SerialNumber == nil {
		template.SerialNumber = big.NewInt(time.Now().UnixNano())
	}
	if template.NotBefore.IsZero() {
		template.NotBefore = time.Now().Add(-time.Hour)
	}
	if template.NotAfter.IsZero() {
		template.NotAfter = time.Now().Add(time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func makeRealAnyPolicyChain(t *testing.T, inhibit int, inhibitZero bool) []*Certificate {
	t.Helper()
	rootKey := makeTestKey(t)
	caKey := makeTestKey(t)
	leafKey := makeTestKey(t)

	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "root"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		Policies:              []OID{AnyPolicyOID},
	}
	root := makeTestCert(t, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		Policies:              []OID{AnyPolicyOID},
		ExtraExtensions: []pkix.Extension{{
			Id:       asn1.ObjectIdentifier{2, 5, 29, 54},
			Critical: true,
			Value:    mustMarshalInt(t, inhibit),
		}},
	}
	if inhibitZero {
		caTemplate.InhibitAnyPolicyZero = true
	}
	ca := makeTestCert(t, caTemplate, root, &caKey.PublicKey, rootKey)

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "leaf"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		Policies:     []OID{AnyPolicyOID},
	}
	leaf := makeTestCert(t, leafTemplate, ca, &leafKey.PublicKey, caKey)

	if ca.InhibitAnyPolicy != inhibit || ca.InhibitAnyPolicyZero != inhibitZero {
		t.Fatalf("parsed inhibitAnyPolicy = %d/%v, want %d/%v",
			ca.InhibitAnyPolicy, ca.InhibitAnyPolicyZero, inhibit, inhibitZero)
	}
	if len(ca.Policies) != 1 || !ca.Policies[0].Equal(AnyPolicyOID) {
		t.Fatalf("parsed CA policies = %v, want anyPolicy", ca.Policies)
	}
	if len(leaf.Policies) != 1 || !leaf.Policies[0].Equal(AnyPolicyOID) {
		t.Fatalf("parsed leaf policies = %v, want anyPolicy", leaf.Policies)
	}

	return []*Certificate{leaf, ca, root}
}

func mustMarshalInt(t *testing.T, value int) []byte {
	t.Helper()
	b, err := asn1.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRealCertificateAnyPolicyAfterInhibitDepth(t *testing.T) {
	oid1 := testPolicyOID(1)
	opts := VerifyOptions{
		CertificatePolicies:   []OID{oid1},
		RequireExplicitPolicy: true,
	}

	inhibitedChain := makeRealAnyPolicyChain(t, 0, true)
	if PoliciesValid(inhibitedChain, opts) {
		t.Fatal("real chain validated an anyPolicy leaf after inhibitAnyPolicy reached zero")
	}

	allowedChain := makeRealAnyPolicyChain(t, 1, false)
	if !PoliciesValid(allowedChain, opts) {
		t.Fatal("real chain rejected anyPolicy while one additional certificate was still allowed")
	}

	rootFirst := []*Certificate{inhibitedChain[2], inhibitedChain[1], inhibitedChain[0]}
	if PoliciesValid(rootFirst, opts) {
		t.Fatal("root-first real chain accepted the inhibited anyPolicy leaf")
	}
}
