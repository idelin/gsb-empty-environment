package netsec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// refChecksum is an independent, byte-at-a-time RFC 1071 implementation used
// only by the tests; the production code sums 16-bit words instead.
func refChecksum(chunks ...[]byte) uint16 {
	var acc uint32
	var word uint16
	haveHigh := false
	flush := func() {
		acc += uint32(word)
		for acc > 0xffff {
			acc = (acc & 0xffff) + (acc >> 16)
		}
	}
	for _, chunk := range chunks {
		for _, b := range chunk {
			if !haveHigh {
				word = uint16(b) << 8
				haveHigh = true
				continue
			}
			word |= uint16(b)
			flush()
			haveHigh = false
		}
	}
	if haveHigh {
		flush()
	}
	return ^uint16(acc)
}

func refPseudoHeader(srcIP, dstIP []byte, tcpLen int) []byte {
	pseudo := make([]byte, 12)
	copy(pseudo[0:4], srcIP)
	copy(pseudo[4:8], dstIP)
	pseudo[9] = protoTCP
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(tcpLen))
	return pseudo
}

// buildPacket assembles a complete IPv4/TCP datagram from explicit parts and
// fills both checksums with the reference implementation. ipOpts and tcpOpts
// must already be multiples of 4 bytes.
func buildPacket(t *testing.T, ipOpts, tcpOpts, payload []byte) []byte {
	t.Helper()
	if len(ipOpts)%4 != 0 || len(tcpOpts)%4 != 0 {
		t.Fatalf("option areas must be 4-byte aligned: ip=%d tcp=%d", len(ipOpts), len(tcpOpts))
	}
	ipHeaderLen := ipv4HeaderMinLen + len(ipOpts)
	tcpHeaderLen := tcpHeaderMinLen + len(tcpOpts)
	totalLen := ipHeaderLen + tcpHeaderLen + len(payload)

	b := make([]byte, totalLen)
	b[0] = 0x40 | byte(ipHeaderLen/4)
	b[1] = 0x2c
	binary.BigEndian.PutUint16(b[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(b[4:6], 0x1a2b)
	b[8] = 64
	b[9] = protoTCP
	copy(b[12:16], []byte{10, 1, 2, 3})
	copy(b[16:20], []byte{192, 168, 9, 10})
	copy(b[20:], ipOpts)

	tcp := ipHeaderLen
	binary.BigEndian.PutUint16(b[tcp+0:tcp+2], 40123)
	binary.BigEndian.PutUint16(b[tcp+2:tcp+4], 8443)
	binary.BigEndian.PutUint32(b[tcp+4:tcp+8], 0x01020304)
	binary.BigEndian.PutUint32(b[tcp+8:tcp+12], 0x05060708)
	b[tcp+12] = byte(tcpHeaderLen/4)<<4 | 0x01 // NS bit must survive the rewrite
	b[tcp+13] = 0x18                           // PSH + ACK
	binary.BigEndian.PutUint16(b[tcp+14:tcp+16], 32768)
	copy(b[tcp+tcpHeaderMinLen:], tcpOpts)
	copy(b[tcp+tcpHeaderLen:], payload)

	binary.BigEndian.PutUint16(b[10:12], refChecksum(b[:ipHeaderLen]))
	tcpSegment := b[tcp:]
	binary.BigEndian.PutUint16(
		b[tcp+16:tcp+18],
		refChecksum(refPseudoHeader(b[12:16], b[16:20], len(tcpSegment)), tcpSegment),
	)
	return b
}

// assertByteSnapshot reports every differing index rather than only the first.
func assertByteSnapshot(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length = %d, want %d", name, len(got), len(want))
	}
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			t.Errorf("%s: byte %d = 0x%02x, want 0x%02x", name, i, got[i], want[i])
		}
	}
}

// padEOL appends EOL bytes until the option area is 4-byte aligned.
func padEOL(opts []byte) []byte {
	for len(opts)%4 != 0 {
		opts = append(opts, 0x00)
	}
	return opts
}

