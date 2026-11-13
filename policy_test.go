package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

func makeLeafDER(t *testing.T) ([]byte, crypto.Signer) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "leaf.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return der, key
}

func marshalTestSCT(logID []byte, ts uint64, exts []byte, hashAlgo, sigAlgo uint8, sig []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(Version1)
	buf.Write(logID)
	buf.Write(uint64Bytes(ts))
	buf.Write(uint16Bytes(uint16(len(exts))))
	buf.Write(exts)
	buf.WriteByte(hashAlgo)
	buf.WriteByte(sigAlgo)
	buf.Write(uint16Bytes(uint16(len(sig))))
	buf.Write(sig)
	return buf.Bytes()
}

// signSCT produces a serialized SCT signed by logKey over certDER.
func signSCT(t *testing.T, logKey crypto.Signer, hashAlgo, sigAlgo uint8, certDER []byte) []byte {
	t.Helper()
	logID, err := LogID(logKey.Public())
	if err != nil {
		t.Fatalf("log id: %v", err)
	}
	ts := uint64(time.Now().UnixMilli())
	signed := x509SignedData(ts, certDER, nil)

	var sig []byte
	switch sigAlgo {
	case SigECDSA:
		hash, ok := hashFor(hashAlgo)
		if !ok {
			t.Fatalf("bad hash algo %d", hashAlgo)
		}
		digest := hash.New()
		digest.Write(signed)
		r, s, err := ecdsa.Sign(rand.Reader, logKey.(*ecdsa.PrivateKey), digest.Sum(nil))
		if err != nil {
			t.Fatalf("ecdsa sign: %v", err)
		}
		sig, err = asn1.Marshal(struct{ R, S *big.Int }{r, s})
		if err != nil {
			t.Fatalf("marshal ecdsa sig: %v", err)
		}
	case SigRSA:
		hash, ok := hashFor(hashAlgo)
		if !ok {
			t.Fatalf("bad hash algo %d", hashAlgo)
		}
		digest := hash.New()
		digest.Write(signed)
		sig, err = rsa.SignPKCS1v15(rand.Reader, logKey.(*rsa.PrivateKey), hash, digest.Sum(nil))
		if err != nil {
			t.Fatalf("rsa sign: %v", err)
		}
	case SigEd25519:
		sig = ed25519.Sign(logKey.(ed25519.PrivateKey), signed)
	default:
		t.Fatalf("unsupported test sig algo %d", sigAlgo)
	}

	return marshalTestSCT(logID, ts, nil, hashAlgo, sigAlgo, sig)
}

func mustPinned(t *testing.T, key crypto.PublicKey) *PinnedLog {
	t.Helper()
	log, err := NewPinnedLog(key)
	if err != nil {
		t.Fatalf("NewPinnedLog: %v", err)
	}
	return log
}

func TestPolicyAcceptsPinnedECDSA(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	logKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sct := signSCT(t, logKey, HashSHA256, SigECDSA, leafDER)

	policy, err := NewPolicy(mustPinned(t, logKey.Public()))
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	if err := policy.VerifySCTs(leafDER, [][]byte{sct}); err != nil {
		t.Fatalf("pinned SCT should pass: %v", err)
	}
	if err := policy.VerifySCT(leafDER, sct); err != nil {
		t.Fatalf("VerifySCT pinned: %v", err)
	}

	parsed, err := ParseSCT(sct)
	if err != nil {
		t.Fatalf("ParseSCT: %v", err)
	}
	if !policy.Pinned(parsed.LogID) {
		t.Fatal("Pinned() should recognize the log id")
	}
}

