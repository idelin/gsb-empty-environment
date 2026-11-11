package transcript

import (
	"errors"
	"io"
	"math/bits"
)

// MaxVarintLen64 is the number of bytes used by the longest minimal
// LEB128 encoding of a 64-bit integer.
const MaxVarintLen64 = 10

// appendUvarint appends the unique minimal unsigned LEB128 ("varint")
// encoding of v to dst.
func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// uvarintLen reports the length of the minimal encoding of v.
func uvarintLen(v uint64) int {
	if v == 0 {
		return 1
	}
	return (bits.Len64(v) + 6) / 7
}

// readUvarint decodes a minimal unsigned LEB128 integer from the head of
// src. It returns the value, the number of bytes consumed and an error if
// the encoding is truncated, longer than 10 bytes, overflows uint64 or is
// non-minimal (a trailing byte that only contributes zero bits).
func readUvarint(src []byte) (uint64, int, error) {
	var value uint64
	for i := 0; i < MaxVarintLen64; i++ {
		if i >= len(src) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		b := src[i]
		if i == MaxVarintLen64-1 {
			// The tenth byte may only carry the single remaining bit.
			if b&0x7e != 0 {
				return 0, 0, errors.New("transcript: varint overflows uint64")
			}
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if i > 0 && b == 0 {
				return 0, 0, errors.New("transcript: non-minimal varint encoding")
			}
			return value, i + 1, nil
		}
	}
	return 0, 0, errors.New("transcript: varint too long")
}

// encodeZigZag maps signed integers to unsigned integers so that small
// absolute values always have small encodings, regardless of sign.
func encodeZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// decodeZigZag is the inverse of encodeZigZag.
func decodeZigZag(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}
