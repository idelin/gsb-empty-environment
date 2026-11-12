package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
	"time"
)

var (
	testNotBefore = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	testNotAfter  = time.Date(2027, 11, 1, 0, 0, 0, 0, time.UTC)
	verifyTime    = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	validTS       = verifyTime.Add(-time.Hour).UnixMilli()
)

type testCA struct {
	cert *x509.Certificate
	key  crypto.Signer
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             testNotBefore,
		NotAfter:              testNotAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return &testCA{cert: cert, key: key}
}

func baseLeafTemplate(serial int64) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "leaf.example"},
		NotBefore:             testNotBefore,
		NotAfter:              testNotAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"leaf.example"},
	}
}

func (ca *testCA) issue(t *testing.T, tmpl *x509.Certificate) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	cert := ca.issueWithKey(t, tmpl, leafKey)
	return cert, leafKey
}

func (ca *testCA) issueWithKey(t *testing.T, tmpl *x509.Certificate, leafKey crypto.Signer) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, leafKey.Public(), ca.key)
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	return cert
}

func poisonExt(critical bool) pkix.Extension {
	return pkix.Extension{
		Id:       oidExtensionPoison,
		Critical: critical,
		Value:    asn1.NullBytes,
	}
}

func sctListExtension(scts [][]byte) pkix.Extension {
	var payload []byte
	for _, sct := range scts {
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(sct)))
		payload = append(payload, lenBuf[:]...)
		payload = append(payload, sct...)
	}
	value := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(value[:2], uint16(len(payload)))
	copy(value[2:], payload)
	return pkix.Extension{Id: oidExtensionSCTList, Value: value}
}

// signSCT produces a v1 SerializedSCT over the given precertificate TBS,
// mirroring what a real CT log returns (RFC 6962 section 3.2).
func signSCT(t *testing.T, logKey crypto.Signer, timestamp int64, issuerKeyHash [32]byte, tbs []byte, hashAlg, sigAlg uint8) []byte {
	t.Helper()
	var sctBuf bytes.Buffer
	sctBuf.WriteByte(sctVersionV1)
	sctBuf.WriteByte(sigTypeCertificateTimestamp)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(timestamp))
	sctBuf.Write(tmp[:])
	binary.BigEndian.PutUint16(tmp[:2], logEntryTypePrecert)
	sctBuf.Write(tmp[:2])
	sctBuf.Write(issuerKeyHash[:])
	sctBuf.Write([]byte{byte(len(tbs) >> 16), byte(len(tbs) >> 8), byte(len(tbs))})
	sctBuf.Write(tbs)
	sctBuf.Write([]byte{0, 0}) // empty SCT extensions

	var (
		hashID crypto.Hash
		digest []byte
	)
	switch hashAlg {
	case hashAlgSHA256:
		hashID = crypto.SHA256
		sum := sha256.Sum256(sctBuf.Bytes())
		digest = sum[:]
	case hashAlgSHA384:
		hashID = crypto.SHA384
		sum := sha512.Sum384(sctBuf.Bytes())
		digest = sum[:]
	default:
		t.Fatalf("unsupported test hash alg %d", hashAlg)
	}

	var signature []byte
	switch key := logKey.(type) {
	case *ecdsa.PrivateKey:
		sig, err := ecdsa.SignASN1(rand.Reader, key, digest)
		if err != nil {
			t.Fatalf("sign SCT: %v", err)
		}
		signature = sig
	case *rsa.PrivateKey:
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, hashID, digest)
		if err != nil {
			t.Fatalf("sign SCT: %v", err)
		}
		signature = sig
	default:
		t.Fatalf("unsupported log key type %T", logKey)
	}

	logSPKI, err := x509.MarshalPKIXPublicKey(logKey.Public())
	if err != nil {
		t.Fatalf("marshal log SPKI: %v", err)
	}
	logID := sha256.Sum256(logSPKI)

	var out bytes.Buffer
	out.WriteByte(sctVersionV1)
	out.Write(logID[:])
	binary.BigEndian.PutUint64(tmp[:], uint64(timestamp))
	out.Write(tmp[:])
	out.Write([]byte{0, 0}) // SCT extensions length
	out.WriteByte(hashAlg)
	out.WriteByte(sigAlg)
	binary.BigEndian.PutUint16(tmp[:2], uint16(len(signature)))
	out.Write(tmp[:2])
	out.Write(signature)
	return out.Bytes()
}

