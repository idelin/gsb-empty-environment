// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format codec: Varint (7-bit compression), ZigZag signed
// integer mapping, Fixed32/Fixed64 and nested Length-delimited messages.
//
// All reads are bounds-checked; any truncated field is reported as
// ErrTruncated with the offending offset so callers get safe
// out-of-range protection instead of a panic.
package wire

import "errors"

// Wire types as defined by the Protocol Buffers binary encoding.
type WireType int

const (
	WireVarint          WireType = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64         WireType = 1 // fixed64, sfixed64, double
	WireLengthDelimited WireType = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32         WireType = 5 // fixed32, sfixed32, float
)

// Sentinel errors. Compare with == (Go 1.11 compatible, no errors.Is).
var (
	// ErrTruncated is returned when the buffer ends before a field is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than 10 bytes).
	ErrOverflow = errors.New("wire: varint overflows 64-bit integer")
	// ErrInvalidWireType is returned for wire types 3, 4 (groups, unsupported) or >5.
	ErrInvalidWireType = errors.New("wire: invalid or unsupported wire type")
	// ErrInvalidFieldNumber is returned when a tag carries field number 0.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number 0")
	// ErrNegativeLength is returned when a length prefix is unreasonably large.
	ErrNegativeLength = errors.New("wire: invalid length prefix")
)

// MaxVarintBytes is the maximum encoded length of a 64-bit varint.
const MaxVarintBytes = 10

// ---------------------------------------------------------------------------
// Varint: 7-bit-per-byte little-endian base-128 compression
// ---------------------------------------------------------------------------

// EncodeVarint appends the varint encoding of v to dst and returns the
// extended slice.
func EncodeVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// SizeVarint returns the encoded length of v in bytes.
func SizeVarint(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// DecodeVarint reads a varint from the head of buf, returning the value and
// the number of bytes consumed. It returns ErrTruncated if buf ends
// mid-varint and ErrOverflow if the varint uses more than 10 bytes.
func DecodeVarint(buf []byte) (v uint64, n int, err error) {
	for i := 0; i < len(buf) && i < MaxVarintBytes; i++ {
		b := buf[i]
		if i == MaxVarintBytes-1 && b > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(buf) >= MaxVarintBytes {
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrTruncated
}

// ---------------------------------------------------------------------------
// ZigZag: signed <-> unsigned bijection (0->0, -1->1, 1->2, -2->3, ...)
// ---------------------------------------------------------------------------

// ZigZagEncode32 maps a signed 32-bit integer to its unsigned varint form.
func ZigZagEncode32(v int32) uint32 { return uint32(v<<1) ^ uint32(v>>31) }

// ZigZagEncode64 maps a signed 64-bit integer to its unsigned varint form.
func ZigZagEncode64(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// ZigZagDecode32 is the inverse of ZigZagEncode32.
func ZigZagDecode32(v uint32) int32 { return int32(v>>1) ^ -int32(v&1) }

// ZigZagDecode64 is the inverse of ZigZagEncode64.
func ZigZagDecode64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// ---------------------------------------------------------------------------
// Fixed-width little-endian codecs
// ---------------------------------------------------------------------------

// EncodeFixed32 appends the 4-byte little-endian encoding of v.
func EncodeFixed32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// EncodeFixed64 appends the 8-byte little-endian encoding of v.
func EncodeFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// DecodeFixed32 reads 4 little-endian bytes from the head of buf.
func DecodeFixed32(buf []byte) (uint32, int, error) {
	if len(buf) < 4 {
		return 0, 0, ErrTruncated
	}
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24, 4, nil
}

// DecodeFixed64 reads 8 little-endian bytes from the head of buf.
func DecodeFixed64(buf []byte) (uint64, int, error) {
	if len(buf) < 8 {
		return 0, 0, ErrTruncated
	}
	return uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 |
		uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56, 8, nil
}

// ---------------------------------------------------------------------------
// Tag (field key) helpers
// ---------------------------------------------------------------------------

// EncodeTag appends the tag for the given field number and wire type.
func EncodeTag(dst []byte, fieldNum int, wt WireType) []byte {
	return EncodeVarint(dst, uint64(fieldNum)<<3|uint64(wt))
}

// DecodeTag decodes a tag into its field number and wire type.
func DecodeTag(buf []byte) (fieldNum int, wt WireType, n int, err error) {
	v, n, err := DecodeVarint(buf)
	if err != nil {
		return 0, 0, 0, err
	}
	wt = WireType(v & 0x7)
	fieldNum = int(v >> 3)
	if fieldNum <= 0 {
		return 0, 0, 0, ErrInvalidFieldNumber
	}
	switch wt {
	case WireVarint, WireFixed64, WireLengthDelimited, WireFixed32:
	default:
		return 0, 0, 0, ErrInvalidWireType
	}
	return fieldNum, wt, n, nil
}