// expectRewritten builds the exact expected datagram independently and checks
// the production result byte for byte, including both recomputed checksums.
func expectRewritten(t *testing.T, original, ipOpts, keptOpts, payload []byte) {
	t.Helper()
	ipHeaderLen := ipv4HeaderMinLen + len(ipOpts)

	want := buildPacket(t, ipOpts, keptOpts, payload)
	// buildPacket already fills checksums with the reference implementation.

	in := bytes.Clone(original)
	n, err := RewriteIPv4TCPPacket(in, NewOptionSet(2, 3, 4, 8))
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	if n != len(want) {
		t.Fatalf("returned length = %d, want %d", n, len(want))
	}
	assertByteSnapshot(t, "rewritten packet", in[:n], want)

	// Every byte outside the new datagram must be untouched.
	if len(in) > n && !bytes.Equal(in[n:], original[n:]) {
		t.Errorf("trailing bytes beyond new datagram were modified")
	}

	// Cross-check the checksums once more with the independent implementation.
	if got := refChecksum(in[:ipHeaderLen]); got != 0 {
		t.Errorf("reference IPv4 header checksum = 0x%04x, want 0", got)
	}
	segment := in[ipHeaderLen:n]
	if got := refChecksum(refPseudoHeader(in[12:16], in[16:20], len(segment)), segment); got != 0 {
		t.Errorf("reference TCP checksum = 0x%04x, want 0", got)
	}

	// The payload must survive verbatim, including an odd final byte.
	newTCPHeaderLen := int(in[ipHeaderLen+12]>>4) * 4
	gotPayload := in[ipHeaderLen+newTCPHeaderLen : n]
	if !bytes.Equal(gotPayload, payload) {
		t.Errorf("payload changed: got %v, want %v", gotPayload, payload)
	}
}

// TestMaxHeaderOddPayload exercises the maximum IPv4 and TCP headers (60
// bytes each), an odd-length payload and every option-rewriting rule at once.
func TestMaxHeaderOddPayload(t *testing.T) {
	ipOpts := bytes.Repeat([]byte{0x88, 0x04, 0xab, 0xcd}, 10) // 40 bytes -> IHL 15
	tcpOpts := []byte{
		0x01,                   // NOP, preserved
		0x02, 0x04, 0x05, 0xb4, // MSS, allowed
		0x01,             // NOP, preserved
		0x03, 0x03, 0x07, // window scale, allowed
		0x01,       // NOP
		0x04, 0x02, // SACK permitted, allowed
		0x05, 0x0a, 1, 2, 3, 4, 5, 6, 7, 8, // SACK, removed
		0x08, 0x0a, 0, 0, 0, 1, 0, 0, 0, 2, // timestamps, allowed
		0x1d, 0x03, 0x01, // experimental option, removed
		0x1e, 0x02, // experimental option, removed
	}
	tcpOpts = padEOL(tcpOpts)
	if len(tcpOpts) != 40 {
		t.Fatalf("test setup: tcpOpts = %d bytes, want 40", len(tcpOpts))
	}
	payload := []byte{0xde, 0xad, 0xbe, 0xef, 0x77} // 5 bytes, odd

	packet := buildPacket(t, ipOpts, tcpOpts, payload)

	keptOpts := []byte{
		0x01,
		0x02, 0x04, 0x05, 0xb4,
		0x01,
		0x03, 0x03, 0x07,
		0x01,
		0x04, 0x02,
		0x08, 0x0a, 0, 0, 0, 1, 0, 0, 0, 2,
	}
	keptOpts = padEOL(keptOpts)
	if len(keptOpts) != 24 {
		t.Fatalf("test setup: keptOpts = %d bytes, want 24", len(keptOpts))
	}
	expectRewritten(t, packet, ipOpts, keptOpts, payload)
}

// TestMinimalHeaderNoOptions covers the no-op case: a 20-byte TCP header with
// no option area is preserved exactly, checksums included.
func TestMinimalHeaderNoOptions(t *testing.T) {
	payload := []byte("hello")
	packet := buildPacket(t, nil, nil, payload)
	expectRewritten(t, packet, nil, nil, payload)
}

// TestRemoveEverythingWithNopsAndEOL checks NOP retention and EOL padding
// against a minimal IPv4 header and an odd payload.
func TestRemoveEverythingWithNopsAndEOL(t *testing.T) {
	tcpOpts := []byte{
		0x01,       // NOP kept
		0x13, 0x02, // experimental, removed
		0x01,                   // NOP kept
		0x05, 0x06, 1, 2, 3, 4, // SACK, removed
	}
	tcpOpts = padEOL(tcpOpts)
	payload := []byte{0x01, 0x02, 0x03}
	packet := buildPacket(t, nil, tcpOpts, payload)

	keptOpts := padEOL([]byte{0x01, 0x01}) // NOP, NOP then EOL padding
	expectRewritten(t, packet, nil, keptOpts, payload)
}

