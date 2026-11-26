package sshcert

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func parseTestCert(t *testing.T, ca, leaf *testKey, mutate func(*certSpec)) *Certificate {
	t.Helper()
	spec := defaultCertSpec(ca, leaf)
	if mutate != nil {
		mutate(&spec)
	}
	c, err := ParseCertificate(buildCert(t, spec))
	if err != nil {
		t.Fatalf("cert build/parse: %v", err)
	}
	return c
}

func TestKRLExplicitKeyRevocation(t *testing.T) {
	ca, leaf, other := generateTestKey(t), generateTestKey(t), generateTestKey(t)
	cert := parseTestCert(t, ca, leaf, nil)

	// PublicKeyBlob must equal the fully wrapped on-wire .pub blob
	// (outer length prefix included), matching PublicKey.Marshal().
	wantBlob := wrapString(leaf.sshBlob)
	if !bytesEqual(cert.PublicKeyBlob, wantBlob) {
		t.Fatalf("PublicKeyBlob is not the full wire blob: got %x want %x",
			cert.PublicKeyBlob, wantBlob)
	}

	b := newKRLBuilder("explicit")
	b.section(krlSectionExplicitKey, explicitKeyPayload(leaf.sshBlob))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse krl: %v", err)
	}
	if err := krl.CheckCertificate(cert); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}

	// A different leaf key under the same CA is not revoked.
	safe := parseTestCert(t, ca, other, nil)
	if err := krl.CheckCertificate(safe); err != nil {
		t.Fatalf("unrelated key wrongly revoked: %v", err)
	}
}

func TestKRLFingerprintRevocation(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	cert := parseTestCert(t, ca, leaf, nil)

	sum := sha256.Sum256(cert.PublicKeyBlob)
	b := newKRLBuilder("fp")
	b.section(krlSectionFingerprintSHA256, fingerprintPayload(sum[:]))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse krl: %v", err)
	}
	if err := krl.CheckCertificate(cert); !errors.Is(err, ErrRevoked) {
		t.Fatalf("fingerprint hit expected, got %v", err)
	}
}

func TestKRLSerialListIncludingZero(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)

	// Serial 0 must be revocable: it is a real serial, not "unset".
	zeroCert := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = 0 })
	b := newKRLBuilder("list")
	b.certSection(ca.sshBlob, krlCertSerialList, serialListPayload(0, 7, 1234))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse krl: %v", err)
	}
	if err := krl.CheckCertificate(zeroCert); !errors.Is(err, ErrRevoked) {
		t.Fatalf("serial zero must be revocable, got %v", err)
	}

	hit := parseTestCert(t, ca, leaf, nil) // serial 1234
	if err := krl.CheckCertificate(hit); !errors.Is(err, ErrRevoked) {
		t.Fatalf("serial 1234 should be revoked: %v", err)
	}

	safe := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = 1235 })
	if err := krl.CheckCertificate(safe); err != nil {
		t.Fatalf("serial 1235 should pass: %v", err)
	}
}

func TestKRLSerialRange(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	b := newKRLBuilder("range")
	b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(100, 200))
	// Open-ended range (max 0) covers everything from 1000 up.
	b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(1000, 0))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse krl: %v", err)
	}

	cases := []struct {
		serial  uint64
		revoked bool
	}{
		{99, false},
		{100, true},
		{150, true},
		{200, true},
		{201, false},
		{999, false},
		{1000, true},
		{1 << 40, true},
	}
	for _, tc := range cases {
		cert := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = tc.serial })
		err := krl.CheckCertificate(cert)
		if tc.revoked && !errors.Is(err, ErrRevoked) {
			t.Fatalf("serial %d should be revoked, got %v", tc.serial, err)
		}
		if !tc.revoked && err != nil {
			t.Fatalf("serial %d should pass, got %v", tc.serial, err)
		}
	}
}

func TestKRLSerialBitmap(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	// Two-byte big-endian mpint covering serials 64..79. Bit N maps to the
	// LSB-side of the magnitude: bit 0 -> last byte bit0 (serial 64),
	// bit 9 -> first byte bit1 (serial 73).
	bitmap := make([]byte, 2)
	bitmap[1] |= 1 << 0 // serial 64
	bitmap[0] |= 1 << 1 // serial 73

	b := newKRLBuilder("bitmap")
	b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(64, bitmap))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse krl: %v", err)
	}

	for _, serial := range []uint64{64, 73} {
		cert := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = serial })
		if err := krl.CheckCertificate(cert); !errors.Is(err, ErrRevoked) {
			t.Fatalf("serial %d bitmap bit should be set: %v", serial, err)
		}
	}
	for _, serial := range []uint64{65, 72, 74, 63, 80} {
		cert := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = serial })
		if err := krl.CheckCertificate(cert); err != nil {
			t.Fatalf("serial %d should pass bitmap: %v", serial, err)
		}
	}
}

