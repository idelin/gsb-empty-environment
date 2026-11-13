package srh

import (
	"bytes"
	"errors"
	"testing"
)

func TestSignRoundTrip(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, 2,
		"2001:db8:9::a", "2001:db8:9::b", "2001:db8:9::c")

	const keyID uint32 = 1234
	key := []byte("egress-shared-secret")
	signed, err := Sign(base, keyID, key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The original packet must remain untouched.
	srhOff := ipv6HeaderLen
	if signed[srhOff+5]&hmacFlagBit == 0 {
		t.Fatal("Sign must set the HMAC flag")
	}
	if len(signed) != len(base)+hmacTLVTotalLen {
		t.Fatalf("signed length = %d, want %d", len(signed), len(base)+hmacTLVTotalLen)
	}

	keys := NewKeyring()
	if err := keys.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Admit(signed, keys); err != nil {
		t.Fatalf("signed packet rejected by Admit: %v", err)
	}

	// Sign must be byte-for-byte compatible with the reference MAC layout.
	expected := referenceMAC(t, src, signed[srhOff+4], signed[srhOff+5],
		keyID, signed, srhOff, 3, key)
	tlvStart := srhOff + srhFixedHeaderLen + 3*sidLen
	if got := signed[tlvStart+8 : tlvStart+8+hmacSHA256Len]; !bytes.Equal(got, expected) {
		t.Fatalf("digest mismatch with reference construction")
	}
}

func TestSignRejectsInvalidInputs(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, 0, "2001:db8:9::a")

	if _, err := Sign(base, 0, []byte("k")); !errors.Is(err, ErrZeroKeyID) {
		t.Fatalf("zero Key ID: want ErrZeroKeyID, got %v", err)
	}
	if _, err := Sign(base, 1, nil); err == nil {
		t.Fatal("empty key must be rejected")
	}

	signed, err := Sign(base, 1, []byte("k"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Sign(signed, 1, []byte("k")); err == nil {
		t.Fatal("re-signing an SRH that already has an HMAC TLV must fail")
	}
}
