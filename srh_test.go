package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
)

var (
	testSource   = netip.MustParseAddr("2001:db8:1::1")
	testDest     = netip.MustParseAddr("2001:db8:2::1")
	testKey      = []byte("0123456789abcdef0123456789abcdef")
	testSegments = []netip.Addr{
		netip.MustParseAddr("2001:db8:100::1"),
		netip.MustParseAddr("2001:db8:200::1"),
		netip.MustParseAddr("2001:db8:300::1"),
	}
)

type packetSpec struct {
	src          netip.Addr
	dst          netip.Addr
	segments     []netip.Addr
	segmentsLeft uint8
	flags        uint8
	tag          uint16
	keyID        uint32
	key          []byte
	noHMAC       bool
	hmacLen      int
	reserved     uint16
	extraTLVs    []byte
	nextHeader   uint8
}

// buildPacket constructs a full IPv6 packet with an SRH.  It computes the
// HMAC independently from the package implementation.
func buildPacket(t *testing.T, spec packetSpec) []byte {
	t.Helper()

	if !spec.src.IsValid() {
		spec.src = testSource
	}
	if !spec.dst.IsValid() {
		spec.dst = testDest
	}
	if spec.segments == nil {
		spec.segments = testSegments
	}
	segmentCount := len(spec.segments)
	if segmentCount < 1 || segmentCount > 256 {
		t.Fatalf("invalid segment count %d", segmentCount)
	}
	lastEntry := segmentCount - 1
	segments := spec.segments

	if spec.key == nil {
		spec.key = testKey
	}
	if spec.hmacLen == 0 {
		spec.hmacLen = HMACSHA256Size
	}
	if spec.nextHeader == 0 {
		spec.nextHeader = 59
	}

	var tlvs []byte
	hmacOffset := -1
	if !spec.noHMAC {
		tlvs = append(tlvs, HMACTLVType, byte(6+spec.hmacLen))
		var reserved [2]byte
		binary.BigEndian.PutUint16(reserved[:], spec.reserved)
		tlvs = append(tlvs, reserved[:]...)

		var keyID [4]byte
		binary.BigEndian.PutUint32(keyID[:], spec.keyID)
		tlvs = append(tlvs, keyID[:]...)

		hmacOffset = len(tlvs)
		tlvs = append(tlvs, make([]byte, spec.hmacLen)...)
	}
	tlvs = append(tlvs, spec.extraTLVs...)

	for (8+16*segmentCount+len(tlvs))%8 != 0 {
		tlvs = append(tlvs, 0) // Pad1
	}

	totalLen := 8 + 16*segmentCount + len(tlvs)
	if totalLen > 2048 {
		t.Fatalf("SRH too large: %d", totalLen)
	}
	hdrExtLen := totalLen/8 - 1
	if hdrExtLen > 255 {
		t.Fatalf("Hdr Ext Len overflows: %d", hdrExtLen)
	}

	srh := make([]byte, totalLen)
	srh[0] = spec.nextHeader
	srh[1] = byte(hdrExtLen)
	srh[2] = SRHRoutingType
	srh[3] = spec.segmentsLeft
	srh[4] = byte(lastEntry)
	srh[5] = spec.flags
	binary.BigEndian.PutUint16(srh[6:8], spec.tag)

	for i, segment := range segments {
		a := segment.As16()
		copy(srh[8+16*i:], a[:])
	}
	copy(srh[8+16*segmentCount:], tlvs)

	if !spec.noHMAC {
		mac := hmac.New(sha256.New, spec.key)
		src := spec.src.As16()
		_, _ = mac.Write(src[:])
		_, _ = mac.Write([]byte{byte(lastEntry), spec.flags, 0, 0})
		var keyID [4]byte
		binary.BigEndian.PutUint32(keyID[:], spec.keyID)
		_, _ = mac.Write(keyID[:])
		for _, segment := range segments {
			a := segment.As16()
			_, _ = mac.Write(a[:])
		}
		digest := mac.Sum(nil)
		start := 8 + 16*segmentCount + hmacOffset
		copy(srh[start:start+spec.hmacLen], digest)
	}

	packet := make([]byte, 40+len(srh))
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], uint16(len(srh)))
	packet[6] = 43
	packet[7] = 64
	src := spec.src.As16()
	copy(packet[8:24], src[:])
	dst := spec.dst.As16()
	copy(packet[24:40], dst[:])
	copy(packet[40:], srh)
	return packet
}

