package nat64

import "encoding/binary"

// checksum computes the RFC 1071 Internet checksum over data (data may be
// an odd number of bytes; the final byte is zero-padded).
func checksum(data []byte) uint16 {
	var sum uint32
	n := len(data)
	i := 0
	for ; i+1 < n; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if i < n {
		sum += uint32(data[i]) << 8
	}
	return fold(sum)
}

func fold(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	if sum == 0xffff {
		return 0xffff
	}
	return ^uint16(sum)
}

// pseudoV4 computes the IPv4 pseudo-header checksum contribution.
func pseudoV4(src, dst [4]byte, proto uint8, length int) uint32 {
	var sum uint32
	sum += uint32(binary.BigEndian.Uint16(src[0:2]))
	sum += uint32(binary.BigEndian.Uint16(src[2:4]))
	sum += uint32(binary.BigEndian.Uint16(dst[0:2]))
	sum += uint32(binary.BigEndian.Uint16(dst[2:4]))
	sum += uint32(proto)
	sum += uint32(length)
	return sum
}

// pseudoV6 computes the IPv6 pseudo-header checksum contribution.
func pseudoV6(src, dst [16]byte, proto uint8, length int) uint32 {
	var sum uint32
	for i := 0; i < 16; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(src[i : i+2]))
		sum += uint32(binary.BigEndian.Uint16(dst[i : i+2]))
	}
	sum += uint32(length)
	sum += uint32(proto)
	return sum
}

// transportChecksumV4 recomputes a TCP/UDP checksum for an IPv4 packet
// (l4 starts at the transport header, totalLen covers the transport header
// and its payload).
func transportChecksumV4(src, dst [4]byte, proto uint8, l4 []byte) uint16 {
	sum := pseudoV4(src, dst, proto, len(l4))
	return fold(sum + checksumSum(l4))
}

// transportChecksumV6 recomputes a TCP/UDP checksum for an IPv6 packet.
func transportChecksumV6(src, dst [16]byte, proto uint8, l4 []byte) uint16 {
	sum := pseudoV6(src, dst, proto, len(l4))
	return fold(sum + checksumSum(l4))
}

// checksumSum returns the unfolded RFC 1071 sum over data.
func checksumSum(data []byte) uint32 {
	var sum uint32
	n := len(data)
	i := 0
	for ; i+1 < n; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if i < n {
		sum += uint32(data[i]) << 8
	}
	return sum
}

// adjustChecksum implements RFC 1624: HC' = ~(~HC + ~m + m') where m/m'
// are the unfolded checksums of the old and new header content.
func adjustChecksum(hc uint16, oldSum, newSum uint32) uint16 {
	hc1 := uint32(hc)
	if hc1 == 0 {
		hc1 = 0xffff
	}
	hc2 := uint32(0xffff) - hc1
	m1 := uint32(0xffff) - oldSum
	m2 := newSum
	x := hc2 + m1 + m2
	for x>>16 != 0 {
		x = (x & 0xffff) + (x >> 16)
	}
	if x == 0 {
		return 0xffff
	}
	return ^uint16(x)
}
