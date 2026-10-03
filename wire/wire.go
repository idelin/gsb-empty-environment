// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format codec: varint (7-bit groups), ZigZag signed integers,
// fixed32/fixed64 and length-delimited nested messages.
//
// Every read is bounds-checked; truncated input is reported via
// *TruncatedError instead of panicking or reading out of range.
package wire

import (
	"errors"
	"fmt"
)

// Type is a protobuf wire type (the low 3 bits of a tag).
type Type int

const (
	Varint  Type = 0 // int32/int64/uint32/uint64/sint32/sint64/bool/enum
	Fixed64 Type = 1 // fixed64/sfixed64/double
	Bytes   Type = 2 // string/bytes/embedded message/packed repeated
	Fixed32 Type = 5 // fixed32/sfixed32/float
)

func (t Type) String() string {
	switch t {
	case Varint:
		return "varint"
	case Fixed64:
		return "fixed64"
	case Bytes:
		return "bytes"
	case Fixed32:
		return "fixed32"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

var (
	// ErrOverflow reports a varint whose payload exceeds 64 bits.
	ErrOverflow = errors.New("wire: varint overflows a 64-bit integer")
	// ErrBadWireType reports an unsupported or mismatched wire type.
	ErrBadWireType = errors.New("wire: bad wire type")
)

// TruncatedError reports a read that would run past the end of the input.
// It is the safe-bounds guard of the decoder: no truncated field is ever
// partially consumed.
type TruncatedError struct {
	Op   string // operation that failed: "tag", "varint", "fixed32", ...
	Need int    // additional bytes required
	Have int    // bytes actually remaining
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("wire: truncated %s: need %d more byte(s), have %d", e.Op, e.Need, e.Have)
}

// IsTruncated reports whether err (or any error it wraps) is a truncation.
func IsTruncated(err error) bool {
	var te *TruncatedError
	return errors.As(err, &te)
}

// EncodeZigZag64 maps a signed integer to an unsigned one so that small
// negative values stay small: 0->0, -1->1, 1->2, -2->3, ...
func EncodeZigZag64(x int64) uint64 { return uint64(x<<1) ^ uint64(x>>63) }

// DecodeZigZag64 is the inverse of EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// EncodeZigZag32 is the 32-bit ZigZag transform.
func EncodeZigZag32(x int32) uint32 { return uint32(x<<1) ^ uint32(x>>31) }

// DecodeZigZag32 is the inverse of EncodeZigZag32.
func DecodeZigZag32(v uint32) int32 { return int32(v>>1) ^ -int32(v&1) }

// AppendVarint encodes v as 7-bit groups, little-endian, high bit = more.
func AppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// AppendTag encodes a field number plus wire type.
func AppendTag(b []byte, field int, t Type) []byte {
	return AppendVarint(b, uint64(field)<<3|uint64(t))
}

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(b []byte, v uint64) []byte {
	return append(b,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendBytes appends a varint length prefix followed by p.
func AppendBytes(b, p []byte) []byte {
	b = AppendVarint(b, uint64(len(p)))
	return append(b, p...)
}

// AppendString appends a varint length prefix followed by s.
func AppendString(b []byte, s string) []byte {
	b = AppendVarint(b, uint64(len(s)))
	return append(b, s...)
}
