package srh

import (
	"errors"
	"testing"
)

func newSignedPacket(t *testing.T, segmentsLeft byte, segments ...string) ([]byte, *Keyring, uint32, []byte) {
	t.Helper()
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, segmentsLeft, segments...)

	const keyID uint32 = 42
	key := []byte("domain-boundary-key")
	packet := attachHMAC(t, base, ipv6HeaderLen, len(base)-ipv6HeaderLen, keyID, key)

	keys := NewKeyring()
	if err := keys.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return packet, keys, keyID, key
}

func TestAdmit_NoHMACRejected(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	packet := buildBasePacket(t, src, dst, 1, "2001:db8:9::a", "2001:db8:9::b")

	err := Admit(packet, NewKeyring())
	if !errors.Is(err, ErrNoHMAC) {
		t.Fatalf("want ErrNoHMAC, got %v", err)
	}
}

func TestAdmit_SegmentsLeftBoundary(t *testing.T) {
	segs := []string{"2001:db8:9::a", "2001:db8:9::b", "2001:db8:9::c"}

	// Last Entry = 2, so SL = 3 (= Last Entry + 1) is the largest legal value.
	packet, keys, _, _ := newSignedPacket(t, 3, segs...)
	if err := Admit(packet, keys); err != nil {
		t.Fatalf("SL = Last Entry + 1 must be accepted: %v", err)
	}

	// SL = 4 exceeds Last Entry + 1 and must be dropped before MAC use.
	packet, keys, _, _ = newSignedPacket(t, 4, segs...)
	err := Admit(packet, keys)
	if !errors.Is(err, ErrSegmentsLeft) {
		t.Fatalf("want ErrSegmentsLeft, got %v", err)
	}
}

func TestAdmit_UnknownAndRevokedKey(t *testing.T) {
	packet, _, keyID, key := newSignedPacket(t, 1, "2001:db8:9::a", "2001:db8:9::b")

	empty := NewKeyring()
	if err := Admit(packet, empty); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}

	ring := NewKeyring()
	if err := ring.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ring.Delete(keyID)
	if err := Admit(packet, ring); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("revoked key must yield ErrUnknownKey, got %v", err)
	}
}

func TestAdmit_PacketsWithoutSRH(t *testing.T) {
	// Bare IPv6 packet whose Next Header is UDP (17): no extension headers.
	packet := make([]byte, ipv6HeaderLen+8)
	packet[0] = 0x60
	packet[6] = 17
	copy(packet[8:24], mustParseIP(t, "2001:db8::1"))
	copy(packet[24:40], mustParseIP(t, "2001:db8::2"))

	if err := Admit(packet, NewKeyring()); err != nil {
		t.Fatalf("packet without SRH must be accepted: %v", err)
	}
}

func TestAdmit_MalformedInputs(t *testing.T) {
	keys := NewKeyring()

	if err := Admit(nil, keys); !errors.Is(err, ErrNotIPv6) {
		t.Fatalf("nil packet: want ErrNotIPv6, got %v", err)
	}

	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	packet := buildBasePacket(t, src, dst, 0, "2001:db8:9::a")

	// Truncate inside the SRH: Hdr Ext Len now overruns the buffer.
	truncated := append([]byte(nil), packet[:len(packet)-4]...)
	if err := Admit(truncated, keys); !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated SRH: want ErrMalformed, got %v", err)
	}

	// Claim a larger segment list than the header contains.
	bad := append([]byte(nil), packet...)
	bad[ipv6HeaderLen+4] = 5 // Last Entry = 5, but only one SID is present
	if err := Admit(bad, keys); !errors.Is(err, ErrMalformed) {
		t.Fatalf("oversized segment list: want ErrMalformed, got %v", err)
	}
}

func TestAdmit_SRHAfterOtherExtensionHeaders(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")

	// A Hop-by-Hop header (8 octets, one Pad1 option) precedes the SRH.
	base := buildBasePacket(t, src, dst, 0, "2001:db8:9::a")
	hbH := make([]byte, 8)
	hbH[0] = nextHeaderRouting
	hbH[1] = 0 // length in 8-octet units beyond the first 8
	hbH[2] = 0 // Pad1

	withExt := make([]byte, 0, len(base)+len(hbH))
	withExt = append(withExt, base[:ipv6HeaderLen]...)
	withExt[6] = nextHeaderHopByHop
	withExt = append(withExt, hbH...)
	withExt = append(withExt, base[ipv6HeaderLen:]...)

	const keyID uint32 = 5
	key := []byte("k")
	srhOff := ipv6HeaderLen + len(hbH)
	oldSRHLen := len(base) - ipv6HeaderLen
	packet := attachHMAC(t, withExt, srhOff, oldSRHLen, keyID, key)

	keys := NewKeyring()
	if err := keys.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Admit(packet, keys); err != nil {
		t.Fatalf("SRH behind Hop-by-Hop must verify: %v", err)
	}

	// Tamper the source; the walk must still locate the same SRH and fail MAC.
	packet[8] ^= 0x01
	if err := Admit(packet, keys); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("source tamper behind extension header: want ErrBadMAC, got %v", err)
	}
}

func TestKeyring_RejectsZeroIDAndEmptyKey(t *testing.T) {
	keys := NewKeyring()
	if err := keys.Add(0, []byte("k")); !errors.Is(err, ErrZeroKeyID) {
		t.Fatalf("zero Key ID: want ErrZeroKeyID, got %v", err)
	}
	if err := keys.Add(1, nil); err == nil {
		t.Fatal("empty key must be rejected")
	}
}
