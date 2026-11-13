package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"testing"
)

func TestCheckExtensionOIDsAcceptsUniqueOIDs(t *testing.T) {
	exts := []pkix.Extension{
		{Id: asn1.ObjectIdentifier{2, 5, 29, 19}},
		{Id: asn1.ObjectIdentifier{2, 5, 29, 17}},
		{Id: OIDExtensionNameConstraints},
	}
	if err := CheckExtensionOIDs(exts); err != nil {
		t.Fatalf("unique extensions rejected: %v", err)
	}
	if got := DuplicateExtensionOIDs(exts); len(got) != 0 {
		t.Fatalf("unexpected duplicates: %v", got)
	}
}

func TestCheckExtensionOIDsRejectsDuplicate(t *testing.T) {
	exts := []pkix.Extension{
		{Id: asn1.ObjectIdentifier{2, 5, 29, 19}},
		{Id: OIDExtensionNameConstraints, Value: q0265NameConstraintsValue("wide.example.com", false)},
		{Id: OIDExtensionNameConstraints, Value: q0265NameConstraintsValue("narrow.example.com", true)},
	}
	err := CheckExtensionOIDs(exts)
	if err == nil {
		t.Fatal("duplicate extension accepted")
	}
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("error does not wrap ErrDuplicateExtension: %v", err)
	}

	var duplicate *DuplicateExtensionError
	if !errors.As(err, &duplicate) {
		t.Fatalf("error is not DuplicateExtensionError: %T", err)
	}
	if !duplicate.OID.Equal(OIDExtensionNameConstraints) {
		t.Fatalf("wrong OID: %v", duplicate.OID)
	}
	if duplicate.Count != 2 {
		t.Fatalf("wrong count: %d", duplicate.Count)
	}

	duplicates := DuplicateExtensionOIDs(exts)
	if len(duplicates) != 1 || !duplicates[0].Equal(OIDExtensionNameConstraints) {
		t.Fatalf("wrong duplicate list: %v", duplicates)
	}
}

func TestCheckExtensionOIDsRejectsThreeOccurrences(t *testing.T) {
	oid := asn1.ObjectIdentifier{2, 5, 29, 17}
	exts := []pkix.Extension{{Id: oid}, {Id: oid}, {Id: oid}}

	var duplicate *DuplicateExtensionError
	err := CheckExtensionOIDs(exts)
	if !errors.As(err, &duplicate) {
		t.Fatalf("expected DuplicateExtensionError, got %v", err)
	}
	if duplicate.Count != 3 {
		t.Fatalf("count = %d, want 3", duplicate.Count)
	}
}

func TestCheckCertificateDetectsDuplicateNameConstraints(t *testing.T) {
	cert := &x509.Certificate{
		Extensions: []pkix.Extension{
			q0265Extension(OIDExtensionNameConstraints, q0265NameConstraintsValue("wide.example.com", false)),
			q0265Extension(OIDExtensionNameConstraints, q0265NameConstraintsValue("narrow.example.com", false)),
		},
	}
	if err := CheckCertificate(cert); err == nil {
		t.Fatal("duplicate name constraints accepted")
	}
	if !HasDuplicateExtensions(cert) {
		t.Fatal("HasDuplicateExtensions reported false")
	}
}

func TestCheckCertificateAcceptsUniqueExtensions(t *testing.T) {
	cert := &x509.Certificate{
		Extensions: []pkix.Extension{
			{Id: asn1.ObjectIdentifier{2, 5, 29, 19}, Value: []byte{0x30, 0x00}},
			{Id: OIDExtensionNameConstraints, Value: q0265NameConstraintsValue("example.com", false)},
		},
	}
	if err := CheckCertificate(cert); err != nil {
		t.Fatalf("unique extensions rejected: %v", err)
	}
}

func TestParseCertificateRejectsDuplicateExtensions(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := q0265RootTemplate()
	template.ExtraExtensions = []pkix.Extension{
		q0265Extension(OIDExtensionNameConstraints, q0265NameConstraintsValue("wide.example.com", false)),
		q0265Extension(OIDExtensionNameConstraints, q0265NameConstraintsValue("narrow.example.com", true)),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ParseCertificate(der)
	if err == nil {
		t.Fatal("ParseCertificate accepted duplicate extensions")
	}
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("ParseCertificate error = %v, want ErrDuplicateExtension", err)
	}
}

func TestParseCertificateAcceptsUniqueExtensions(t *testing.T) {
	root := newQ0265Cert(t, q0265RootTemplate(), nil)
	leaf := newQ0265Cert(t, q0265LeafTemplate("leaf.example.com"), root)

	parsed, err := ParseCertificate(leaf.der)
	if err != nil {
		t.Fatalf("ParseCertificate rejected unique extensions: %v", err)
	}
	if len(parsed.Extensions) == 0 {
		t.Fatal("expected parsed extensions")
	}
}

func TestExtensionOIDsFromDER(t *testing.T) {
	root := newQ0265Cert(t, q0265RootTemplate(), nil)
	oids, err := ExtensionOIDsFromDER(root.der)
	if err != nil {
		t.Fatalf("ExtensionOIDsFromDER: %v", err)
	}
	if len(oids) < 2 {
		t.Fatalf("expected at least two extensions, got %v", oids)
	}
}