func validKeys() HMACKeyFunc {
	return HMACKeys{7: testKey}.Lookup
}

func TestAdmitExternalSRHValid(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		segmentsLeft: 2,
		flags:        0x11,
		tag:          0xbeef,
		keyID:        7,
	})

	if err := AdmitExternalSRH(packet, validKeys()); err != nil {
		t.Fatalf("valid packet denied: %v", err)
	}
}

func TestAdmitExternalSRHSegmentsLeftBoundary(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		segmentsLeft: uint8(len(testSegments)), // Last Entry + 1
		keyID:        7,
	})

	if err := AdmitExternalSRH(packet, validKeys()); err != nil {
		t.Fatalf("Segments Left == Last Entry + 1 denied: %v", err)
	}
}

func TestAdmitExternalSRHModifiedSegmentList(t *testing.T) {
	packet := buildPacket(t, packetSpec{keyID: 7})
	packet[40+8+16*1+7] ^= 0x01

	err := AdmitExternalSRH(packet, validKeys())
	if !errors.Is(err, ErrInvalidHMAC) {
		t.Fatalf("got %v, want ErrInvalidHMAC", err)
	}
}

func TestAdmitExternalSRHZeroKeyID(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		keyID: 0,
		key:   testKey,
	})

	err := AdmitExternalSRH(packet, HMACKeys{0: testKey}.Lookup)
	if !errors.Is(err, ErrZeroHMACKeyID) {
		t.Fatalf("got %v, want ErrZeroHMACKeyID", err)
	}
}

func TestAdmitExternalSRHTamperedSource(t *testing.T) {
	packet := buildPacket(t, packetSpec{keyID: 7})
	packet[8] ^= 0x01

	err := AdmitExternalSRH(packet, validKeys())
	if !errors.Is(err, ErrInvalidHMAC) {
		t.Fatalf("got %v, want ErrInvalidHMAC", err)
	}
}

func TestAdmitExternalSRHSegmentsLeftBeyondLastEntry(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		segmentsLeft: uint8(len(testSegments) + 1), // Last Entry + 2
		keyID:        7,
	})

	err := AdmitExternalSRH(packet, validKeys())
	if !errors.Is(err, ErrSegmentsLeftExceedsLastEntry) {
		t.Fatalf("got %v, want ErrSegmentsLeftExceedsLastEntry", err)
	}
}

func TestAdmitExternalSRHMissingHMAC(t *testing.T) {
	packet := buildPacket(t, packetSpec{noHMAC: true})

	err := AdmitExternalSRH(packet, validKeys())
	if !errors.Is(err, ErrMissingHMAC) {
		t.Fatalf("got %v, want ErrMissingHMAC", err)
	}
}

func TestAdmitExternalSRHUnknownKeyID(t *testing.T) {
	packet := buildPacket(t, packetSpec{keyID: 8})

	err := AdmitExternalSRH(packet, HMACKeys{7: testKey}.Lookup)
	if !errors.Is(err, ErrUnknownHMACKeyID) {
		t.Fatalf("got %v, want ErrUnknownHMACKeyID", err)
	}
}

func TestAdmitExternalSRHUnsupportedHMACLength(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		keyID:   7,
		hmacLen: 16,
	})

	err := AdmitExternalSRH(packet, validKeys())
	if !errors.Is(err, ErrUnsupportedHMAC) {
		t.Fatalf("got %v, want ErrUnsupportedHMAC", err)
	}
}

