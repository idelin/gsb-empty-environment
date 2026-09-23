package huffcodec

// Code is a canonical prefix code for one symbol. Bits holds the code value
// right-aligned; it is transmitted most-significant-bit first over Len bits.
type Code struct {
	Bits uint32
	Len  uint8
}

// CanonicalCodes assigns canonical (DEFLATE-style) codes for the given code
// lengths: symbols are ordered by (length, symbol value) and codes are
// handed out in increasing order. The lengths are validated first, so
// oversubscribed or incomplete tables are rejected before any assignment.
func CanonicalCodes(lengths []uint8) ([]Code, error) {
	if err := ValidateLengths(lengths); err != nil {
		return nil, err
	}
	var blCount [MaxCodeLength + 1]uint32
	for _, l := range lengths {
		if l > MaxCodeLength {
			return nil, ErrOversubscribed
		}
		if l > 0 {
			blCount[l]++
		}
	}
	var nextCode [MaxCodeLength + 1]uint32
	var code uint32
	for bits := 1; bits <= MaxCodeLength; bits++ {
		code = (code + blCount[bits-1]) << 1
		nextCode[bits] = code
	}
	codes := make([]Code, len(lengths))
	for sym, l := range lengths {
		if l == 0 {
			continue
		}
		c := nextCode[l]
		if c >= 1<<l {
			return nil, ErrOversubscribed
		}
		codes[sym] = Code{Bits: c, Len: l}
		nextCode[l]++
	}
	return codes, nil
}
