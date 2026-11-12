package netsec

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

var (
	oidQ0230SHA256WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidQ0230UnknownExt    = asn1.ObjectIdentifier{1, 2, 3, 4, 5}
)

type q0230PKI struct {
	ca1         *x509.Certificate
	ca1Key      *rsa.PrivateKey
	ca2         *x509.Certificate
	ca2Key      *rsa.PrivateKey
	holder100   *x509.Certificate
	holder101   *x509.Certificate
	holder2_100 *x509.Certificate
	authority   *x509.Certificate
	aaKey       *rsa.PrivateKey
}

func q0230NewPKI(t *testing.T) q0230PKI {
	t.Helper()
	ca1, ca1Key := q0230MakeCA(t, "Test CA 1", 1)
	ca2, ca2Key := q0230MakeCA(t, "Test CA 2", 2)
	holder100, _ := q0230MakeLeaf(t, ca1, ca1Key, "Holder 100", 100)
	holder101, _ := q0230MakeLeaf(t, ca1, ca1Key, "Holder 101", 101)
	holder2_100, _ := q0230MakeLeaf(t, ca2, ca2Key, "Other Holder 100", 100)
	authority, aaKey := q0230MakeCA(t, "Attribute Authority", 3)
	return q0230PKI{
		ca1:         ca1,
		ca1Key:      ca1Key,
		ca2:         ca2,
		ca2Key:      ca2Key,
		holder100:   holder100,
		holder101:   holder101,
		holder2_100: holder2_100,
		authority:   authority,
		aaKey:       aaKey,
	}
}

func q0230MakeCA(t *testing.T, commonName string, serial int64) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key := q0230MustRSAKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"netsec test"}},
		NotBefore:             time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	return cert, key
}

func q0230MakeLeaf(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, commonName string, serial int64) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key := q0230MustRSAKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{"netsec test"}},
		NotBefore:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return cert, key
}

func q0230MustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func TestQ0230VerifyLegalHolder(t *testing.T) {
	pki := q0230NewPKI(t)
	for name, holder := range map[string][]byte{
		"explicit": q0230BaseIDHolder(pki.holder100, pki.ca1),
		"implicit": q0230ImplicitBaseIDHolder(pki.holder100, pki.ca1),
	} {
		t.Run(name, func(t *testing.T) {
			ac := q0230BuildAC(t, pki, q0230ACSpec{holder: holder})
			if err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Fatalf("legal holder rejected: %v", err)
			}
		})
	}
}

func TestQ0230VerifyV2FormIssuer(t *testing.T) {
	pki := q0230NewPKI(t)
	explicit := q0230ExplicitDER(0, q0230SeqDER(q0230GeneralNamesDER(pki.authority.RawSubject)))
	implicit := q0230ExplicitDER(0, q0230GeneralNamesDER(pki.authority.RawSubject))
	authoritySerial := q0230SeqDER(
		q0230GeneralNamesDER(pki.authority.RawIssuer),
		q0230IntegerDER(pki.authority.SerialNumber),
	)
	explicitBaseID := q0230ExplicitDER(0, q0230SeqDER(q0230ExplicitDER(0, authoritySerial)))
	implicitBaseID := q0230ExplicitDER(0, q0230ExplicitDER(0, authoritySerial))
	issuers := map[string][]byte{
		"explicit":           explicit,
		"implicit":           implicit,
		"explicitBaseIDOnly": explicitBaseID,
		"implicitBaseIDOnly": implicitBaseID,
	}
	for name, issuer := range issuers {
		t.Run(name, func(t *testing.T) {
			ac := q0230BuildAC(t, pki, q0230ACSpec{
				holder: q0230BaseIDHolder(pki.holder100, pki.ca1),
				issuer: issuer,
			})
			if err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Fatalf("%s v2Form rejected: %v", name, err)
			}
		})
	}
}

