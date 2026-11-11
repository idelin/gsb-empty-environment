package netsec

import "encoding/binary"

// foldChecksum folds a running 32-bit one's-complement sum into a 16-bit
// one's-complement value.
func foldChecksum(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// addChecksumWords extends sum with the 16-bit big-endian words contained in
// b. A trailing odd byte is treated as the high byte of a final zero-padded
// word, per RFC 1071.
func addChecksumWords(sum uint32, b []byte) uint32 {
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	return sum
}

// ipv4HeaderChecksum computes the IPv4 header checksum. The caller must zero
// the checksum field before calling.
func ipv4HeaderChecksum(header []byte) uint16 {
	return foldChecksum(addChecksumWords(0, header))
}

// tcpv4Checksum computes the TCP checksum for an IPv4 segment. The TCP
// checksum field inside segment must be zeroed by the caller. srcIP and dstIP
// are the 4-byte address blocks from the IPv4 header.
func tcpv4Checksum(segment, srcIP, dstIP []byte) uint16 {
	var sum uint32
	sum += uint32(binary.BigEndian.Uint16(srcIP[0:2]))
	sum += uint32(binary.BigEndian.Uint16(srcIP[2:4]))
	sum += uint32(binary.BigEndian.Uint16(dstIP[0:2]))
	sum += uint32(binary.BigEndian.Uint16(dstIP[2:4]))
	sum += protoTCP // zero byte followed by the protocol number
	sum += uint32(len(segment))
	sum = addChecksumWords(sum, segment)
	return foldChecksum(sum)
}
