package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, commonName string, parent *testCA, serial int64) testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	subjectKeyID := make([]byte, 20)
	if _, err := rand.Read(subjectKeyID); err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          subjectKeyID,
	}

	parentCert := template
	parentKey := key
	if parent != nil {
		parentCert = parent.cert
		parentKey = parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parentCert, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{cert: cert, key: key}
}

func newLeaf(t *testing.T, ca testCA, commonName string, serial int64) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func issuingDistributionPointValue(t *testing.T, indirect bool) []byte {
	t.Helper()

	// RFC 5280, 5.2.5. The implicit context-specific tag 4 encodes
	// indirectCRL.
	type issuingDistributionPoint struct {
		IndirectCRL bool `asn1:"optional,tag:4"`
	}
	der, err := asn1.Marshal(issuingDistributionPoint{IndirectCRL: indirect})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func certificateIssuerExtension(t *testing.T, issuerName []byte) pkix.Extension {
	t.Helper()

	// GeneralName directoryName is [4] Name; because Name is a CHOICE, the
	// value is encoded explicitly.
	directoryName := asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        4,
		IsCompound: true,
		Bytes:      issuerName,
	}
	value, err := asn1.Marshal([]asn1.RawValue{directoryName})
	if err != nil {
		t.Fatal(err)
	}
	return pkix.Extension{Id: OIDCertificateIssuer, Value: value}
}