// TestEOLEarlyTruncation verifies that bytes after EOL are treated as padding
// and dropped even when they look like options.
func TestEOLEarlyTruncation(t *testing.T) {
	tcpOpts := []byte{
		0x02, 0x04, 0x11, 0x22, // MSS, allowed
		0x00,                   // EOL
		0x01, 0x03, 0x03, 0x09, // junk after EOL: ignored entirely
		0x77, 0x77, 0x77,
	}
	if len(tcpOpts) != 12 {
		t.Fatalf("test setup: tcpOpts = %d bytes", len(tcpOpts))
	}
	payload := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x99} // odd
	packet := buildPacket(t, nil, tcpOpts, payload)

	keptOpts := []byte{0x02, 0x04, 0x11, 0x22} // already aligned, no padding
	expectRewritten(t, packet, nil, keptOpts, payload)
}

// TestNoEOLExactFit covers an option area whose options end exactly on the
// boundary without any EOL; the rewriter supplies the padding itself.
func TestNoEOLExactFit(t *testing.T) {
	tcpOpts := []byte{
		0x02, 0x04, 0x11, 0x22, // MSS
		0x04, 0x02, 0x13, 0x02, // SACK permitted + experimental
	}
	payload := []byte{9} // odd payload
	packet := buildPacket(t, nil, tcpOpts, payload)

	keptOpts := []byte{
		0x02, 0x04, 0x11, 0x22,
		0x04, 0x02,
		0x00, 0x00,
	}
	expectRewritten(t, packet, nil, keptOpts, payload)
}

// TestEndsOnNopWithoutEOL walks the option loop to its natural exit: the
// final byte is a NOP and the area contains no EOL at all.
func TestEndsOnNopWithoutEOL(t *testing.T) {
	tcpOpts := []byte{
		0x02, 0x04, 0x11, 0x22, // MSS
		0x04, 0x02, // SACK permitted
		0x01, // trailing NOP
		0x01, // trailing NOP on the area's final byte
	}
	payload := []byte{9, 8, 7} // odd payload
	packet := buildPacket(t, nil, tcpOpts, payload)

	keptOpts := []byte{
		0x02, 0x04, 0x11, 0x22,
		0x04, 0x02,
		0x01, 0x01,
	}
	expectRewritten(t, packet, nil, keptOpts, payload)
}

// TestEmptyOptionArea drives filterOptions with no option bytes at all
// (TCP data offset 5), covering the walk that immediately falls through.
func TestEmptyOptionArea(t *testing.T) {
	packet := buildPacket(t, nil, nil, []byte{0x7f, 0x80, 0x81})
	kept, err := filterOptions(packet[20:40], NewOptionSet(2))
	if err != nil {
		t.Fatalf("filterOptions failed: %v", err)
	}
	if len(kept) != 0 {
		t.Fatalf("kept = % x, want empty", kept)
	}
}

// TestTrailerPreserved makes sure link-layer-style bytes outside the IPv4
// total length survive the in-place rewrite.
func TestTrailerPreserved(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5}
	packet := buildPacket(t, nil, []byte{0x01, 0x01, 0x01, 0x01}, payload)
	trailer := []byte{0xfe, 0xed, 0xfa, 0xce}
	packet = append(packet, trailer...)

	in := bytes.Clone(packet)
	n, err := RewriteIPv4TCPPacket(in, NewOptionSet(2))
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	wantLen := len(packet) - len(trailer) // NOP-only options are all retained
	if n != wantLen {
		t.Fatalf("length = %d, want %d", n, wantLen)
	}
	if !bytes.Equal(in[n:n+len(trailer)], trailer) {
		t.Errorf("trailer corrupted: % x", in[n:])
	}
	// Full expected datagram, independently built and checksummed.
	want := buildPacket(t, nil, []byte{0x01, 0x01, 0x01, 0x01}, payload)
	assertByteSnapshot(t, "datagram", in[:n], want)
}
