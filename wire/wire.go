// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format codec. All reads are bounds-checked: any truncated or
// malformed input is reported as an error instead of panicking or reading
// out of bounds.
package wire

import "errors"

// Wire types as defined by the Protocol Buffers binary encoding.
const (
	WireVarint  = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 = 1 // fixed64, sfixed64, double
	WireBytes   = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32 = 5 // fixed32, sfixed32, float
)

// Sentinel errors. The hot path never allocates: callers compare with
// errors.Is and only wrap for reporting on the (cold) error path.
var (
	ErrTruncated       = errors.New("wire: truncated input")
	ErrOverflow        = errors.New("wire: varint overflows 64 bits")
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	ErrInvalidTag      = errors.New("wire: invalid field number")
)

// maxVarintBytes is the maximum number of bytes in a 64-bit varint.
const maxVarintBytes = 10

// AppendVarint appends v to dst in varint (7-bit groups, LSB first) encoding.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendTag appends a field tag (field number + wire type) to dst.
func AppendTag(dst []byte, fieldNum int, wireType int) []byte {
	return AppendVarint(dst, uint64(fieldNum)<<3|uint64(wireType))
}

// AppendFixed32 appends v to dst in little-endian fixed32 encoding.
func AppendFixed32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends v to dst in little-endian fixed64 encoding.
func AppendFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// EncodeZigZag64 maps a signed integer to an unsigned one so that values
// with small absolute magnitude have small varint encodings.
func EncodeZigZag64(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// DecodeZigZag64 is the inverse of EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// EncodeZigZag32 is the 32-bit variant of EncodeZigZag64.
func EncodeZigZag32(v int32) uint32 {
	return uint32(v<<1) ^ uint32(v>>31)
}

// DecodeZigZag32 is the inverse of EncodeZigZag32.
func DecodeZigZag32(v uint32) int32 {
	return int32(v>>1) ^ -int32(v&1)
}