func newECDSALog(t *testing.T) (*CTLog, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate log key: %v", err)
	}
	log, err := NewCTLog(&key.PublicKey)
	if err != nil {
		t.Fatalf("new CT log: %v", err)
	}
	return log, key
}

func newRSALog(t *testing.T) (*CTLog, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA log key: %v", err)
	}
	log, err := NewCTLog(&key.PublicKey)
	if err != nil {
		t.Fatalf("new CT log: %v", err)
	}
	return log, key
}

func issuerHash(cert *x509.Certificate) [32]byte {
	return sha256.Sum256(cert.RawSubjectPublicKeyInfo)
}

// issuePair issues a precertificate and a final certificate sharing the same
// leaf key, as happens in the real issuance flow.
func issuePair(t *testing.T, ca *testCA, serial int64, finalExts []pkix.Extension) (crypto.Signer, *x509.Certificate, *x509.Certificate) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	preTmpl := baseLeafTemplate(serial)
	preTmpl.ExtraExtensions = []pkix.Extension{poisonExt(true)}
	precert := ca.issueWithKey(t, preTmpl, leafKey)

	finalTmpl := baseLeafTemplate(serial)
	finalTmpl.ExtraExtensions = finalExts
	final := ca.issueWithKey(t, finalTmpl, leafKey)
	return leafKey, precert, final
}

// buildValidPair issues a precertificate, obtains an SCT for it, and issues the
// final certificate embedding that SCT.
func buildValidPair(t *testing.T, ca *testCA, logKey crypto.Signer, serial int64, ts int64) (precert, final *x509.Certificate, sct []byte) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	preTmpl := baseLeafTemplate(serial)
	preTmpl.ExtraExtensions = []pkix.Extension{poisonExt(true)}
	precert = ca.issueWithKey(t, preTmpl, leafKey)

	sct = signSCT(t, logKey, ts, issuerHash(ca.cert), precert.RawTBSCertificate, hashAlgSHA256, sigAlgECDSA)

	finalTmpl := baseLeafTemplate(serial)
	finalTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{sct})}
	final = ca.issueWithKey(t, finalTmpl, leafKey)
	return precert, final, sct
}

func TestRebuildPrecertTBSMatchesRealPrecert(t *testing.T) {
	ca := newTestCA(t)
	_, logKey := newECDSALog(t)
	precert, final, _ := buildValidPair(t, ca, logKey, 100, validTS)

	rebuilt, err := rebuildPrecertTBS(final.RawTBSCertificate)
	if err != nil {
		t.Fatalf("rebuild precert TBS: %v", err)
	}
	if !bytes.Equal(rebuilt, precert.RawTBSCertificate) {
		t.Fatal("rebuilt precertificate TBS does not match the issued precertificate TBS")
	}
}

func TestVerifyEmbeddedSCTsLegal(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)
	_, final, _ := buildValidPair(t, ca, logKey, 101, validTS)

	store := NewLogStore(log)
	if err := store.VerifyEmbeddedSCTs(final, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("legal certificate failed SCT verification: %v", err)
	}
}

func TestVerifyEmbeddedSCTsRSALog(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newRSALog(t)

	leafKey, precert, _ := issuePair(t, ca, 102, nil)
	sct := signSCT(t, logKey, validTS, issuerHash(ca.cert), precert.RawTBSCertificate, hashAlgSHA256, sigAlgRSA)

	finalTmpl := baseLeafTemplate(102)
	finalTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{sct})}
	final := ca.issueWithKey(t, finalTmpl, leafKey)

	if err := NewLogStore(log).VerifyEmbeddedSCTs(final, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("RSA log SCT failed: %v", err)
	}
}

func TestVerifyEmbeddedSCTsSHA384(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)

	leafKey, precert, _ := issuePair(t, ca, 103, nil)
	sct := signSCT(t, logKey, validTS, issuerHash(ca.cert), precert.RawTBSCertificate, hashAlgSHA384, sigAlgECDSA)

	finalTmpl := baseLeafTemplate(103)
	finalTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{sct})}
	final := ca.issueWithKey(t, finalTmpl, leafKey)

	if err := NewLogStore(log).VerifyEmbeddedSCTs(final, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("SHA-384 SCT failed: %v", err)
	}
}