func TestKRLKeyID(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)

	b := newKRLBuilder("keyid")
	b.certSection(ca.sshBlob, krlCertKeyID, keyIDPayload("build-host-01"))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse krl: %v", err)
	}
	cert := parseTestCert(t, ca, leaf, nil) // key id build-host-01
	if err := krl.CheckCertificate(cert); !errors.Is(err, ErrRevoked) {
		t.Fatalf("key id hit expected: %v", err)
	}

	safe := parseTestCert(t, ca, leaf, func(s *certSpec) { s.keyID = "other-host" })
	if err := krl.CheckCertificate(safe); err != nil {
		t.Fatalf("different key id wrongly revoked: %v", err)
	}

	// Wildcard empty key id revokes every certificate of the CA.
	wb := newKRLBuilder("wildcard")
	wb.certSection(ca.sshBlob, krlCertKeyID, keyIDPayload(""))
	wildKRL, err := ParseKRL(wb.bytes(true))
	if err != nil {
		t.Fatalf("parse wildcard krl: %v", err)
	}
	if err := wildKRL.CheckCertificate(safe); !errors.Is(err, ErrRevoked) {
		t.Fatalf("wildcard key id should revoke all: %v", err)
	}
}

func TestKRLMultiCAScoping(t *testing.T) {
	ca1, ca2, leaf := generateTestKey(t), generateTestKey(t), generateTestKey(t)

	// CA blobs must be presented in ascending order in the KRL.
	first, second := ca1.sshBlob, ca2.sshBlob
	if !bytesLess(first, second) {
		first, second = second, first
	}
	b := newKRLBuilder("multi-ca")
	b.certSection(first, krlCertSerialList, serialListPayload(1234))
	b.certSection(second, krlCertSerialList, serialListPayload(1234))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse multi-ca krl: %v", err)
	}

	underCA1 := parseTestCert(t, ca1, leaf, nil)
	underCA2 := parseTestCert(t, ca2, leaf, nil)
	if err := krl.CheckCertificate(underCA1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("ca1 cert should be revoked: %v", err)
	}
	if err := krl.CheckCertificate(underCA2); !errors.Is(err, ErrRevoked) {
		t.Fatalf("ca2 cert should be revoked: %v", err)
	}

	unknownCA := generateTestKey(t)
	underUnknown := parseTestCert(t, unknownCA, leaf, nil)
	if err := krl.CheckCertificate(underUnknown); err != nil {
		t.Fatalf("cert under unknown CA must not match: %v", err)
	}
}

func TestVerifyRejectsRevokedEndToEnd(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	cert := parseTestCert(t, ca, leaf, nil)

	b := newKRLBuilder("e2e")
	b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(1, 0))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(cert, VerifyOptions{
		ExpectedType:    CertTypeUser,
		CAPublicKeyBlob: ca.sshBlob,
		Principal:       "alice",
		Now:             1500,
		KRL:             krl,
	})
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("Verify must reject revoked credential, got %v", err)
	}
}

func TestKRLWildcardCA(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	cert := parseTestCert(t, ca, leaf, nil)

	// An empty CA blob applies the key-id revocation to every CA.
	b := newKRLBuilder("wildcard-ca")
	b.certSection(nil, krlCertKeyID, keyIDPayload("build-host-01"))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse wildcard-ca krl: %v", err)
	}
	if err := krl.CheckCertificate(cert); !errors.Is(err, ErrRevoked) {
		t.Fatalf("wildcard CA key-id should revoke: %v", err)
	}

	// Non-matching key id under wildcard CA still passes.
	safe := parseTestCert(t, ca, leaf, func(s *certSpec) { s.keyID = "elsewhere" })
	if err := krl.CheckCertificate(safe); err != nil {
		t.Fatalf("non-matching key id under wildcard CA revoked: %v", err)
	}
}

