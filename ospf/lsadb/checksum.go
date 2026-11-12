package lsadb

// checksumOffset is the byte offset of the 16-bit LS checksum field inside
// the OSPFv2 LSA header (RFC 2328 section A.4.1): LS age 0-1, options 2,
// LS type 3, Link State ID 4-7, advertising router 8-11, LS sequence number
// 12-15, LS checksum 16-17, length 18-19.
const checksumOffset = 16

// fletcher16 computes the OSPF Fletcher checksum over data. The LS age field
// (the first two bytes) and the checksum field itself are treated as zero,
// which is equivalent to skipping them because zero bytes do not change the
// running sums (RFC 2328 section 12.1.7).
func fletcher16(data []byte) (c0, c1 uint16) {
	var sum0, sum1 uint32
	for index, b := range data {
		switch {
		case index < 2: // LS age: not part of the checksum.
			continue
		case index >= checksumOffset && index < checksumOffset+2: // checksum.
			continue
		}
		sum0 += uint32(b)
		sum1 += sum0
	}
	return uint16(sum0 % 255), uint16(sum1 % 255)
}

// computeChecksum derives the two checksum bytes x (at checksumOffset) and y
// (at checksumOffset+1) that make the Fletcher sums of the LSA equal zero.
// With c0/c1 computed over the LSA with zeroed age and checksum fields and L
// the number of checksum-covered bytes:
//
//	x = ((L - 8) * c0 - (L - 1) * c1) mod 255
//	y = ((L - 1) * c1 - (L - 7) * c0) mod 255
//
// A result byte of zero is encoded as 255, since 0 and 255 are equivalent
// modulo 255 and a zero byte would be indistinguishable from a skipped byte.
func computeChecksum(data []byte) uint16 {
	c0, c1 := fletcher16(data)
	length := uint32(len(data))

	x := ((length-8)*uint32(c0) - (length-1)*uint32(c1)) % 255
	y := ((length-1)*uint32(c1) - (length-7)*uint32(c0)) % 255

	if x == 0 {
		x = 255
	}
	if y == 0 {
		y = 255
	}
	return uint16(x)<<8 | uint16(y)
}

// verifyChecksum reports whether the checksum carried in an encoded LSA is
// valid.
func verifyChecksum(data []byte) bool {
	got := uint16(data[checksumOffset])<<8 | uint16(data[checksumOffset+1])
	return got == computeChecksum(data)
}