func TestFinalCertificateWithPoisonRejected(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)
	_, final, sct := buildValidPair(t, ca, logKey, 104, validTS)

	// Re-issue the leaf with a critical poison extension while still carrying
	// SCTs: a precertificate presented as a final certificate.
	evilTmpl := baseLeafTemplate(104)
	evilTmpl.ExtraExtensions = []pkix.Extension{poisonExt(true), sctListExtension([][]byte{sct})}
	evil, _ := ca.issue(t, evilTmpl)

	err := NewLogStore(log).VerifyEmbeddedSCTs(evil, ca.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrPoisonInFinal) {
		t.Fatalf("expected ErrPoisonInFinal, got %v", err)
	}

	// Even without any SCTs, poison on a presented leaf is rejected first.
	plainPoisonTmpl := baseLeafTemplate(105)
	plainPoisonTmpl.ExtraExtensions = []pkix.Extension{poisonExt(true)}
	plainPoison, _ := ca.issue(t, plainPoisonTmpl)
	err = NewLogStore(log).VerifyEmbeddedSCTs(plainPoison, ca.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrPoisonInFinal) {
		t.Fatalf("expected ErrPoisonInFinal for bare poison leaf, got %v", err)
	}

	// Sanity: the unmodified final certificate still verifies.
	if err := NewLogStore(log).VerifyEmbeddedSCTs(final, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("legal certificate regressed: %v", err)
	}
}

func TestTamperedExtensionBreaksSCT(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)
	_, _, sct := buildValidPair(t, ca, logKey, 106, validTS)

	// Re-sign the same leaf content with an extra extension. The embedded SCT
	// was issued over a different precertificate TBS and must no longer verify.
	tamperedTmpl := baseLeafTemplate(106)
	tamperedTmpl.DNSNames = []string{"leaf.example", "attacker.example"}
	tamperedTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{sct})}
	tampered, _ := ca.issue(t, tamperedTmpl)

	err := NewLogStore(log).VerifyEmbeddedSCTs(tampered, ca.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrSCTSignature) {
		t.Fatalf("expected ErrSCTSignature for tampered leaf, got %v", err)
	}
}

func TestSCTListMustNotParticipateInDigest(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)

	// Attacker signs over the final certificate TBS *with* the SCT list
	// extension still present. A correct verifier strips the extension before
	// rebuilding the precertificate TBS, so this signature must not verify.
	leafKey, precert, placeholder := issuePair(t, ca, 107, []pkix.Extension{sctListExtension(nil)})
	badSCT := signSCT(t, logKey, validTS, issuerHash(ca.cert), placeholder.RawTBSCertificate, hashAlgSHA256, sigAlgECDSA)

	evilTmpl := baseLeafTemplate(107)
	evilTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{badSCT})}
	evil := ca.issueWithKey(t, evilTmpl, leafKey)

	err := NewLogStore(log).VerifyEmbeddedSCTs(evil, ca.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrSCTSignature) {
		t.Fatalf("expected ErrSCTSignature when SCT list participated in digest, got %v", err)
	}

	// An SCT over the properly rebuilt (stripped) TBS verifies.
	goodSCT := signSCT(t, logKey, validTS, issuerHash(ca.cert), precert.RawTBSCertificate, hashAlgSHA256, sigAlgECDSA)
	goodTmpl := baseLeafTemplate(107)
	goodTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{goodSCT})}
	good := ca.issueWithKey(t, goodTmpl, leafKey)
	if err := NewLogStore(log).VerifyEmbeddedSCTs(good, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("properly rebuilt TBS SCT failed: %v", err)
	}
}

func TestSCTTimestampNotInFuture(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)

	// Boundary: timestamp equal to verification time is accepted.
	_, onTime, _ := buildValidPair(t, ca, logKey, 109, verifyTime.UnixMilli())
	if err := NewLogStore(log).VerifyEmbeddedSCTs(onTime, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("boundary timestamp rejected: %v", err)
	}

	// One millisecond later must be rejected.
	_, future, _ := buildValidPair(t, ca, logKey, 110, verifyTime.Add(time.Millisecond).UnixMilli())
	err := NewLogStore(log).VerifyEmbeddedSCTs(future, ca.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrSCTInFuture) {
		t.Fatalf("expected ErrSCTInFuture, got %v", err)
	}
}

