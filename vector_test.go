package netsec

import (
	"encoding/hex"
	"testing"
)

func TestKnownStrKeyVector(t *testing.T) {
	// Canonical Stellar vector: the all-zero ed25519 key encodes to this
	// G-address under StrKey (version 0x30 + CRC16-XModem).
	pub, _ := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000000")
	addr, err := EncodeAccountID(pub)
	if err != nil {
		t.Fatal(err)
	}
	const want = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABZKY"
	if addr != want {
		t.Fatalf("all-zero key: got %s want %s", addr, want)
	}
	if _, err := DecodeAccountID(want); err != nil {
		t.Fatalf("decode canonical vector: %v", err)
	}
}
