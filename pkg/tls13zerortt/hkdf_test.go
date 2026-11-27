package tls13zerortt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

// TestRFC5869TestCase1 pins HKDF-Extract and HKDF-Expand to the published
// SHA-256 test vector in RFC 5869 Appendix A.1, anchoring the primitives that
// the entire TLS 1.3 key schedule is built on.
func TestRFC5869TestCase1(t *testing.T) {
	ikm := mustHex(t, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b")
	salt := mustHex(t, "000102030405060708090a0b0c")
	info := mustHex(t, "f0f1f2f3f4f5f6f7f8f9")
	wantPRK := mustHex(t,
		"077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5")
	wantOKM := mustHex(t,
		"3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf"+
			"34007208d5b887185865")

	prk := hkdfExtract(salt, ikm)
	if !bytes.Equal(prk, wantPRK) {
		t.Fatalf("HKDF-Extract mismatch:\n got %x\nwant %x", prk, wantPRK)
	}
	okm, err := hkdfExpand(prk, info, 42)
	if err != nil {
		t.Fatalf("HKDF-Expand: %v", err)
	}
	if !bytes.Equal(okm, wantOKM) {
		t.Fatalf("HKDF-Expand mismatch:\n got %x\nwant %x", okm, wantOKM)
	}
}

// TestHKDFExpandLengthRejects checks the RFC 5869 length bound.
func TestHKDFExpandLengthRejects(t *testing.T) {
	prk := make([]byte, HashLength)
	if _, err := hkdfExpand(prk, nil, 0); err == nil {
		t.Fatal("expected error for zero length")
	}
	if _, err := hkdfExpand(prk, nil, MaxHKDFExpandLength+1); err == nil {
		t.Fatal("expected error for oversize length")
	}
}

// TestHKDFExpandLabelEncoding verifies the exact HkdfLabel wire structure from
// RFC 8446 7.1: uint16 length, "tls13 " prefixed label with one-byte length,
// context with one-byte length.
func TestHKDFExpandLabelEncoding(t *testing.T) {
	secret := make([]byte, HashLength)
	context := []byte{0xde, 0xad, 0xbe, 0xef}
	label := "finished"

	var wantInfo bytes.Buffer
	wantInfo.Write([]byte{0x00, byte(HashLength)})
	fullLabel := "tls13 " + label
	wantInfo.WriteByte(byte(len(fullLabel)))
	wantInfo.WriteString(fullLabel)
	wantInfo.WriteByte(byte(len(context)))
	wantInfo.Write(context)

	got, err := hkdfExpandLabel(secret, label, context, HashLength)
	if err != nil {
		t.Fatalf("hkdfExpandLabel: %v", err)
	}
	want, err := hkdfExpand(secret, wantInfo.Bytes(), HashLength)
	if err != nil {
		t.Fatalf("hkdfExpand: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("HKDF-Expand-Label encoding differs from hand-built HkdfLabel")
	}
}

// TestKeyScheduleChain verifies early_secret -> binder_key -> finished_key
// against an independent recomputation and confirms ext/res label separation.
func TestKeyScheduleChain(t *testing.T) {
	psk := bytes.Repeat([]byte{0xAB}, HashLength)

	for _, mode := range []PSKMode{ResumptionPSK, ExternalPSK} {
		ks, err := NewKeySchedule(psk, mode)
		if err != nil {
			t.Fatalf("NewKeySchedule(%v): %v", mode, err)
		}

		// early_secret = HMAC(0, PSK).
		wantEarly := hkdfExtract(make([]byte, HashLength), psk)
		if !bytes.Equal(ks.EarlySecret, wantEarly) {
			t.Fatalf("mode %v: early secret mismatch", mode)
		}

		// binder_key = Derive-Secret(early, label, ""), context = SHA256("").
		emptyHash := sha256.Sum256(nil)
		label := mode.binderLabel()
		wantBinder, err := hkdfExpandLabel(ks.EarlySecret, label, emptyHash[:], HashLength)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ks.BinderKey, wantBinder) {
			t.Fatalf("mode %v: binder key mismatch", mode)
		}

		// finished_key = Expand-Label(binder, "finished", "", 32), empty ctx.
		wantFinished, err := hkdfExpandLabel(ks.BinderKey, "finished", nil, HashLength)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ks.FinishedKey, wantFinished) {
			t.Fatalf("mode %v: finished key mismatch", mode)
		}
	}

	res, _ := NewKeySchedule(psk, ResumptionPSK)
	ext, _ := NewKeySchedule(psk, ExternalPSK)
	if bytes.Equal(res.BinderKey, ext.BinderKey) {
		t.Fatal("ext binder and res binder keys must differ for the same PSK")
	}
	if bytes.Equal(res.FinishedKey, ext.FinishedKey) {
		t.Fatal("finished keys must differ across binder labels")
	}
}

func TestUnknownPSKModeRejected(t *testing.T) {
	if _, err := NewKeySchedule(make([]byte, 32), PSKMode(99)); err == nil {
		t.Fatal("expected error for unknown PSK mode")
	}
}
