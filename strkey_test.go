package netsec

import "testing"

func TestStrKeyRoundTrip(t *testing.T) {
	_, pub := mustKey(t, 21)

	addr, err := EncodeAccountID(pub)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(addr) != 56 || addr[0] != 'G' {
		t.Fatalf("unexpected address shape: %q len=%d", addr, len(addr))
	}

	decoded, err := DecodeAccountID(addr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != string(pub) {
		t.Fatal("round trip changed the public key")
	}
}

func TestStrKeyChecksumAndVersion(t *testing.T) {
	_, pub := mustKey(t, 22)
	addr, err := EncodeAccountID(pub)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Flip one payload character; the CRC check must reject it.
	tampered := "G" + addr[1:]
	if addr[1] == 'A' {
		tampered = "G" + "B" + addr[2:]
	} else {
		tampered = "G" + "A" + addr[2:]
	}
	if _, err := DecodeAccountID(tampered); err == nil {
		t.Fatal("checksum must reject a tampered address")
	}

	// Wrong length and non-alphabet characters are rejected.
	if _, err := DecodeAccountID(addr + "AAA"); err == nil {
		t.Fatal("oversized address must be rejected")
	}
	if _, err := DecodeAccountID("0" + addr[1:]); err == nil {
		t.Fatal("illegal alphabet character must be rejected")
	}

	if _, err := EncodeAccountID(make([]byte, 31)); err == nil {
		t.Fatal("31-byte key must be rejected")
	}
}
