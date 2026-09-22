package sintcol

import (
	"io"
	"math"
)

const maxVarintLen = 10

func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// strictUvarint rejects truncated input, 11-byte encodings, non-minimal
// encodings, and invalid bits in the tenth byte.
func strictUvarint(buf []byte) (uint64, int, error) {
	if len(buf) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}

	var v uint64
	for i := 0; i < maxVarintLen; i++ {
		if i >= len(buf) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		c := buf[i]
		if i == 9 {
			if c&0x80 != 0 || c != 0x01 {
				return 0, 0, ErrCorrupt
			}
			v |= 1 << 63
		} else {
			v |= uint64(c&0x7f) << (7 * i)
		}
		if c&0x80 == 0 {
			if i > 0 && buf[0]&0x7f == 0 {
				return 0, 0, ErrCorrupt
			}
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrCorrupt
}

func zigzagEncode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

func zigzagDecode(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

func appendZigzag(dst []byte, v int64) []byte {
	return appendUvarint(dst, zigzagEncode(v))
}

func addChecked(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func subChecked(a, b int64) (int64, bool) {
	if b == math.MinInt64 {
		if a >= 0 {
			return 0, false
		}
		return a - b, true
	}
	return addChecked(a, -b)
}