func TestUnknownLogRejected(t *testing.T) {
	ca := newTestCA(t)
	trustedLog, _ := newECDSALog(t)
	_, rogueKey := newECDSALog(t)
	_, final, _ := buildValidPair(t, ca, rogueKey, 111, validTS)

	err := NewLogStore(trustedLog).VerifyEmbeddedSCTs(final, ca.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrUnknownLog) {
		t.Fatalf("expected ErrUnknownLog, got %v", err)
	}
}

func TestMissingAndMalformedSCTs(t *testing.T) {
	ca := newTestCA(t)
	log, _ := newECDSALog(t)

	tmpl := baseLeafTemplate(112)
	leaf, _ := ca.issue(t, tmpl)
	if err := NewLogStore(log).VerifyEmbeddedSCTs(leaf, ca.cert, &VerifyOptions{Now: verifyTime}); !errors.Is(err, ErrNoSCTs) {
		t.Fatalf("expected ErrNoSCTs, got %v", err)
	}

	badTmpl := baseLeafTemplate(113)
	badTmpl.ExtraExtensions = []pkix.Extension{{Id: oidExtensionSCTList, Value: []byte{0x00, 0x09, 0x00}}}
	badLeaf, _ := ca.issue(t, badTmpl)
	if err := NewLogStore(log).VerifyEmbeddedSCTs(badLeaf, ca.cert, &VerifyOptions{Now: verifyTime}); !errors.Is(err, ErrMalformedSCT) {
		t.Fatalf("expected ErrMalformedSCT, got %v", err)
	}

	if err := NewLogStore(log).VerifyEmbeddedSCTs(nil, ca.cert, nil); !errors.Is(err, ErrMissingCertificate) {
		t.Fatalf("expected ErrMissingCertificate, got %v", err)
	}
	if err := NewLogStore(log).VerifyEmbeddedSCTs(leaf, nil, nil); !errors.Is(err, ErrMissingIssuer) {
		t.Fatalf("expected ErrMissingIssuer, got %v", err)
	}
}

func TestVerifyPrecertificateSCTs(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)

	preTmpl := baseLeafTemplate(120)
	preTmpl.ExtraExtensions = []pkix.Extension{poisonExt(true)}
	precert, _ := ca.issue(t, preTmpl)
	sct := signSCT(t, logKey, validTS, issuerHash(ca.cert), precert.RawTBSCertificate, hashAlgSHA256, sigAlgECDSA)

	store := NewLogStore(log)
	if err := store.VerifyPrecertificateSCTs(precert, ca.cert, [][]byte{sct}, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("precertificate SCT failed: %v", err)
	}

	// A precertificate without poison is not a valid precertificate.
	plainTmpl := baseLeafTemplate(121)
	plain, _ := ca.issue(t, plainTmpl)
	err := store.VerifyPrecertificateSCTs(plain, ca.cert, [][]byte{sct}, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrMissingPoison) {
		t.Fatalf("expected ErrMissingPoison, got %v", err)
	}

	// Poison must be critical with a NULL value.
	nonCriticalTmpl := baseLeafTemplate(122)
	nonCriticalTmpl.ExtraExtensions = []pkix.Extension{poisonExt(false)}
	nonCritical, _ := ca.issue(t, nonCriticalTmpl)
	err = store.VerifyPrecertificateSCTs(nonCritical, ca.cert, [][]byte{sct}, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrInvalidPoison) {
		t.Fatalf("expected ErrInvalidPoison (non-critical), got %v", err)
	}

	badValueTmpl := baseLeafTemplate(123)
	badValueTmpl.ExtraExtensions = []pkix.Extension{{Id: oidExtensionPoison, Critical: true, Value: []byte{0x01, 0x00}}}
	badValue, _ := ca.issue(t, badValueTmpl)
	err = store.VerifyPrecertificateSCTs(badValue, ca.cert, [][]byte{sct}, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrInvalidPoison) {
		t.Fatalf("expected ErrInvalidPoison (bad value), got %v", err)
	}

	if err := store.VerifyPrecertificateSCTs(precert, ca.cert, nil, &VerifyOptions{Now: verifyTime}); !errors.Is(err, ErrNoSCTs) {
		t.Fatalf("expected ErrNoSCTs, got %v", err)
	}
}