func TestQ0230RejectsTransplantedCertificate(t *testing.T) {
	pki := q0230NewPKI(t)
	ac := q0230BuildAC(t, pki, q0230ACSpec{
		holder: q0230BaseIDHolder(pki.holder100, pki.ca1),
	})
	err := Verify(ac, pki.holder101, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("certificate transplant was accepted")
	}
	if !q0230ErrorsIs(err, ErrHolderMismatch) {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestQ0230RejectsSameSerialFromDifferentIssuer(t *testing.T) {
	pki := q0230NewPKI(t)
	ac := q0230BuildAC(t, pki, q0230ACSpec{
		holder: q0230BaseIDHolder(pki.holder100, pki.ca1),
	})
	err := Verify(ac, pki.holder2_100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("same serial from a different issuer was accepted")
	}
	if !q0230ErrorsIs(err, ErrHolderMismatch) {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestQ0230VerifyEntityName(t *testing.T) {
	pki := q0230NewPKI(t)
	ac := q0230BuildAC(t, pki, q0230ACSpec{
		holder: q0230EntityNameHolder(pki.holder100),
	})
	if err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("matching entityName rejected: %v", err)
	}
	if err := Verify(ac, pki.holder101, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("non-matching entityName was accepted")
	}
}

func TestQ0230VerifyObjectDigest(t *testing.T) {
	pki := q0230NewPKI(t)
	for _, objectType := range []int{DigestedObjectPublicKey, DigestedObjectPublicKeyCert} {
		ac := q0230BuildAC(t, pki, q0230ACSpec{
			holder: q0230ObjectDigestHolder(pki.holder100, objectType),
		})
		if err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("valid object digest type %d rejected: %v", objectType, err)
		}
		if err := Verify(ac, pki.holder101, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err == nil {
			t.Fatalf("object digest type %d matched the wrong certificate", objectType)
		}
	}
}

func TestQ0230HalfOpenValidity(t *testing.T) {
	pki := q0230NewPKI(t)
	notBefore := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)
	ac := q0230BuildAC(t, pki, q0230ACSpec{
		holder:    q0230BaseIDHolder(pki.holder100, pki.ca1),
		notBefore: notBefore,
		notAfter:  notAfter,
	})
	if err := Verify(ac, pki.holder100, pki.authority, notBefore); err != nil {
		t.Fatalf("notBefore must be inside the half-open interval: %v", err)
	}
	if err := Verify(ac, pki.holder100, pki.authority, notAfter); err == nil {
		t.Fatal("notAfter must be outside the half-open interval")
	}
	if err := Verify(ac, pki.holder100, pki.authority, notBefore.Add(-time.Second)); err == nil {
		t.Fatal("time before notBefore was accepted")
	}
}

func TestQ0230UnknownCriticalExtension(t *testing.T) {
	pki := q0230NewPKI(t)
	ac := q0230BuildAC(t, pki, q0230ACSpec{
		holder:     q0230BaseIDHolder(pki.holder100, pki.ca1),
		extensions: q0230ExtensionDER(oidQ0230UnknownExt, true, []byte{0x05, 0x00}),
	})
	err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC))
	if err == nil || !q0230ErrorsIs(err, ErrUnknownCriticalExtension) {
		t.Fatalf("unknown critical extension: %v", err)
	}

	ac = q0230BuildAC(t, pki, q0230ACSpec{
		holder:     q0230BaseIDHolder(pki.holder100, pki.ca1),
		extensions: q0230ExtensionDER(oidQ0230UnknownExt, false, []byte{0x05, 0x00}),
	})
	if err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("unknown non-critical extension rejected: %v", err)
	}
}

func TestQ0230RejectsBadAuthoritySignature(t *testing.T) {
	pki := q0230NewPKI(t)
	ac := q0230BuildAC(t, pki, q0230ACSpec{
		holder:   q0230BaseIDHolder(pki.holder100, pki.ca1),
		signWith: pki.ca1Key,
	})
	err := Verify(ac, pki.holder100, pki.authority, time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC))
	if err == nil || !q0230ErrorsIs(err, ErrSignature) {
		t.Fatalf("bad authority signature: %v", err)
	}
}

type q0230ACSpec struct {
	holder     []byte
	notBefore  time.Time
	notAfter   time.Time
	extensions []byte
	issuer     []byte
	signWith   *rsa.PrivateKey
}

