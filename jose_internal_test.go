package netsec

import (
	"encoding/hex"
	"testing"
)

// RFC 3394 section 4.1 test vector.
func TestAESKeyUnwrapRFC3394Vector(t *testing.T) {
	kek, err := hex.DecodeString("000102030405060708090A0B0C0D0E0F")
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := hex.DecodeString("1FA68B0A8112B447AEF34BD8FB5A7B829D3E862371D2CFE5")
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := aesKeyUnwrap(kek, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	want := "00112233445566778899aabbccddeeff"
	if got := hex.EncodeToString(unwrapped); got != want {
		t.Fatalf("unwrapped = %s, want %s", got, want)
	}
}

func TestAESKeyUnwrapRejectsTampering(t *testing.T) {
	kek, _ := hex.DecodeString("000102030405060708090A0B0C0D0E0F")
	wrapped, _ := hex.DecodeString("1FA68B0A8112B447AEF34BD8FB5A7B829D3E862371D2CFE5")
	wrapped[0] ^= 0x01
	if _, err := aesKeyUnwrap(kek, wrapped); err == nil {
		t.Fatal("tampered key unwrap succeeded")
	}
}