func TestPolicyRejectsUnpinnedLogSCT(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	pinnedKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	policy, err := NewPolicy(mustPinned(t, pinnedKey.Public()))
	if err != nil {
		t.Fatal(err)
	}

	// A valid SCT, but from a log outside the pinned set: must fail.
	foreignSCT := signSCT(t, otherKey, HashSHA256, SigECDSA, leafDER)
	if err := policy.VerifySCTs(leafDER, [][]byte{foreignSCT}); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("unpinned SCT: want ErrPolicyFailed, got %v", err)
	}
	if err := policy.VerifySCT(leafDER, foreignSCT); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("VerifySCT unpinned: want ErrPolicyFailed, got %v", err)
	}
}

func TestPolicyNoSCTs(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	policy, _ := NewPolicy()
	if err := policy.VerifySCTs(leafDER, nil); !errors.Is(err, ErrNoSCTs) {
		t.Fatalf("want ErrNoSCTs, got %v", err)
	}
	cert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.VerifyCertificate(cert); !errors.Is(err, ErrNoSCTs) {
		t.Fatalf("VerifyCertificate no SCT: want ErrNoSCTs, got %v", err)
	}
}

func TestPolicyMixedSCTs(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	pinnedKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	policy, _ := NewPolicy(mustPinned(t, pinnedKey.Public()))
	good := signSCT(t, pinnedKey, HashSHA256, SigECDSA, leafDER)
	foreign := signSCT(t, otherKey, HashSHA256, SigECDSA, leafDER)

	// An unpinned SCT plus a valid pinned SCT: pass.
	if err := policy.VerifySCTs(leafDER, [][]byte{foreign, good}); err != nil {
		t.Fatalf("mixed SCTs with a good pinned one should pass: %v", err)
	}

	// A tampered pinned SCT plus an unpinned one: fail.
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xFF
	if err := policy.VerifySCTs(leafDER, [][]byte{foreign, bad}); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("tampered pinned SCT: want ErrPolicyFailed, got %v", err)
	}
}

func TestPolicySignatureCoversCertificate(t *testing.T) {
	cert1DER, _ := makeLeafDER(t)
	cert2DER, _ := makeLeafDER(t)
	logKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	policy, _ := NewPolicy(mustPinned(t, logKey.Public()))

	sctOverCert1 := signSCT(t, logKey, HashSHA256, SigECDSA, cert1DER)
	if err := policy.VerifySCTs(cert2DER, [][]byte{sctOverCert1}); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("SCT over another certificate: want ErrPolicyFailed, got %v", err)
	}

	tampered := append([]byte(nil), sctOverCert1...)
	tampered[len(tampered)-1] ^= 0x01
	if err := policy.VerifySCTs(cert1DER, [][]byte{tampered}); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("tampered signature: want ErrPolicyFailed, got %v", err)
	}
}

func TestPolicyRSALog(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	logKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sct := signSCT(t, logKey, HashSHA256, SigRSA, leafDER)
	policy, _ := NewPolicy(mustPinned(t, logKey.Public()))
	if err := policy.VerifySCTs(leafDER, [][]byte{sct}); err != nil {
		t.Fatalf("RSA pinned SCT should pass: %v", err)
	}
}

func TestPolicyEd25519Log(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sct := signSCT(t, priv, HashNone, SigEd25519, leafDER)
	policy, _ := NewPolicy(mustPinned(t, pub))
	if err := policy.VerifySCTs(leafDER, [][]byte{sct}); err != nil {
		t.Fatalf("Ed25519 pinned SCT should pass: %v", err)
	}
}

func TestPolicyECDSAWithSHA384(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	logKey, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	sct := signSCT(t, logKey, HashSHA384, SigECDSA, leafDER)
	policy, _ := NewPolicy(mustPinned(t, logKey.Public()))
	if err := policy.VerifySCTs(leafDER, [][]byte{sct}); err != nil {
		t.Fatalf("ECDSA P-384/SHA-384 SCT should pass: %v", err)
	}
}

