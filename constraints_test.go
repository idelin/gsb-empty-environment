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

var (
	testOID      = asn1.ObjectIdentifier{1, 2, 3, 4}
	testOIDOther = asn1.ObjectIdentifier{1, 2, 3, 5}
)

func TestOIDRoundTrip(t *testing.T) {
	for _, oid := range []asn1.ObjectIdentifier{
		{1, 2, 3, 4},
		{2, 999, 3},
		{1, 2, 840, 113549, 1, 1, 11},
		{0, 0},
	} {
		content, err := marshalOIDContent(oid)
		if err != nil {
			t.Fatalf("marshal %v: %v", oid, err)
		}
		got, err := decodeOIDContent(content)
		if err != nil {
			t.Fatalf("decode %v: %v", oid, err)
		}
		if !got.Equal(oid) {
			t.Errorf("round trip %v -> %v", oid, got)
		}
		if parsed, err := ParseOID(oid.String()); err != nil || !parsed.Equal(oid) {
			t.Errorf("ParseOID(%q) = %v, %v", oid.String(), parsed, err)
		}
	}
}

func TestParseOIDRejectsNonCanonicalValues(t *testing.T) {
	for _, s := range []string{"", "1", "1..2", "1.02.3", "3.1.2", "1.40.2", "1.a.2"} {
		if _, err := ParseOID(s); err == nil {
			t.Errorf("ParseOID(%q) unexpectedly succeeded", s)
		}
	}
}

func TestEncodeAndParseNameConstraints(t *testing.T) {
	ip := []byte{10, 0, 0, 0, 255, 255, 0, 0}
	nc := &NameConstraints{
		Permitted: []GeneralSubtree{
			{Base: RegisteredIDName(testOID)},
			{Base: GeneralName{Type: NameTypeDNS, Value: ".example.com"}},
			{Base: GeneralName{Type: NameTypeEmail, Value: "user@example.com"}},
			{Base: GeneralName{Type: NameTypeURI, Value: "example.com"}},
			{Base: GeneralName{Type: NameTypeIPAddress, IP: ip[:4], Mask: ip[4:]}},
		},
		Excluded: []GeneralSubtree{
			{Base: RegisteredIDName(testOIDOther)},
			{Base: GeneralName{Type: NameTypeDNS, Value: "bad.example.com"}},
		},
	}
	der, err := MarshalNameConstraints(nc)
	if err != nil {
		t.Fatalf("MarshalNameConstraints: %v", err)
	}
	got, err := parseNameConstraints(der, true)
	if err != nil {
		t.Fatalf("parseNameConstraints: %v", err)
	}
	if !got.Critical {
		t.Fatal("critical flag was not retained")
	}
	if len(got.PermittedRegisteredIDs) != 1 || !got.PermittedRegisteredIDs[0].Equal(testOID) {
		t.Errorf("PermittedRegisteredIDs = %v", got.PermittedRegisteredIDs)
	}
	if len(got.ExcludedRegisteredIDs) != 1 || !got.ExcludedRegisteredIDs[0].Equal(testOIDOther) {
		t.Errorf("ExcludedRegisteredIDs = %v", got.ExcludedRegisteredIDs)
	}
	if len(got.PermittedDNSDomains) != 1 || got.PermittedDNSDomains[0] != ".example.com" {
		t.Errorf("PermittedDNSDomains = %v", got.PermittedDNSDomains)
	}
	if len(got.PermittedEmailAddresses) != 1 || got.PermittedEmailAddresses[0] != "user@example.com" {
		t.Errorf("PermittedEmailAddresses = %v", got.PermittedEmailAddresses)
	}
	if len(got.PermittedURIDomains) != 1 || got.PermittedURIDomains[0] != "example.com" {
		t.Errorf("PermittedURIDomains = %v", got.PermittedURIDomains)
	}
	if len(got.PermittedIPRanges) != 1 {
		t.Errorf("PermittedIPRanges = %v", got.PermittedIPRanges)
	}
}

func TestRegisteredIDMatchingIsTypeIsolated(t *testing.T) {
	oid := testOID
	registered := RegisteredIDName(oid)
	if !MatchRegisteredIDConstraint(registered, oid, PolicyStrict) {
		t.Fatal("same registeredID did not match")
	}
	if MatchRegisteredIDConstraint(RegisteredIDName(testOIDOther), oid, PolicyStrict) {
		t.Fatal("different registeredID matched")
	}
	for _, tc := range []struct {
		name GeneralName
		typ  string
	}{
		{DNSName("1.2.3.4"), "DNS"},
		{GeneralName{Type: NameTypeEmail, Value: "1.2.3.4"}, "email"},
		{GeneralName{Type: NameTypeURI, Value: "1.2.3.4"}, "URI"},
		{GeneralName{Type: NameTypeURI, Value: "urn:oid:1.2.3.4"}, "URI"},
	} {
		if MatchRegisteredIDConstraint(tc.name, oid, PolicyStrict) {
			t.Errorf("%s name %q matched a registeredID constraint in strict mode", tc.typ, tc.name.String())
		}
	}
}

