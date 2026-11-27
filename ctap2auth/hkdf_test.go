package ctap2auth

import (
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture: %v", err)
	}
	return b
}

// RFC 5869 Test Case 1 (SHA-256, hash-length IKM/salt/info, L=42).
func TestHKDF_RFC5869Case1(t *testing.T) {
	ikm := mustHex(t, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b")
	salt := mustHex(t, "000102030405060708090a0b0c")
	info := mustHex(t, "f0f1f2f3f4f5f6f7f8f9")
	want := mustHex(t, "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")

	got, err := hkdfSHA256(ikm, salt, info, 42)
	if err != nil {
		t.Fatalf("hkdf: %v", err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("HKDF mismatch\n got=%x\nwant=%x", got, want)
	}
}

// RFC 5869 Test Case 3: empty salt must be replaced with a HashLen zero salt.
func TestHKDF_ZeroSaltDefault(t *testing.T) {
	ikm := mustHex(t, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b")
	want := mustHex(t, "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8")

	got, err := hkdfSHA256(ikm, nil, nil, 42)
	if err != nil {
		t.Fatalf("hkdf: %v", err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("zero-salt HKDF mismatch\n got=%x\nwant=%x", got, want)
	}
}