// validSCTHeader builds a well-formed prefix through the extensions field so
// malformed-signature cases exercise the parser deeply.
func validSCTHeader(t *testing.T, key crypto.PublicKey) []byte {
	t.Helper()
	id, err := LogID(key)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteByte(Version1)
	buf.Write(id)
	buf.Write(uint64Bytes(123456))
	buf.Write(uint16Bytes(0))
	return buf.Bytes()
}

func TestMalformedSCTs(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	logKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	policy, _ := NewPolicy(mustPinned(t, logKey.Public()))

	cases := map[string][]byte{
		"empty":               {},
		"truncated":           {0x00, 0x01, 0x02},
		"bad version":         bytes.Repeat([]byte{0xAB}, minSCTLength),
		"bad extension len":   append(validSCTHeader(t, logKey.Public()), []byte{0xFF, 0xFF, 0x00, 0x00, 0x04, 0x03, 0x00, 0x00}...),
		"sig length mismatch": append(validSCTHeader(t, logKey.Public()), []byte{0x04, 0x03, 0x00, 0x09, 0x01, 0x02}...),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := policy.VerifySCTs(leafDER, [][]byte{raw}); !errors.Is(err, ErrPolicyFailed) {
				t.Fatalf("malformed SCT %q: want ErrPolicyFailed, got %v", name, err)
			}
		})
	}

	// A malformed entry followed by a valid pinned entry still passes.
	good := signSCT(t, logKey, HashSHA256, SigECDSA, leafDER)
	if err := policy.VerifySCTs(leafDER, [][]byte{{0x00, 0x01}, good}); err != nil {
		t.Fatalf("recovery after malformed SCT should pass: %v", err)
	}
}

func TestUnsupportedAlgorithms(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	logKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	policy, _ := NewPolicy(mustPinned(t, logKey.Public()))

	id, _ := LogID(logKey.Public())
	dsaSCT := marshalTestSCT(id, 1, nil, HashSHA256, SigDSA, []byte{1, 2, 3})
	if err := policy.VerifySCTs(leafDER, [][]byte{dsaSCT}); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("DSA SCT: want ErrPolicyFailed, got %v", err)
	}

	// ECDSA with MD5 (a rejected, legacy hash) must not verify.
	md5SCT := marshalTestSCT(id, 1, nil, HashMD5, SigECDSA, []byte{1, 2, 3, 4})
	if err := policy.VerifySCTs(leafDER, [][]byte{md5SCT}); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("MD5 ECDSA SCT: want ErrPolicyFailed, got %v", err)
	}
}