func TestLegacyDNSOIDMatching(t *testing.T) {
	oid := testOID
	if !MatchRegisteredIDConstraint(DNSName("1.2.3.4"), oid, PolicyLegacy) {
		t.Fatal("legacy DNS OID did not match the registeredID constraint")
	}
	if MatchRegisteredIDConstraint(DNSName("1.2.3.5"), oid, PolicyLegacy) {
		t.Fatal("a different DNS OID matched")
	}
	if MatchRegisteredIDConstraint(DNSName("1.2.3.4.5"), oid, PolicyLegacy) {
		t.Fatal("a longer DNS OID matched")
	}
	if MatchRegisteredIDConstraint(DNSName("example.com"), oid, PolicyLegacy) {
		t.Fatal("a regular DNS name matched")
	}
	if MatchRegisteredIDConstraint(GeneralName{Type: NameTypeEmail, Value: "1.2.3.4"}, oid, PolicyLegacy) {
		t.Fatal("an email value matched through the legacy DNS path")
	}
	if MatchRegisteredIDConstraint(GeneralName{Type: NameTypeURI, Value: "1.2.3.4"}, oid, PolicyLegacy) {
		t.Fatal("a URI value matched through the legacy DNS path")
	}
}

func TestNameConstraintsCheck(t *testing.T) {
	nc := &NameConstraints{
		Permitted: []GeneralSubtree{{Base: RegisteredIDName(testOID)}},
	}
	if err := nc.Check([]GeneralName{DNSName("1.2.3.4")}, PolicyLegacy); err != nil {
		t.Fatalf("legacy DNS OID was rejected: %v", err)
	}
	if err := nc.Check([]GeneralName{RegisteredIDName(testOID)}, PolicyStrict); err != nil {
		t.Fatalf("same registeredID was rejected: %v", err)
	}
	err := nc.Check([]GeneralName{RegisteredIDName(testOIDOther)}, PolicyStrict)
	if err == nil {
		t.Fatal("different registeredID was accepted")
	}
	var violation *ConstraintViolationError
	if !errorsAs(err, &violation) {
		t.Fatalf("error is %T, want *ConstraintViolationError", err)
	}

	nc.Excluded = []GeneralSubtree{{Base: RegisteredIDName(testOID)}}
	if err := nc.Check([]GeneralName{DNSName("1.2.3.4")}, PolicyLegacy); err == nil {
		t.Fatal("legacy DNS OID was not excluded")
	}
	if err := nc.Check([]GeneralName{DNSName("1.2.3.4")}, PolicyStrict); err != nil {
		t.Fatalf("strict mode treated a DNS name as a registeredID exclusion: %v", err)
	}
}

func TestCertificatePathRegisteredID(t *testing.T) {
	root, rootKey := makeRoot(t, &NameConstraints{
		Permitted: []GeneralSubtree{{Base: RegisteredIDName(testOID)}},
	})

	leafDNS := makeLeaf(t, root, rootKey, []GeneralName{DNSName("1.2.3.4")})
	if err := VerifyChain([]*x509.Certificate{leafDNS, root}, Options{Policy: PolicyLegacy}); err != nil {
		t.Fatalf("legacy DNS/OID path was rejected: %v", err)
	}
	if err := VerifyChain([]*x509.Certificate{leafDNS, root}, Options{Policy: PolicyStrict}); err != nil {
		// Strict mode does not use a DNS name to match a registeredID
		// constraint. Because the leaf has no registeredID name, the
		// permitted subtree is vacuously satisfied rather than cross-matched.
		t.Fatalf("strict path should not cross-match, but should otherwise validate: %v", err)
	}

	leafRegistered := makeLeaf(t, root, rootKey, []GeneralName{RegisteredIDName(testOID)})
	if err := VerifyChain(leafRegistered, root, Options{Policy: PolicyStrict}); err != nil {
		t.Fatalf("matching registeredID path was rejected: %v", err)
	}

	leafWrong := makeLeaf(t, root, rootKey, []GeneralName{RegisteredIDName(testOIDOther)})
	if err := VerifyChain([]*x509.Certificate{leafWrong, root}, Options{Policy: PolicyStrict}); err == nil {
		t.Fatal("registeredID outside the permitted subtree was accepted")
	}

	leafEmail := makeLeaf(t, root, rootKey, []GeneralName{{Type: NameTypeEmail, Value: "1.2.3.4"}})
	if err := VerifyChain([]*x509.Certificate{leafEmail, root}, Options{Policy: PolicyLegacy}); err != nil {
		// The email value cannot be cross-matched, so the registeredID
		// permitted subtree is vacuously satisfied and the rest of the path
		// remains valid.
		t.Fatalf("email path rejected: %v", err)
	}

	leafURI := makeLeaf(t, root, rootKey, []GeneralName{{Type: NameTypeURI, Value: "1.2.3.4"}})
	if err := VerifyChain([]*x509.Certificate{leafURI, root}, Options{Policy: PolicyLegacy}); err != nil {
		t.Fatalf("URI path rejected: %v", err)
	}
}

