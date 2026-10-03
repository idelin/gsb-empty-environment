// Package wire implements a zero-dependency Protocol Buffers binary
// wire-format parser: varint (7-bit compression), zigzag signed
// integers, fixed32/64 and length-delimited primitives, all with
// strict bounds checking so truncated input is reported safely.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	Varint     WireType = 0
	Fixed64    WireType = 1
	Bytes      WireType = 2
	StartGroup WireType = 3 // deprecated, rejected
	EndGroup   WireType = 4 // deprecated, rejected
	Fixed32    WireType = 5
)

// MaxFieldNumber is the largest legal protobuf field number (2^29-1).
const MaxFieldNumber = 1<<29 - 1

var (
	ErrTruncated   = errors.New("wire: truncated input")
	ErrOverflow    = errors.New("wire: varint overflows 64-bit integer")
	ErrBadWireType = errors.New("wire: unsupported wire type")
	ErrBadTag      = errors.New("wire: invalid field number")
	ErrBadLength   = errors.New("wire: length-delimited size out of range")
)

// TruncatedError reports exactly where parsing ran out of input.
type TruncatedError struct {
	What  string // component being decoded, e.g. "varint", "fixed32"
	Field uint32 // field number being decoded (0 when unknown)
	Need  int    // additional bytes required (lower bound)
	Have  int    // bytes remaining
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("wire: truncated %s (field %d): need >= %d more byte(s), have %d",
		e.What, e.Field, e.Need, e.Have)
}

// IsTruncated reports whether err (or any error it wraps) is a
// truncation error, i.e. the input ended prematurely.
func IsTruncated(err error) bool {
	for err != nil {
		if err == ErrTruncated {
			return true
		}
		if _, ok := err.(*TruncatedError); ok {
			return true
		}
		if fe, ok := err.(*FieldError); ok {
			err = fe.Err
			continue
		}
		return false
	}
	return false
}

// FieldError attaches a field number to a lower-level decode error.
type FieldError struct {
	Field uint32
	Err   error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("wire: field %d: %v", e.Field, e.Err)
}

// AppendVarint encodes v using 7-bit-per-byte little-endian-base-128
// compression and appends it to buf.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// ConsumeVarint decodes a varint from the head of buf, returning the
// value and the number of bytes consumed. It returns TruncatedError
// when buf ends mid-varint and ErrOverflow when the encoding exceeds
// 64 bits (more than 10 bytes, or a 10th byte larger than 1).
func ConsumeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	limit := len(buf)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		b := buf[i]
		if i == 9 && b > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(buf) >= 10 {
		// 10 continuation bytes and still unterminated.
		return 0, 0, ErrOverflow
	}
	return 0, 0, &TruncatedError{What: "varint", Need: 1, Have: 0}
}

// ZigzagEncode maps a signed integer to an unsigned one so that
// small-magnitude negatives stay small: 0->0, -1->1, 1->2, -2->3.
func ZigzagEncode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// ZigzagDecode is the inverse of ZigzagEncode.
func ZigzagDecode(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// ConsumeFixed32 decodes 4 little-endian bytes from buf.
func ConsumeFixed32(buf []byte) (uint32, int, error) {
	if len(buf) < 4 {
		return 0, 0, &TruncatedError{What: "fixed32", Need: 4 - len(buf), Have: len(buf)}
	}
	return binary.LittleEndian.Uint32(buf), 4, nil
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(buf []byte, v uint64) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// ConsumeFixed64 decodes 8 little-endian bytes from buf.
func ConsumeFixed64(buf []byte) (uint64, int, error) {
	if len(buf) < 8 {
		return 0, 0, &TruncatedError{What: "fixed64", Need: 8 - len(buf), Have: len(buf)}
	}
	return binary.LittleEndian.Uint64(buf), 8, nil
}

// AppendTag appends the (field number, wire type) key of a field.
func AppendTag(buf []byte, fieldNum uint32, wt WireType) []byte {
	return AppendVarint(buf, uint64(fieldNum)<<3|uint64(wt))
}

// ConsumeTag decodes a field key, rejecting zero/out-of-range field
// numbers and deprecated or reserved wire types.
func ConsumeTag(buf []byte) (uint32, WireType, int, error) {
	v, n, err := ConsumeVarint(buf)
	if err != nil {
		return 0, 0, 0, err
	}
	fieldNum := uint32(v >> 3)
	wt := WireType(v & 7)
	if fieldNum == 0 || fieldNum > MaxFieldNumber {
		return 0, 0, 0, ErrBadTag
	}
	switch wt {
	case Varint, Fixed64, Bytes, Fixed32:
		return fieldNum, wt, n, nil
	default:
		return 0, 0, 0, ErrBadWireType
	}
}