func TestSCTListRoundTrip(t *testing.T) {
	leafDER, _ := makeLeafDER(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	first := signSCT(t, key, HashSHA256, SigECDSA, leafDER)
	second := signSCT(t, key, HashSHA256, SigECDSA, leafDER)

	encoded := MarshalSCTList([][]byte{first, second})
	parsed, err := ParseSCTList(encoded)
	if err != nil {
		t.Fatalf("ParseSCTList: %v", err)
	}
	if len(parsed) != 2 || !bytes.Equal(parsed[0], first) || !bytes.Equal(parsed[1], second) {
		t.Fatal("SCT list round trip mismatch")
	}

	for _, bad := range [][]byte{nil, {0x00}, {0x00, 0x05, 0x00}, {0x00, 0x02, 0x00, 0x05, 0x01}} {
		if _, err := ParseSCTList(bad); err == nil {
			t.Fatalf("malformed list %x should fail", bad)
		}
	}
}

func TestPinnedLogConstructors(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id, err := LogID(key.Public())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewPinnedLogWithID(id, key.Public()); err != nil {
		t.Fatalf("matching id should be accepted: %v", err)
	}
	wrong := append([]byte(nil), id...)
	wrong[0] ^= 0x01
	if _, err := NewPinnedLogWithID(wrong, key.Public()); err == nil {
		t.Fatal("mismatched id should be rejected")
	}
	if _, err := NewPinnedLogWithID([]byte{1, 2}, key.Public()); err == nil {
		t.Fatal("short id should be rejected")
	}

	// An unsupported key type cannot produce a log id.
	if _, err := NewPinnedLog("not-a-key"); err == nil {
		t.Fatal("unsupported key type should be rejected")
	}

	policy, err := NewPolicy(&PinnedLog{ID: []byte{1}, Key: key.Public()})
	if err == nil {
		t.Fatal("policy should reject bad log id length")
	}
	policy, _ = NewPolicy()
	if err := policy.Add(nil); err == nil {
		t.Fatal("Add(nil) should fail")
	}
}

// buildEmbeddedCertificate creates a base leaf certificate, signs SCTs over
// that base DER (the x509_entry model), and returns a second certificate that
// embeds those SCTs in its SignedCertificateTimestampList extension together
// with the base DER the SCTs were signed over. The two certificates share the
// same template so they differ only in the embedded extension.
func buildEmbeddedCertificate(t *testing.T, logKeys []ed25519.PrivateKey) (*x509.Certificate, []byte) {
	t.Helper()
	leafPub, leafKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(77),
		Subject:      pkix.Name{CommonName: "embedded.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	baseDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, leafPub, leafKey)
	if err != nil {
		t.Fatalf("base certificate: %v", err)
	}

	var realSCTs [][]byte
	for _, logKey := range logKeys {
		realSCTs = append(realSCTs, signSCT(t, logKey, HashNone, SigEd25519, baseDER))
	}

	embeddedTmpl := *tmpl
	embeddedTmpl.ExtraExtensions = []pkix.Extension{{
		Id:       OIDSCTList,
		Critical: false,
		Value:    MarshalSCTList(realSCTs),
	}}
	finalDER, err := x509.CreateCertificate(rand.Reader, &embeddedTmpl, &embeddedTmpl, leafPub, leafKey)
	if err != nil {
		t.Fatalf("embedded certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(finalDER)
	if err != nil {
		t.Fatalf("parse final certificate: %v", err)
	}
	embedded, err := EmbeddedSCTs(cert)
	if err != nil {
		t.Fatalf("EmbeddedSCTs: %v", err)
	}
	if len(embedded) != len(logKeys) {
		t.Fatalf("expected %d embedded SCTs, got %d", len(logKeys), len(embedded))
	}
	return cert, baseDER
}

func TestVerifyCertificateEmbeddedSCT(t *testing.T) {
	pinnedPub, pinnedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, foreignPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	policy, err := NewPolicy(mustPinned(t, pinnedPub))
	if err != nil {
		t.Fatal(err)
	}

	// Only an unpinned log's SCT embedded: extraction works, policy fails.
	foreignCert, baseDER := buildEmbeddedCertificate(t, []ed25519.PrivateKey{foreignPriv})
	foreignSCTs, err := EmbeddedSCTs(foreignCert)
	if err != nil {
		t.Fatalf("EmbeddedSCTs: %v", err)
	}
	if err := policy.VerifySCTs(baseDER, foreignSCTs); !errors.Is(err, ErrPolicyFailed) {
		t.Fatalf("embedded unpinned-only SCT: want ErrPolicyFailed, got %v", err)
	}

	// Pinned log SCT embedded alongside the foreign one: extraction yields both,
	// and the pinned SCT verifies over the base certificate DER.
	goodCert, goodBaseDER := buildEmbeddedCertificate(t, []ed25519.PrivateKey{foreignPriv, pinnedPriv})
	goodSCTs, err := EmbeddedSCTs(goodCert)
	if err != nil {
		t.Fatalf("EmbeddedSCTs: %v", err)
	}
	if err := policy.VerifySCTs(goodBaseDER, goodSCTs); err != nil {
		t.Fatalf("embedded pinned SCT should pass: %v", err)
	}
}

func TestLogIDIsSPKISHA256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	id, err := LogID(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id, sum[:]) {
		t.Fatal("LogID must be SHA-256 of SPKI DER")
	}
}
