package bitlayout

// checksumBytes packs the stream bits [start,end) into bytes using the
// stream's own bit order (last byte zero-padded) and returns them. This
// canonical packing makes the checksum independent of field boundaries.
func checksumBytes(buf []byte, order BitOrder, start, end int64) []byte {
	n := end - start
	out := make([]byte, (n+7)/8)
	r := newBitReader(buf, order, end)
	for i := int64(0); i < n; i++ {
		if r.bitAt(start+i) != 0 {
			if order == MSBFirst {
				out[i>>3] |= 0x80 >> (uint(i) & 7)
			} else {
				out[i>>3] |= 1 << (uint(i) & 7)
			}
		}
	}
	return out
}

// checksum computes the named algorithm over data and truncates to w bits.
func checksum(algo string, data []byte, w int) uint64 {
	switch algo {
	case "sum":
		var s uint64
		for _, b := range data {
			s += uint64(b)
		}
		return s & mask(w)
	case "xor":
		var x uint64
		for _, b := range data {
			x ^= uint64(b)
		}
		return x & mask(w)
	case "crc8":
		// CRC-8/SMBUS: polynomial 0x07, init 0x00, no reflection.
		var crc byte
		for _, b := range data {
			crc ^= b
			for i := 0; i < 8; i++ {
				if crc&0x80 != 0 {
					crc = crc<<1 ^ 0x07
				} else {
					crc <<= 1
				}
			}
		}
		return uint64(crc)
	}
	return 0
}
