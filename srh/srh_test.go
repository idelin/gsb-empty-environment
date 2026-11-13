package srh

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

// mustParseIP parses s or fails the test.
func mustParseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s).To16()
	if ip == nil {
		t.Fatalf("bad test address %q", s)
	}
	return ip
}

// buildBasePacket assembles an IPv6 packet with an SRH holding segments and
// no TLVs. The SRH layout follows RFC 8754 Section 2.
func buildBasePacket(t *testing.T, src, dst net.IP, segmentsLeft byte, segments ...string) []byte {
	t.Helper()
	if len(segments) > 256 {
		t.Fatal("too many segments")
	}
	lastEntry := byte(len(segments) - 1)
	srhLen := srhFixedHeaderLen + len(segments)*sidLen

	packet := make([]byte, ipv6HeaderLen+srhLen)
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], uint16(srhLen)) // payload length
	packet[6] = nextHeaderRouting
	packet[7] = 64 // hop limit
	copy(packet[8:24], src)
	copy(packet[24:40], dst)

	srh := packet[ipv6HeaderLen:]
	srh[0] = 59 // Next Header: no next header
	srh[1] = byte((srhLen - srhFixedHeaderLen) / 8)
	srh[2] = routingTypeSRH
	srh[3] = segmentsLeft
	srh[4] = lastEntry
	srh[5] = 0
	srh[6] = 0
	srh[7] = 0
	for i, seg := range segments {
		copy(srh[srhFixedHeaderLen+i*sidLen:], mustParseIP(t, seg))
	}
	return packet
}

// referenceMAC computes HMAC-SHA-256 independently of the production code,
// following the exact input layout required by the admission policy.
func referenceMAC(t *testing.T, src net.IP, lastEntry, flags byte, keyID uint32, packet []byte, srhOff, segCount int, key []byte) []byte {
	t.Helper()
	var input bytes.Buffer
	input.Write(src.To16())
	input.WriteByte(lastEntry)
	input.WriteByte(flags)
	input.Write([]byte{0, 0})
	var keyIDBytes [4]byte
	binary.BigEndian.PutUint32(keyIDBytes[:], keyID)
	input.Write(keyIDBytes[:])
	segStart := srhOff + srhFixedHeaderLen
	input.Write(packet[segStart : segStart+segCount*sidLen])

	mac := hmac.New(sha256.New, key)
	mac.Write(input.Bytes())
	return mac.Sum(nil)
}

// attachHMAC appends an HMAC TLV using the test's own reference MAC rather
// than the production Sign helper.
func attachHMAC(t *testing.T, packet []byte, srhOff, oldSRHLen int, keyID uint32, key []byte) []byte {
	t.Helper()
	src := append(net.IP(nil), packet[8:24]...)
	lastEntry := packet[srhOff+4]
	flags := packet[srhOff+5] | hmacFlagBit
	segCount := int(lastEntry) + 1

	out := make([]byte, len(packet)+hmacTLVTotalLen)
	copy(out, packet)
	out[srhOff+1] += byte(hmacTLVTotalLen / 8)
	out[srhOff+5] |= hmacFlagBit
	digest := referenceMAC(t, src, lastEntry, flags, keyID, out, srhOff, segCount, key)
	tlv := out[srhOff+oldSRHLen:]
	tlv[0] = tlvTypeHMAC
	tlv[1] = hmacTLVValueLen
	binary.BigEndian.PutUint32(tlv[4:8], keyID)
	copy(tlv[8:8+hmacSHA256Len], digest)
	return out
}

func TestAdmit_ValidPacket(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, 2,
		"2001:db8:9::a", "2001:db8:9::b", "2001:db8:9::c")

	const keyID uint32 = 777
	key := []byte("domain-boundary-shared-secret")
	packet := attachHMAC(t, base, ipv6HeaderLen, len(base)-ipv6HeaderLen, keyID, key)

	keys := NewKeyring()
	if err := keys.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Admit(packet, keys); err != nil {
		t.Fatalf("valid packet rejected: %v", err)
	}
}

func TestAdmit_SegmentListTampered(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, 1,
		"2001:db8:9::a", "2001:db8:9::b")

	const keyID uint32 = 42
	key := []byte("k1")
	packet := attachHMAC(t, base, ipv6HeaderLen, len(base)-ipv6HeaderLen, keyID, key)

	// Flip an octet inside the signed segment list.
	packet[ipv6HeaderLen+srhFixedHeaderLen+3] ^= 0xff

	keys := NewKeyring()
	if err := keys.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	err := Admit(packet, keys)
	if !errors.Is(err, ErrBadMAC) {
		t.Fatalf("want ErrBadMAC for tampered segment list, got %v", err)
	}
}

func TestAdmit_ZeroKeyID(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, 1, "2001:db8:9::a")

	key := []byte("k1")
	// Key ID 0 is rejected by the key ring too, so build the TLV directly
	// with a well-formed digest under key ID 0.
	packet := attachHMAC(t, base, ipv6HeaderLen, len(base)-ipv6HeaderLen, 0, key)

	keys := NewKeyring()
	err := Admit(packet, keys)
	if !errors.Is(err, ErrZeroKeyID) {
		t.Fatalf("want ErrZeroKeyID, got %v", err)
	}
}

func TestAdmit_SourceAddressTampered(t *testing.T) {
	src := mustParseIP(t, "2001:db8::1")
	dst := mustParseIP(t, "2001:db8:1::1")
	base := buildBasePacket(t, src, dst, 0, "2001:db8:9::a")

	const keyID uint32 = 9
	key := []byte("k1")
	packet := attachHMAC(t, base, ipv6HeaderLen, len(base)-ipv6HeaderLen, keyID, key)

	// Overwrite the IPv6 source after the digest was computed.
	packet[8] = 0x20
	packet[15] = 0xab

	keys := NewKeyring()
	if err := keys.Add(keyID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	err := Admit(packet, keys)
	if !errors.Is(err, ErrBadMAC) {
		t.Fatalf("want ErrBadMAC for tampered source address, got %v", err)
	}
}
