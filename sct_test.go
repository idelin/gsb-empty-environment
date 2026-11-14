package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// testLog is a fake CT log with its signing key.
type testLog struct {
	log *Log
	key *ecdsa.PrivateKey
}

func newTestLog(t *testing.T, name string) *testLog {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	log, err := NewLog(name, key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return &testLog{log: log, key: key}
}

func newCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             testNow.Add(-time.Hour),
		NotAfter:              testNow.Add(30 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		SubjectKeyId:          []byte{1, 2, 3, 4},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func leafTemplate(serial int64) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "www.example.com"},
		DNSNames:     []string{"www.example.com"},
		NotBefore:    testNow.Add(-time.Hour),
		NotAfter:     testNow.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
}

// signSCT creates an SCT from tl over entry at timestamp ts.
func signSCT(t *testing.T, tl *testLog, entry []byte, ts uint64) *SCT {
	t.Helper()
	sct := &SCT{
		Version:            VersionV1,
		LogID:              tl.log.ID,
		Timestamp:          ts,
		HashAlgorithm:      HashSHA256,
		SignatureAlgorithm: SigECDSA,
	}
	digest := sha256.Sum256(signatureInput(sct, entry))
	sig, err := ecdsa.SignASN1(rand.Reader, tl.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sct.Signature = sig
	return sct
}

// leafFixture builds a precert/final certificate pair sharing one leaf key:
// the precert TBS is what logs sign, the final cert embeds the SCT list.
type leafFixture struct {
	ca         *x509.Certificate
	caKey      *ecdsa.PrivateKey
	leafKey    *ecdsa.PrivateKey
	precertTBS []byte
	issuerHash [32]byte
}

func newLeafFixture(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64) *leafFixture {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, leafTemplate(serial), ca, leafKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &leafFixture{
		ca:         ca,
		caKey:      caKey,
		leafKey:    leafKey,
		precertTBS: pre.RawTBSCertificate,
		issuerHash: IssuerKeyHash(ca),
	}
}

// entry returns the precert log entry logs would sign for this fixture.
func (f *leafFixture) entry() []byte {
	return precertEntry(f.issuerHash, f.precertTBS)
}

// sctList signs this fixture's precert entry with each log at ts.
func (f *leafFixture) sctList(t *testing.T, logs []*testLog, ts uint64) []byte {
	t.Helper()
	var scts []*SCT
	for _, tl := range logs {
		scts = append(scts, signSCT(t, tl, f.entry(), ts))
	}
	return marshalSCTList(scts)
}

// issueFinal creates the final certificate embedding sctList.
func (f *leafFixture) issueFinal(t *testing.T, serial int64, sctList []byte) *x509.Certificate {
	t.Helper()
	tmpl := leafTemplate(serial)
	if sctList != nil {
		tmpl.ExtraExtensions = []pkix.Extension{{Id: oidExtensionSCT, Value: sctList}}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, f.ca, f.leafKey.Public(), f.caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func embeddedList(t *testing.T, leaf *x509.Certificate) []byte {
	t.Helper()
	list, ok := SCTListExtension(leaf)
	if !ok {
		t.Fatal("leaf has no SCT extension")
	}
	return list
}

func newVerifier(t *testing.T, gen uint64, logs []*testLog, minLogs int, maxAge time.Duration) *Verifier {
	t.Helper()
	ll := &LogList{Generation: gen}
	for _, tl := range logs {
		ll.Logs = append(ll.Logs, tl.log)
	}
	v, err := NewVerifier(ll, Policy{Generation: gen, MinDistinctLogs: minLogs, MaxAge: maxAge})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ms(tm time.Time) uint64 { return uint64(tm.UnixMilli()) }

// TestValidDoubleSCT: two SCTs from two distinct trusted logs prove the
// current certificate.
func TestValidDoubleSCT(t *testing.T) {
	ca, caKey := newCA(t)
	l1, l2 := newTestLog(t, "log-alpha"), newTestLog(t, "log-beta")
	v := newVerifier(t, 1, []*testLog{l1, l2}, 2, 24*time.Hour)

	fx := newLeafFixture(t, ca, caKey, 100)
	leaf := fx.issueFinal(t, 100, fx.sctList(t, []*testLog{l1, l2}, ms(testNow.Add(-time.Hour))))

	res, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if err != nil {
		t.Fatalf("valid double SCT rejected: %v", err)
	}
	if res.ValidSCTs != 2 || res.Generation != 1 || len(res.LogIDs) != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestPrecertTBSRebuildMatches: the rebuilt precert TBS is byte-identical to
// the originally logged one.
func TestPrecertTBSRebuildMatches(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	fx := newLeafFixture(t, ca, caKey, 100)
	leaf := fx.issueFinal(t, 100, fx.sctList(t, []*testLog{l1}, ms(testNow)))

	rebuilt, err := PrecertTBSCertificate(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(rebuilt, fx.precertTBS) {
		t.Fatal("rebuilt precert TBS does not match logged TBS")
	}
}

// TestSCTFromAnotherCertificate: an SCT list minted for a different
// certificate must not validate for this leaf.
func TestSCTFromAnotherCertificate(t *testing.T) {
	ca, caKey := newCA(t)
	l1, l2 := newTestLog(t, "log-alpha"), newTestLog(t, "log-beta")
	v := newVerifier(t, 1, []*testLog{l1, l2}, 2, 24*time.Hour)

	// SCTs commit to certificate serial 200, but are embedded in serial 100.
	other := newLeafFixture(t, ca, caKey, 200)
	foreign := other.sctList(t, []*testLog{l1, l2}, ms(testNow.Add(-time.Hour)))
	victim := newLeafFixture(t, ca, caKey, 100)
	leaf := victim.issueFinal(t, 100, foreign)

	_, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("foreign SCT accepted or wrong error: %v", err)
	}
}

// TestSCTFromAnotherCertificateTransmitted: same swap attack against
// TLS-transmitted (x509_entry) SCTs.
func TestSCTFromAnotherCertificateTransmitted(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	v := newVerifier(t, 1, []*testLog{l1}, 1, 24*time.Hour)

	leafB := newLeafFixture(t, ca, caKey, 200).issueFinal(t, 200, nil)
	sctB := signSCT(t, l1, x509Entry(leafB.Raw), ms(testNow.Add(-time.Hour)))

	leafA := newLeafFixture(t, ca, caKey, 100).issueFinal(t, 100, nil)
	_, err := v.VerifyTransmittedSCTs(leafA, marshalSCTList([]*SCT{sctB}), testNow)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("foreign transmitted SCT accepted or wrong error: %v", err)
	}

	// Sanity: the SCT does prove its own certificate.
	sctA := signSCT(t, l1, x509Entry(leafA.Raw), ms(testNow.Add(-time.Hour)))
	if _, err := v.VerifyTransmittedSCTs(leafA, marshalSCTList([]*SCT{sctA}), testNow); err != nil {
		t.Fatalf("own transmitted SCT rejected: %v", err)
	}
}

// TestOldLogRemovedFromTrustList: after a generation update drops a log, its
// SCTs no longer count.
func TestOldLogRemovedFromTrustList(t *testing.T) {
	ca, caKey := newCA(t)
	l1, l2 := newTestLog(t, "log-alpha"), newTestLog(t, "log-beta")
	v := newVerifier(t, 1, []*testLog{l1, l2}, 1, 24*time.Hour)

	fx := newLeafFixture(t, ca, caKey, 100)
	leaf := fx.issueFinal(t, 100, fx.sctList(t, []*testLog{l1, l2}, ms(testNow.Add(-time.Hour))))

	// Generation 2 retires log-beta.
	if err := v.UpdateGeneration(
		&LogList{Generation: 2, Logs: []*Log{l1.log}},
		Policy{Generation: 2, MinDistinctLogs: 1, MaxAge: 24 * time.Hour},
	); err != nil {
		t.Fatal(err)
	}
	_, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if !errors.Is(err, ErrUnknownLog) {
		t.Fatalf("SCT from retired log accepted or wrong error: %v", err)
	}
}

// TestTimestampAfterNotAfter: an SCT dated after the certificate's notAfter
// is rejected.
func TestTimestampAfterNotAfter(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	v := newVerifier(t, 1, []*testLog{l1}, 1, 0)

	fx := newLeafFixture(t, ca, caKey, 100)
	notAfter := leafTemplate(100).NotAfter
	list := fx.sctList(t, []*testLog{l1}, ms(notAfter.Add(time.Hour)))
	leaf := fx.issueFinal(t, 100, list)

	// Validate at a time after the SCT timestamp so only the notAfter
	// check can fire.
	_, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), notAfter.Add(2*time.Hour))
	if !errors.Is(err, ErrTimestampAfterNotAfter) {
		t.Fatalf("expired-timestamp SCT accepted or wrong error: %v", err)
	}
}

// TestSCTTooOld: an SCT older than the policy max age is rejected.
func TestSCTTooOld(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	v := newVerifier(t, 1, []*testLog{l1}, 1, time.Hour)

	fx := newLeafFixture(t, ca, caKey, 100)
	leaf := fx.issueFinal(t, 100, fx.sctList(t, []*testLog{l1}, ms(testNow.Add(-2*time.Hour))))

	_, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if !errors.Is(err, ErrSCTTooOld) {
		t.Fatalf("stale SCT accepted or wrong error: %v", err)
	}
}

// TestDuplicateLogs: two SCTs from the same log are rejected outright.
func TestDuplicateLogs(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	v := newVerifier(t, 1, []*testLog{l1}, 1, 24*time.Hour)

	fx := newLeafFixture(t, ca, caKey, 100)
	s1 := signSCT(t, l1, fx.entry(), ms(testNow.Add(-2*time.Hour)))
	s2 := signSCT(t, l1, fx.entry(), ms(testNow.Add(-time.Hour)))
	leaf := fx.issueFinal(t, 100, marshalSCTList([]*SCT{s1, s2}))

	_, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if !errors.Is(err, ErrDuplicateLog) {
		t.Fatalf("duplicate-log SCTs accepted or wrong error: %v", err)
	}
}

// TestZeroSignature: an all-zero signature is rejected before verification.
func TestZeroSignature(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	v := newVerifier(t, 1, []*testLog{l1}, 1, 24*time.Hour)

	fx := newLeafFixture(t, ca, caKey, 100)
	sct := signSCT(t, l1, fx.entry(), ms(testNow.Add(-time.Hour)))
	sct.Signature = make([]byte, 64)
	leaf := fx.issueFinal(t, 100, marshalSCTList([]*SCT{sct}))

	_, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if !errors.Is(err, ErrZeroSignature) {
		t.Fatalf("zero-signature SCT accepted or wrong error: %v", err)
	}
}

// TestPolicyUpdateByGeneration: a generation update changes the effective
// policy; stale generations are rejected.
func TestPolicyUpdateByGeneration(t *testing.T) {
	ca, caKey := newCA(t)
	l1, l2 := newTestLog(t, "log-alpha"), newTestLog(t, "log-beta")
	v := newVerifier(t, 1, []*testLog{l1, l2}, 2, 24*time.Hour)

	fx := newLeafFixture(t, ca, caKey, 100)
	leaf := fx.issueFinal(t, 100, fx.sctList(t, []*testLog{l1}, ms(testNow.Add(-time.Hour))))

	// Generation 1 requires two logs: a single SCT is not enough.
	if _, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow); !errors.Is(err, ErrNotEnoughLogs) {
		t.Fatalf("single SCT under two-log policy: %v", err)
	}

	// Stale and mismatched generations are refused.
	if err := v.UpdateGeneration(
		&LogList{Generation: 1, Logs: []*Log{l1.log}},
		Policy{Generation: 1, MinDistinctLogs: 1},
	); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation accepted: %v", err)
	}
	if err := v.UpdateGeneration(
		&LogList{Generation: 2, Logs: []*Log{l1.log}},
		Policy{Generation: 3, MinDistinctLogs: 1},
	); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("mismatched generation accepted: %v", err)
	}

	// Generation 2 relaxes the policy to a single log.
	if err := v.UpdateGeneration(
		&LogList{Generation: 2, Logs: []*Log{l1.log, l2.log}},
		Policy{Generation: 2, MinDistinctLogs: 1, MaxAge: 24 * time.Hour},
	); err != nil {
		t.Fatal(err)
	}
	res, err := v.VerifyEmbeddedSCTs(leaf, ca, embeddedList(t, leaf), testNow)
	if err != nil {
		t.Fatalf("single SCT under generation-2 policy rejected: %v", err)
	}
	if res.Generation != 2 {
		t.Fatalf("result reports generation %d, want 2", res.Generation)
	}
}

// TestParseSCTListFields: parsing exposes version, log ID, timestamp and
// signature algorithms.
func TestParseSCTListFields(t *testing.T) {
	ca, caKey := newCA(t)
	l1 := newTestLog(t, "log-alpha")
	fx := newLeafFixture(t, ca, caKey, 100)
	sct := signSCT(t, l1, fx.entry(), ms(testNow))
	sct.Extensions = []byte{0xde, 0xad}

	scts, err := ParseSCTList(marshalSCTList([]*SCT{sct}))
	if err != nil {
		t.Fatal(err)
	}
	if len(scts) != 1 {
		t.Fatalf("got %d SCTs, want 1", len(scts))
	}
	got := scts[0]
	if got.Version != VersionV1 || got.LogID != l1.log.ID ||
		got.Timestamp != ms(testNow) ||
		got.HashAlgorithm != HashSHA256 || got.SignatureAlgorithm != SigECDSA ||
		!bytesEqual(got.Extensions, []byte{0xde, 0xad}) {
		t.Fatalf("parsed SCT fields mismatch: %+v", got)
	}
}