func q0230BuildAC(t *testing.T, pki q0230PKI, spec q0230ACSpec) []byte {
	t.Helper()
	if spec.notBefore.IsZero() {
		spec.notBefore = time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if spec.notAfter.IsZero() {
		spec.notAfter = time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if spec.signWith == nil {
		spec.signWith = pki.aaKey
	}

	issuer := spec.issuer
	if issuer == nil {
		issuer = q0230GeneralNamesDER(pki.authority.RawSubject)
	}
	algorithm := q0230AlgIDDER(oidQ0230SHA256WithRSA, true)
	validity := q0230SeqDER(
		q0230GeneralizedTimeDER(spec.notBefore),
		q0230GeneralizedTimeDER(spec.notAfter),
	)
	attributes := q0230SeqDER()

	fields := [][]byte{
		q0230IntegerDER(1),
		q0230SeqDER(spec.holder),
		issuer,
		algorithm,
		q0230IntegerDER(500),
		validity,
		attributes,
	}
	if spec.extensions != nil {
		fields = append(fields, q0230SeqDER(spec.extensions))
	}
	acInfo := q0230SeqDER(fields...)

	digest := sha256.Sum256(acInfo)
	signature, err := rsa.SignPKCS1v15(rand.Reader, spec.signWith, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign attribute certificate: %v", err)
	}
	outer := q0230SeqDER(
		acInfo,
		algorithm,
		q0230BitStringDER(signature),
	)
	return outer
}

func q0230BaseIDHolder(holder, issuer *x509.Certificate) []byte {
	issuerSerial := q0230SeqDER(
		q0230GeneralNamesDER(issuer.RawSubject),
		q0230IntegerDER(holder.SerialNumber),
	)
	return q0230ExplicitDER(0, issuerSerial)
}

func q0230ImplicitBaseIDHolder(holder, issuer *x509.Certificate) []byte {
	content := bytes.Join([][]byte{
		q0230GeneralNamesDER(issuer.RawSubject),
		q0230IntegerDER(holder.SerialNumber),
	}, nil)
	return append(derLength(0xa0, len(content)), content...)
}

func q0230EntityNameHolder(holder *x509.Certificate) []byte {
	return q0230ExplicitDER(1, q0230GeneralNamesDER(holder.RawSubject))
}

func q0230ObjectDigestHolder(holder *x509.Certificate, objectType int) []byte {
	var object []byte
	if objectType == DigestedObjectPublicKey {
		object = holder.RawSubjectPublicKeyInfo
	} else {
		object = holder.Raw
	}
	digest := sha256.Sum256(object)
	info := q0230SeqDER(
		q0230EnumeratedDER(objectType),
		q0230AlgIDDER(oidSHA256, false),
		q0230BitStringDER(digest[:]),
	)
	return q0230ExplicitDER(2, info)
}

func q0230GeneralNamesDER(names ...[]byte) []byte {
	var items [][]byte
	for _, name := range names {
		items = append(items, q0230ExplicitDER(4, name))
	}
	return q0230SeqDER(items...)
}

func q0230ExtensionDER(oid asn1.ObjectIdentifier, critical bool, value []byte) []byte {
	fields := [][]byte{q0230OIDDER(oid)}
	if critical {
		fields = append(fields, []byte{0x01, 0x01, 0xff})
	}
	fields = append(fields, q0230OctetStringDER(value))
	return q0230SeqDER(fields...)
}

func q0230AlgIDDER(oid asn1.ObjectIdentifier, nullParameters bool) []byte {
	if nullParameters {
		return q0230SeqDER(q0230OIDDER(oid), []byte{0x05, 0x00})
	}
	return q0230SeqDER(q0230OIDDER(oid))
}

func q0230OIDDER(oid asn1.ObjectIdentifier) []byte {
	der, err := asn1.Marshal(oid)
	if err != nil {
		panic(err)
	}
	return der
}

func q0230IntegerDER(value interface{}) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return der
}

func q0230EnumeratedDER(value int) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	der[0] = 0x0a
	return der
}

func q0230GeneralizedTimeDER(t time.Time) []byte {
	der, err := asn1.MarshalWithParams(t.UTC(), "generalized")
	if err != nil {
		panic(err)
	}
	return der
}

func q0230BitStringDER(value []byte) []byte {
	der, err := asn1.Marshal(asn1.BitString{Bytes: value, BitLength: len(value) * 8})
	if err != nil {
		panic(err)
	}
	return der
}

func q0230OctetStringDER(value []byte) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return der
}

func q0230SeqDER(items ...[]byte) []byte {
	content := bytes.Join(items, nil)
	return append(derLength(0x30, len(content)), content...)
}

func q0230ExplicitDER(tag int, inner []byte) []byte {
	return append(derLength(byte(0xa0|tag), len(inner)), inner...)
}

func q0230ErrorsIs(err error, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
