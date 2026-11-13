package netsec

// readULEB reads a canonical unsigned LEB128 value of at most 64 bits.
//
// Canonical means the shortest possible encoding is used. In particular a
// multi-byte encoding whose final payload byte is zero is rejected.
func readULEB(data []byte, offset uint64) (value uint64, next uint64, err error) {
	if offset >= uint64(len(data)) {
		return 0, offset, invalidf("ULEB128 at offset %d is truncated", offset)
	}

	var result uint64
	for i := uint64(0); i < 10; i++ {
		pos := offset + i
		if pos >= uint64(len(data)) {
			return 0, offset, invalidf("ULEB128 at offset %d is truncated", offset)
		}
		b := data[pos]

		if i == 9 {
			// Nine groups of seven bits already fill 63 bits. The tenth
			// group may contribute only bit 63 and must terminate.
			if b&0xfe != 0 {
				return 0, offset, invalidf("ULEB128 at offset %d overflows 64 bits", offset)
			}
		}

		result |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if i > 0 && b == 0 {
				return 0, offset, invalidf("ULEB128 at offset %d is not canonical", offset)
			}
			return result, pos + 1, nil
		}
	}

	return 0, offset, invalidf("ULEB128 at offset %d overflows 64 bits", offset)
}

// readULEBEnd reads a ULEB128 that must terminate before end. This keeps a
// terminal-area ULEB from consuming the childCount byte or a following node.
func readULEBEnd(data []byte, offset, end uint64) (value uint64, next uint64, err error) {
	if offset > end || end > uint64(len(data)) {
		return 0, offset, invalidf("ULEB128 at offset %d has an invalid limit", offset)
	}
	value, localNext, err := readULEB(data[:end], offset)
	if err != nil {
		return 0, offset, err
	}
	return value, localNext, nil
}

// appendULEB encodes value in canonical unsigned LEB128 form. It is used by
// tests and is intentionally kept small.
func appendULEB(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}
