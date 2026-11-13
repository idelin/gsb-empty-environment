package netsec

import (
	"bytes"
	"crypto/elliptic"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestHKDFSHA256RFC5869Case1(t *testing.T) {
	ikm := bytes.Repeat([]byte{0x0b}, 22)
	salt := mustHex(t, "000102030405060708090a0b0c")
	info := mustHex(t, "f0f1f2f3f4f5f6f7f8f9")
	want := mustHex(t, "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")

	got, err := HKDFSHA256(ikm, salt, info, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("HKDF = %x, want %x", got, want)
	}
}

func TestDeriveKeyScheduleRequiresCanonicalSharedPoint(t *testing.T) {
	ephemeral, err := EphemeralFromScalar([]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := ephemeral.SharedPoint(ephemeral.PublicBytes())
	if err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	schedule, err := DeriveKeySchedule(shared, digest)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.SendA == schedule.SendB || schedule.SendA == schedule.Confirm || schedule.SendB == schedule.Confirm {
		t.Fatal("HKDF info labels did not separate the keys")
	}

	x, y, _, err := ParseP256Point(shared)
	if err != nil {
		t.Fatal(err)
	}
	uncompressed := elliptic.Marshal(elliptic.P256(), x, y)
	if _, err := DeriveKeySchedule(uncompressed, digest); err == nil {
		t.Fatal("non-canonical shared point was accepted")
	}
	if _, err := DeriveKeySchedule(shared, digest[:31]); err == nil {
		t.Fatal("short transcript digest was accepted")
	}
}