func TestPrecertWithSCTListStripsIt(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)

	// Defensive: even if an SCT list extension is present on the precert, the
	// signed TBS must exclude it. Sign over the stripped TBS and verify the
	// verifier performs the same stripping.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	combinedTmpl := baseLeafTemplate(124)
	combinedTmpl.ExtraExtensions = []pkix.Extension{
		poisonExt(true),
		sctListExtension(nil),
	}
	combined := ca.issueWithKey(t, combinedTmpl, leafKey)
	strippedTBS, err := stripSCTList(combined.RawTBSCertificate)
	if err != nil {
		t.Fatalf("strip SCT list: %v", err)
	}
	sct := signSCT(t, logKey, validTS, issuerHash(ca.cert), strippedTBS, hashAlgSHA256, sigAlgECDSA)

	// Re-issue with a real SCT list, same key and serial so the TBS core is
	// identical apart from the SCT bytes.
	realTmpl := baseLeafTemplate(124)
	realTmpl.ExtraExtensions = []pkix.Extension{
		poisonExt(true),
		sctListExtension([][]byte{sct}),
	}
	combined = ca.issueWithKey(t, realTmpl, leafKey)
	if err := NewLogStore(log).VerifyPrecertificateSCTs(combined, ca.cert, [][]byte{sct}, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("precert with SCT list extension should strip it before verification: %v", err)
	}
}

func TestIssuerKeyHashComesFromActualIssuer(t *testing.T) {
	ca := newTestCA(t)
	otherCA := newTestCA(t)
	log, logKey := newECDSALog(t)

	leafKey, precert, _ := issuePair(t, ca, 130, nil)
	// Sign with the correct issuer's key hash.
	sct := signSCT(t, logKey, validTS, issuerHash(ca.cert), precert.RawTBSCertificate, hashAlgSHA256, sigAlgECDSA)

	finalTmpl := baseLeafTemplate(130)
	finalTmpl.ExtraExtensions = []pkix.Extension{sctListExtension([][]byte{sct})}
	final := ca.issueWithKey(t, finalTmpl, leafKey)

	// Verifying against a different issuer's key hash fails.
	err := NewLogStore(log).VerifyEmbeddedSCTs(final, otherCA.cert, &VerifyOptions{Now: verifyTime})
	if !errors.Is(err, ErrSCTSignature) {
		t.Fatalf("expected ErrSCTSignature with wrong issuer, got %v", err)
	}

	// The actual issuer makes it pass.
	if err := NewLogStore(log).VerifyEmbeddedSCTs(final, ca.cert, &VerifyOptions{Now: verifyTime}); err != nil {
		t.Fatalf("verification with actual issuer failed: %v", err)
	}
}

func TestVerifyConnectionCallback(t *testing.T) {
	ca := newTestCA(t)
	log, logKey := newECDSALog(t)
	_, final, _ := buildValidPair(t, ca, logKey, 140, validTS)
	store := NewLogStore(log)

	// Peer chain only: issuer is taken from the presented chain.
	cs := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{final, ca.cert},
	}
	if err := store.VerifyConnectionAt(cs, verifyTime); err != nil {
		t.Fatalf("VerifyConnectionAt (peer chain) failed: %v", err)
	}

	// A verified chain is preferred when present.
	csVerified := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{final},
		VerifiedChains:   [][]*x509.Certificate{{final, ca.cert}},
	}
	if err := store.VerifyConnectionAt(csVerified, verifyTime); err != nil {
		t.Fatalf("VerifyConnectionAt (verified chain) failed: %v", err)
	}

	// Poison presented at the TLS layer is rejected through the callback too.
	poisonTmpl := baseLeafTemplate(141)
	poisonTmpl.ExtraExtensions = []pkix.Extension{poisonExt(true)}
	poisonLeaf, _ := ca.issue(t, poisonTmpl)
	csPoison := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{poisonLeaf, ca.cert},
	}
	if err := store.VerifyConnectionAt(csPoison, verifyTime); !errors.Is(err, ErrPoisonInFinal) {
		t.Fatalf("expected ErrPoisonInFinal via callback, got %v", err)
	}

	// Empty connection state.
	if err := store.VerifyConnectionAt(tls.ConnectionState{}, verifyTime); !errors.Is(err, ErrMissingCertificate) {
		t.Fatalf("expected ErrMissingCertificate, got %v", err)
	}
}