func rfc822IssuerExtension(t *testing.T, address string) pkix.Extension {
	t.Helper()
	raw, err := asn1.Marshal([]asn1.RawValue{{
		Class: asn1.ClassContextSpecific,
		Tag:   1,
		Bytes: []byte(address),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return pkix.Extension{Id: OIDCertificateIssuer, Value: raw}
}

func createTestCRL(t *testing.T, signer testCA, entries []x509.RevocationListEntry, indirect bool) []byte {
	t.Helper()

	template := &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                time.Now().Add(-time.Hour),
		NextUpdate:                time.Now().Add(time.Hour),
		RevokedCertificateEntries: entries,
		ExtraExtensions: []pkix.Extension{{
			Id:       OIDIssuingDistributionPoint,
			Critical: true,
			Value:    issuingDistributionPointValue(t, indirect),
		}},
	}
	der, err := x509.CreateRevocationList(rand.Reader, template, signer.cert, signer.key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func revokedEntry(serial *big.Int, issuer pkix.Extension) x509.RevocationListEntry {
	entry := x509.RevocationListEntry{
		SerialNumber:   serial,
		RevocationTime: time.Now().Add(-time.Minute),
	}
	if issuer.Id != nil {
		entry.ExtraExtensions = []pkix.Extension{issuer}
	}
	return entry
}

func TestIndirectCRLWithIssuerOnEveryEntryCanBeQueried(t *testing.T) {
	crlIssuer := newTestCA(t, "CRL Issuer", nil, 1)
	caA := newTestCA(t, "CA A", &crlIssuer, 2)
	caB := newTestCA(t, "CA B", &crlIssuer, 3)

	// Both certificates use the same serial number. Lookup must disambiguate
	// them using the certificate issuer, not just the serial number.
	leafA := newLeaf(t, caA, "leaf A", 7)
	leafB := newLeaf(t, caB, "leaf B", 7)

	der := createTestCRL(t, crlIssuer, []x509.RevocationListEntry{
		revokedEntry(leafA.SerialNumber, certificateIssuerExtension(t, caA.cert.RawSubject)),
		revokedEntry(leafB.SerialNumber, certificateIssuerExtension(t, caB.cert.RawSubject)),
	}, true)

	rl, err := ParseCRL(der)
	if err != nil {
		t.Fatalf("ParseCRL: %v", err)
	}
	if !rl.Indirect || !rl.IsIndirectCRL {
		t.Fatal("CRL was not recognized as indirect")
	}
	if len(rl.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(rl.Entries))
	}
	for index, want := range [][]byte{caA.cert.RawSubject, caB.cert.RawSubject} {
		if !bytes.Equal(rl.Entries[index].RawIssuer, want) {
			t.Fatalf("entry %d issuer does not match", index)
		}
		if !rl.Entries[index].HasCertificateIssuer {
			t.Fatalf("entry %d does not report certificateIssuer", index)
		}
	}

	revoked, err := rl.IsRevoked(leafA)
	if err != nil {
		t.Fatalf("IsRevoked(leafA): %v", err)
	}
	if !revoked {
		t.Fatal("leafA should be revoked")
	}
	if entry, err := rl.Lookup(leafA); err != nil || entry == nil {
		t.Fatalf("Lookup(leafA): entry=%v err=%v", entry, err)
	} else if !bytes.Equal(entry.RawIssuer, caA.cert.RawSubject) {
		t.Fatal("leafA resolved to the wrong issuer")
	}

	revoked, err = rl.IsRevoked(leafB)
	if err != nil {
		t.Fatalf("IsRevoked(leafB): %v", err)
	}
	if !revoked {
		t.Fatal("leafB should be revoked")
	}
	if entry, err := rl.Lookup(leafB); err != nil || entry == nil {
		t.Fatalf("Lookup(leafB): entry=%v err=%v", entry, err)
	} else if !bytes.Equal(entry.RawIssuer, caB.cert.RawSubject) {
		t.Fatal("leafB resolved to the wrong issuer")
	}

	if err := ValidateIndirectCRL(der); err != nil {
		t.Fatalf("ValidateIndirectCRL(DER): %v", err)
	}
	parsed, err := x509.ParseRevocationList(der)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndirectCRL(parsed); err != nil {
		t.Fatalf("ValidateIndirectCRL(x509): %v", err)
	}
}

func TestMissingCertificateIssuerInvalidatesWholeIndirectCRL(t *testing.T) {
	crlIssuer := newTestCA(t, "CRL Issuer", nil, 1)
	caA := newTestCA(t, "CA A", &crlIssuer, 2)
	caB := newTestCA(t, "CA B", &crlIssuer, 3)
	leafA := newLeaf(t, caA, "leaf A", 7)
	leafB := newLeaf(t, caB, "leaf B", 8)
	leafC := newLeaf(t, caA, "leaf C", 9)

	// The middle entry has no certificateIssuer. It must not inherit either
	// the CRL issuer or CA A from the previous entry.
	der := createTestCRL(t, crlIssuer, []x509.RevocationListEntry{
		revokedEntry(leafA.SerialNumber, certificateIssuerExtension(t, caA.cert.RawSubject)),
		revokedEntry(leafB.SerialNumber, pkix.Extension{}),
		revokedEntry(leafC.SerialNumber, certificateIssuerExtension(t, caA.cert.RawSubject)),
	}, true)

	if _, err := ParseCRL(der); !errors.Is(err, ErrMissingCertificateIssuer) {
		t.Fatalf("ParseCRL error = %v, want ErrMissingCertificateIssuer", err)
	}
	if err := ValidateCRL(der); !errors.Is(err, ErrMissingCertificateIssuer) {
		t.Fatalf("ValidateCRL error = %v, want ErrMissingCertificateIssuer", err)
	}
	if err := ValidateIndirectCRL(der); !errors.Is(err, ErrMissingCertificateIssuer) {
		t.Fatalf("ValidateIndirectCRL error = %v, want ErrMissingCertificateIssuer", err)
	}

	parsed, err := x509.ParseRevocationList(der)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromX509RevocationList(parsed); !errors.Is(err, ErrMissingCertificateIssuer) {
		t.Fatalf("FromX509RevocationList error = %v, want ErrMissingCertificateIssuer", err)
	}
}

func TestCertificateIssuerWithoutDirectoryNameIsUnusable(t *testing.T) {
	crlIssuer := newTestCA(t, "CRL Issuer", nil, 1)
	caA := newTestCA(t, "CA A", &crlIssuer, 2)
	leaf := newLeaf(t, caA, "leaf", 7)

	der := createTestCRL(t, crlIssuer, []x509.RevocationListEntry{
		revokedEntry(leaf.SerialNumber, rfc822IssuerExtension(t, "ca@example.test")),
	}, true)

	if _, err := ParseCRL(der); !errors.Is(err, ErrMissingCertificateIssuer) {
		t.Fatalf("ParseCRL error = %v, want ErrMissingCertificateIssuer", err)
	}
}

func TestDirectCRLUsesCRLIssuer(t *testing.T) {
	crlIssuer := newTestCA(t, "CRL Issuer", nil, 1)
	leaf := newLeaf(t, crlIssuer, "leaf", 7)
	other := newLeaf(t, crlIssuer, "other", 8)

	der := createTestCRL(t, crlIssuer, []x509.RevocationListEntry{
		revokedEntry(leaf.SerialNumber, pkix.Extension{}),
	}, false)

	rl, err := ParseCRL(der)
	if err != nil {
		t.Fatalf("ParseCRL: %v", err)
	}
	if rl.Indirect {
		t.Fatal("direct CRL was recognized as indirect")
	}
	if !bytes.Equal(rl.Entries[0].RawIssuer, crlIssuer.cert.RawSubject) {
		t.Fatal("direct CRL entry has the wrong issuer")
	}
	revoked, err := rl.IsRevoked(leaf)
	if err != nil || !revoked {
		t.Fatalf("leaf: revoked=%v err=%v, want true, nil", revoked, err)
	}
	revoked, err = rl.IsRevoked(other)
	if err != nil || revoked {
		t.Fatalf("other: revoked=%v err=%v, want false, nil", revoked, err)
	}
	if err := ValidateIndirectCRL(der); !errors.Is(err, ErrNotIndirectCRL) {
		t.Fatalf("ValidateIndirectCRL error = %v, want ErrNotIndirectCRL", err)
	}
}
