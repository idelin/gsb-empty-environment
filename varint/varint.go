package varint

import "strconv"

// AppendUvarint appends the canonical (shortest) LEB128 encoding of v
// to dst. v must fit in width bits or ErrOverflow is returned.
func AppendUvarint(dst []byte, v uint64, width uint) ([]byte, error) {
	if width == 0 || width > 64 {
		return nil, &FieldError{Field: "", Err: strconv.ErrRange}
	}
	if width < 64 && v>>width != 0 {
		return nil, ErrOverflow
	}
	for {
		b := byte(v)
		v >>= 7
		if v == 0 {
			return append(dst, b&0x7f), nil
		}
		dst = append(dst, b|0x80)
	}
}

// AppendSvarint appends the canonical zigzag varint encoding of v.
// v must fit in signed width bits or ErrOverflow is returned.
func AppendSvarint(dst []byte, v int64, width uint) ([]byte, error) {
	if width == 0 || width > 64 {
		return nil, &FieldError{Field: "", Err: strconv.ErrRange}
	}
	if width < 64 {
		min := -(int64(1) << (width - 1))
		max := (int64(1) << (width - 1)) - 1
		if v < min || v > max {
			return nil, ErrOverflow
		}
	}
	return AppendUvarint(dst, zigzagEncode(v), width)
}

// decodeUvarint reads a canonical LEB128 varint of at most width bits
// from buf. It returns the value and the number of bytes consumed.
// If buf ends mid-varint it returns ErrNeedMore (never a zero value).
// Encodings that exceed width bits return ErrOverflow; encodings that
// are not the shortest form return ErrNonCanonical.
func decodeUvarint(buf []byte, width uint) (uint64, int, error) {
	maxBytes := int((width + 6) / 7)
	var v uint64
	for i := 0; i < maxBytes; i++ {
		if i >= len(buf) {
			return 0, 0, ErrNeedMore
		}
		b := buf[i]
		if i == maxBytes-1 {
			// Last permissible byte: a continuation bit or any bit
			// beyond the field width is an overflow.
			rem := width - uint(7*i) // valid payload bits here, 1..7
			if b&0x80 != 0 {
				return 0, 0, ErrOverflow
			}
			if rem < 7 && uint64(b)>>rem != 0 {
				return 0, 0, ErrOverflow
			}
			if b == 0 && i > 0 {
				return 0, 0, ErrNonCanonical
			}
			v |= uint64(b) << (7 * uint(i))
			return v, i + 1, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b&0x80 == 0 {
			if b == 0 && i > 0 {
				return 0, 0, ErrNonCanonical
			}
			return v, i + 1, nil
		}
	}
	panic("unreachable")
}

// decodeSvarint reads a canonical zigzag varint of at most width bits.
func decodeSvarint(buf []byte, width uint) (int64, int, error) {
	u, n, err := decodeUvarint(buf, width)
	if err != nil {
		return 0, 0, err
	}
	return zigzagDecode(u), n, nil
}

func zigzagEncode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

func zigzagDecode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}