func TestIntermediatePropagation(t *testing.T) {
	root, rootKey := makeRoot(t, nil)
	intermediate, intermediateKey := makeIntermediate(t, root, rootKey, &NameConstraints{
		Permitted: []GeneralSubtree{{Base: RegisteredIDName(testOID)}},
	})
	leaf := makeLeaf(t, intermediate, intermediateKey, []GeneralName{RegisteredIDName(testOIDOther)})
	if err := VerifyChain([]*x509.Certificate{leaf, intermediate, root}, Options{Policy: PolicyStrict}); err == nil {
		t.Fatal("intermediate name constraints were not applied to the leaf")
	}
	good := makeLeaf(t, intermediate, intermediateKey, []GeneralName{DNSName("1.2.3.4")})
	if err := VerifyChain(good, intermediate, root, Options{Policy: PolicyLegacy}); err != nil {
		t.Fatalf("valid intermediate path rejected: %v", err)
	}
}

func TestFlattenedConstraintFieldsAreUsable(t *testing.T) {
	nc := &NameConstraints{PermittedRegisteredIDs: []asn1.ObjectIdentifier{testOID}}
	if err := nc.Check([]GeneralName{DNSName("1.2.3.4")}, PolicyLegacy); err != nil {
		t.Fatalf("flattened permitted registeredID did not match: %v", err)
	}
	der, err := MarshalNameConstraints(nc)
	if err != nil {
		t.Fatalf("marshal flattened constraints: %v", err)
	}
	parsed, err := parseNameConstraints(der, true)
	if err != nil {
		t.Fatalf("parse flattened constraints: %v", err)
	}
	if len(parsed.PermittedRegisteredIDs) != 1 || !parsed.PermittedRegisteredIDs[0].Equal(testOID) {
		t.Fatalf("round trip lost the registeredID: %v", parsed.PermittedRegisteredIDs)
	}
}

func TestVerifyRegisteredID(t *testing.T) {
	root, rootKey := makeRoot(t, nil)
	leafDNS := makeLeaf(t, root, rootKey, []GeneralName{DNSName("1.2.3.4")})
	if err := VerifyRegisteredID(leafDNS, testOID, PolicyLegacy); err != nil {
		t.Fatalf("legacy DNS OID check failed: %v", err)
	}
	if err := VerifyRegisteredID(leafDNS, testOID, PolicyStrict); err == nil {
		t.Fatal("strict mode accepted a DNS name for a registeredID requirement")
	}
	leafEmail := makeLeaf(t, root, rootKey, []GeneralName{{Type: NameTypeEmail, Value: "1.2.3.4"}})
	if err := VerifyRegisteredID(leafEmail, testOID, PolicyLegacy); err == nil {
		t.Fatal("legacy mode accepted an email value for a registeredID requirement")
	}
	leafRegistered := makeLeaf(t, root, rootKey, []GeneralName{RegisteredIDName(testOID)})
	if err := VerifyRegisteredID(leafRegistered, testOID, PolicyStrict); err != nil {
		t.Fatalf("registeredID check failed: %v", err)
	}
}

func makeRoot(t *testing.T, nc *NameConstraints) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "registered-id root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	addConstraints(t, tmpl, nc)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	return cert, key
}

func makeIntermediate(t *testing.T, root *x509.Certificate, rootKey *ecdsa.PrivateKey, nc *NameConstraints) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "registered-id intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	addConstraints(t, tmpl, nc)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, &key.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create intermediate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse intermediate: %v", err)
	}
	return cert, key
}

func makeLeaf(t *testing.T, issuer *x509.Certificate, issuerKey *ecdsa.PrivateKey, names []GeneralName) *x509.Certificate {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	ext, err := SubjectAltNameExtension(names)
	if err != nil {
		t.Fatalf("subjectAltName extension: %v", err)
	}
	tmpl.ExtraExtensions = []pkix.Extension{ext}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return cert
}

func addConstraints(t *testing.T, tmpl *x509.Certificate, nc *NameConstraints) {
	t.Helper()
	if nc == nil {
		return
	}
	ext, err := NameConstraintsExtension(nc, true)
	if err != nil {
		t.Fatalf("nameConstraints extension: %v", err)
	}
	tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, ext)
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func errorsAs(err error, target any) bool {
	switch t := target.(type) {
	case **ConstraintViolationError:
		ce, ok := err.(*ConstraintViolationError)
		if ok {
			*t = ce
		}
		return ok
	default:
		return false
	}
}
