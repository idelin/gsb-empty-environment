package ndadmit

import "net/netip"

// be16/be32/be128 decode big-endian fixed-width integers. Callers guarantee
// the slices are long enough after the fixed-header length checks.
func be16(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func be128(b []byte) [16]byte {
	var out [16]byte
	copy(out[:], b[:16])
	return out
}

// checksumValid reports whether the ICMPv6 checksum in packet verifies. The
// checksum is computed over the IPv6 pseudo-header (RFC 8200 section 8.1) and
// the whole ICMPv6 packet with its checksum field treated as zero.
func checksumValid(src, dst netip.Addr, packet []byte) bool {
	if len(packet) < 4 {
		return false
	}
	sum := uint32(0)

	src16 := src.As16()
	dst16 := dst.As16()
	for i := 0; i < 16; i += 2 {
		sum += uint32(src16[i])<<8 | uint32(src16[i+1])
		sum += uint32(dst16[i])<<8 | uint32(dst16[i+1])
	}

	// Upper-layer packet length (32 bits) and next header = 58 (ICMPv6).
	length := uint32(len(packet))
	sum += (length >> 16) & 0xffff
	sum += length & 0xffff
	sum += 58

	// Zero the checksum field on a local copy so the 16-bit word alignment of
	// every following byte is preserved; skipping the word would shift it.
	zeroed := append([]byte(nil), packet...)
	zeroed[2], zeroed[3] = 0, 0
	sum += sumWords(zeroed)

	// Add the checksum value itself: a valid datagram folds to 0xffff.
	sum += uint32(packet[2])<<8 | uint32(packet[3])

	// Fold carries until the result fits in 16 bits.
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return uint16(sum) == 0xffff
}

// sumWords sums a byte slice as 16-bit big-endian words. A trailing odd byte
// is padded with a zero per RFC 8200 section 8.1.
func sumWords(b []byte) uint32 {
	sum := uint32(0)
	for i := 0; i < len(b); i += 2 {
		word := uint32(b[i]) << 8
		if i+1 < len(b) {
			word |= uint32(b[i+1])
		}
		sum += word
	}
	return sum
}