func TestAdmitExternalSRHNonzeroReserved(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		keyID:    7,
		reserved: 0x8000, // D bit
	})

	err := AdmitExternalSRH(packet, validKeys())
	if !errors.Is(err, ErrUnsupportedHMAC) {
		t.Fatalf("got %v, want ErrUnsupportedHMAC", err)
	}
}

func TestAdmitExternalSRHNoSRHIsAdmitted(t *testing.T) {
	packet := make([]byte, 40)
	packet[0] = 0x60
	packet[6] = 59
	src := testSource.As16()
	copy(packet[8:24], src[:])
	dst := testDest.As16()
	copy(packet[24:40], dst[:])

	if err := AdmitExternalSRH(packet, validKeys()); err != nil {
		t.Fatalf("packet without SRH denied: %v", err)
	}
}

func TestAdmitExternalSRHAfterHopByHop(t *testing.T) {
	packet := buildPacket(t, packetSpec{keyID: 7})
	srh := packet[40:]

	hopByHop := make([]byte, 8)
	hopByHop[0] = 43
	hopByHop[1] = 0

	withHopByHop := make([]byte, 0, len(packet)+len(hopByHop))
	withHopByHop = append(withHopByHop, packet[:40]...)
	withHopByHop[6] = 0
	binary.BigEndian.PutUint16(withHopByHop[4:6], uint16(len(hopByHop)+len(srh)))
	withHopByHop = append(withHopByHop, hopByHop...)
	withHopByHop = append(withHopByHop, srh...)

	if err := AdmitExternalSRH(withHopByHop, validKeys()); err != nil {
		t.Fatalf("valid packet after Hop-by-Hop denied: %v", err)
	}
}

func TestAdmitExternalSRHHeader(t *testing.T) {
	packet := buildPacket(t, packetSpec{keyID: 7})
	if err := AdmitExternalSRHHeader(packet[40:], testSource, validKeys()); err != nil {
		t.Fatalf("valid SRH header denied: %v", err)
	}
}

func TestHMACInputLayout(t *testing.T) {
	packet := buildPacket(t, packetSpec{
		segmentsLeft: 1,
		flags:        0xa5,
		keyID:        7,
	})
	srh, err := ParseSRH(packet[40:])
	if err != nil {
		t.Fatal(err)
	}

	input := srh.HMACInput(testSource)

	var want []byte
	src := testSource.As16()
	want = append(want, src[:]...)
	want = append(want, 2, 0xa5, 0, 0)
	want = append(want, 0, 0, 0, 7)
	for _, segment := range testSegments {
		a := segment.As16()
		want = append(want, a[:]...)
	}

	if !bytes.Equal(input, want) {
		t.Fatalf("HMAC input mismatch\n got %x\nwant %x", input, want)
	}
}

func TestParseSRHRejectsSegmentListOverrun(t *testing.T) {
	header := make([]byte, 8)
	header[0] = 59
	header[1] = 0
	header[2] = SRHRoutingType
	header[4] = 1

	_, err := ParseSRH(header)
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("got %v, want ErrMalformedPacket", err)
	}
}

func TestParseSRHRejectsBadTLVLength(t *testing.T) {
	header := make([]byte, 32)
	header[0] = 59
	header[1] = 3
	header[2] = SRHRoutingType
	header[4] = 0
	header[24] = 1
	header[25] = 255

	_, err := ParseSRH(header)
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("got %v, want ErrMalformedPacket", err)
	}
}

func TestAdmitExternalSRHRejectsTruncatedPackets(t *testing.T) {
	valid := buildPacket(t, packetSpec{keyID: 7})

	for length := 0; length < 40; length++ {
		if err := AdmitExternalSRH(valid[:length], validKeys()); !errors.Is(err, ErrMalformedPacket) {
			t.Fatalf("length %d: got %v, want ErrMalformedPacket", length, err)
		}
	}
}

func TestAdmitExternalSRHRejectsNonIPv6(t *testing.T) {
	packet := make([]byte, 40)
	packet[0] = 0x40

	if err := AdmitExternalSRH(packet, validKeys()); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("got %v, want ErrMalformedPacket", err)
	}
}
