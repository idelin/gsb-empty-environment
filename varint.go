package streamcol

import "errors"

var (
	// ErrNeedMoreData reports that the buffer is a strict prefix of a valid
	// encoding: parsing may succeed once more bytes arrive. It is never
	// returned for bytes that are already known to be invalid.
	ErrNeedMoreData = errors.New("streamcol: need more data")
	// ErrVarintOverflow reports a varint that does not fit in 64 bits
	// (more than 10 bytes, or a 10th byte other than 0x01).
	ErrVarintOverflow = errors.New("streamcol: varint overflows 64 bits")
	// ErrVarintNotShortest reports a varint that uses more bytes than the
	// canonical shortest encoding of its value.
	ErrVarintNotShortest = errors.New("streamcol: varint is not the shortest encoding")
	// ErrOverflow reports a 64-bit signed overflow in delta arithmetic.
	ErrOverflow = errors.New("streamcol: 64-bit integer overflow")
)

// maxVarintLen is the longest legal encoding of a uint64: 9*7 = 63 bits in
// the first nine bytes, plus a single payload bit in the tenth.
const maxVarintLen = 10

// appendUvarint appends the canonical (shortest) varint encoding of v.
func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// decodeUvarint decodes a varint from the head of buf, returning the value
// and the number of bytes consumed. It returns ErrNeedMoreData only when
// buf is a prefix of some valid encoding; any byte sequence that is already
// known to be invalid yields a hard error so callers can distinguish
// "missing data" from "bad data".
func decodeUvarint(buf []byte) (uint64, int, error) {
	n := len(buf)
	if n > maxVarintLen {
		n = maxVarintLen
	}
	var v uint64
	for i := 0; i < n; i++ {
		b := buf[i]
		if i == maxVarintLen-1 {
			// Tenth byte: only bit 63 remains, so the byte must be exactly
			// 0x01. A set continuation bit (>= 0x80) or any value above 1
			// overflows 64 bits; 0x00 would be non-shortest.
			if b > 1 {
				return 0, 0, ErrVarintOverflow
			}
			if b == 0 {
				return 0, 0, ErrVarintNotShortest
			}
			v |= uint64(b) << 63
			return v, maxVarintLen, nil
		}
		if b < 0x80 {
			if b == 0 && i > 0 {
				// A zero final byte after the first means the value fits in
				// fewer bytes: non-canonical padding such as 0x80 0x00.
				return 0, 0, ErrVarintNotShortest
			}
			v |= uint64(b) << (7 * uint(i))
			return v, i + 1, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
	}
	if len(buf) >= maxVarintLen {
		return 0, 0, ErrVarintOverflow
	}
	return 0, 0, ErrNeedMoreData
}

// zigzagEncode maps a signed int64 to an unsigned varint-friendly value.
// It is a total bijection: math.MinInt64 maps to math.MaxUint64.
func zigzagEncode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// zigzagDecode inverts zigzagEncode for every uint64 input; it cannot
// overflow because every uint64 has a defined preimage.
func zigzagDecode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

func appendZigzag(dst []byte, v int64) []byte {
	return appendUvarint(dst, zigzagEncode(v))
}

func decodeZigzag(buf []byte) (int64, int, error) {
	u, n, err := decodeUvarint(buf)
	if err != nil {
		return 0, 0, err
	}
	return zigzagDecode(u), n, nil
}

// checkedAdd returns a+b, rejecting results that wrap around int64.
func checkedAdd(a, b int64) (int64, error) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, ErrOverflow
	}
	return s, nil
}

// checkedSub returns a-b, rejecting results that wrap around int64.
func checkedSub(a, b int64) (int64, error) {
	d := a - b
	if (b > 0 && d > a) || (b < 0 && d < a) {
		return 0, ErrOverflow
	}
	return d, nil
}