func TestKRLSHA1AndSignatureSectionsTolerated(t *testing.T) {
	// Real-world KRLs may carry a legacy SHA-1 fingerprint section (type 3)
	// or a detached signature section (type 4). They must parse without
	// being mistaken for SHA-256 data.
	var w wireWriter
	w.bytes([]byte(krlMagic))
	w.uint32(krlVersionMajor)
	w.uint64(1)
	w.string(nil)

	var sha1Sec wireWriter
	sha1Sec.byte(krlSectionFingerprintSHA1)
	sha1Sec.uint32(20)
	sha1Sec.bytes(make([]byte, 20))
	w.bytes(sha1Sec.buf)

	var sigSec wireWriter
	sigSec.byte(krlSectionSignature)
	sigSec.uint32(4)
	sigSec.bytes([]byte{0x01, 0x02, 0x03, 0x04})
	w.bytes(sigSec.buf)

	var sha256Sec wireWriter
	sha256Sec.byte(krlSectionFingerprintSHA256)
	sha256Sec.uint32(32)
	sha256Sec.bytes(make([]byte, 32))
	w.bytes(sha256Sec.buf)

	w.byte(krlSectionNull)
	w.uint32(0)

	krl, err := ParseKRL(w.buf)
	if err != nil {
		t.Fatalf("multi-section parse: %v", err)
	}
	if len(krl.fingerprints) != 1 {
		t.Fatalf("only SHA-256 records should be indexed, got %d", len(krl.fingerprints))
	}

	// Ragged SHA-1 width must still be rejected structurally.
	var head wireWriter
	head.bytes([]byte(krlMagic))
	head.uint32(krlVersionMajor)
	head.uint64(1)
	head.string(nil)
	head.byte(krlSectionFingerprintSHA1)
	head.uint32(21) // not a multiple of 20
	head.bytes(make([]byte, 21))
	if _, err := ParseKRL(head.buf); !errors.Is(err, ErrKRLBadFingerprint) {
		t.Fatalf("want ErrKRLBadFingerprint for ragged SHA1, got %v", err)
	}
}

func TestKRLSerialBitmapMpintHighBit(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	// A magnitude whose top byte has the high bit set requires a leading
	// mpint sign byte (0x00); the verifier must skip it when indexing.
	bitmap := append([]byte{0x00}, []byte{0x80, 0x00}...)
	b := newKRLBuilder("mpint-sign")
	b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(0, bitmap))
	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("parse canonical high-bit mpint: %v", err)
	}
	// Magnitude bit 15 set => serial 15 revoked.
	hit := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = 15 })
	if err := krl.CheckCertificate(hit); !errors.Is(err, ErrRevoked) {
		t.Fatalf("serial 15 should be revoked via high-bit mpint: %v", err)
	}
	safe := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = 14 })
	if err := krl.CheckCertificate(safe); err != nil {
		t.Fatalf("serial 14 should pass: %v", err)
	}
}

// TestKRLFullRealWorldLayout builds one KRL exercising every section and
// subsection in a single file, in the exact on-disk order ssh-keygen emits,
// then asserts each revocation path independently.
func TestKRLFullRealWorldLayout(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)

	b := newKRLBuilder("full-layout")

	// CERTIFICATES (section 1): one CA group with all four subsection types
	// in ascending order 0x20, 0x21, 0x22, 0x23.
	b.certSection(ca.sshBlob, krlCertSerialList, serialListPayload(42))
	b.certSection(ca.sshBlob, krlCertSerialRange, serialRangePayload(1000, 2000))
	b.certSection(ca.sshBlob, krlCertSerialBitmap, bitmapPayload(5000, []byte{0x01}))
	b.certSection(ca.sshBlob, krlCertKeyID, keyIDPayload("retired-host"))

	// EXPLICIT_KEY (section 2).
	b.section(krlSectionExplicitKey, explicitKeyPayload(leaf.sshBlob))

	// FINGERPRINT_SHA256 (section 5).
	sum := sha256.Sum256(leaf.sshBlob)
	b.section(krlSectionFingerprintSHA256, fingerprintPayload(sum[:]))

	krl, err := ParseKRL(b.bytes(true))
	if err != nil {
		t.Fatalf("full-layout parse: %v", err)
	}

	if krl.Version() != 1 || krl.Comment() != "full-layout" {
		t.Fatalf("header fields wrong: %+v", krl)
	}

	revokedSerials := []uint64{42, 1000, 1500, 2000, 5000}
	for _, serial := range revokedSerials {
		cert := parseTestCert(t, ca, leaf, func(s *certSpec) { s.serial = serial })
		if err := krl.CheckCertificate(cert); !errors.Is(err, ErrRevoked) {
			t.Fatalf("serial %d must be revoked: %v", serial, err)
		}
	}

	retiredID := parseTestCert(t, ca, leaf, func(s *certSpec) {
		s.serial = 999999
		s.keyID = "retired-host"
	})
	if err := krl.CheckCertificate(retiredID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("retired key id must be revoked: %v", err)
	}

	// A cert that matches none of the mechanisms must pass.
	clean := parseTestCert(t, ca, leaf, func(s *certSpec) {
		s.serial = 7777
		s.keyID = "fresh-host"
	})
	otherLeaf := generateTestKey(t)
	clean = parseTestCert(t, ca, otherLeaf, func(s *certSpec) {
		s.serial = 7777
		s.keyID = "fresh-host"
	})
	if err := krl.CheckCertificate(clean); err != nil {
		t.Fatalf("unrelated cert wrongly revoked: %v", err)
	}
}
