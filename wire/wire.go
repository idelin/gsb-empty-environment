// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format codec: varint, zigzag, fixed32/64 and length-delimited
// primitives. All decoders are bounds-safe and report truncation instead of
// panicking or reading out of range.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	WireVarint  WireType = 0 // int32/64, uint32/64, sint32/64, bool, enum
	WireFixed64 WireType = 1 // fixed64, sfixed64, double
	WireBytes   WireType = 2 // string, bytes, embedded messages, packed repeated
	WireFixed32 WireType = 5 // fixed32, sfixed32, float
)

// Sentinel errors. Use Is(err, ErrTruncated) to test wrapped errors.
var (
	ErrTruncated          = errors.New("wire: truncated input (safe bounds guard)")
	ErrOverflow           = errors.New("wire: varint overflows 64 bits")
	ErrBadWireType        = errors.New("wire: unsupported wire type")
	ErrBadFieldNumber     = errors.New("wire: invalid field number")
	ErrNegativeLength     = errors.New("wire: negative or overflowing length prefix")
	ErrWireTypeMismatch   = errors.New("wire: wire type does not match schema")
	ErrUnsupportedSchema  = errors.New("wire: unsupported schema field")
)

// Error carries decode context (operation + field number) around a cause.
type Error struct {
	Op    string
	Field int
	Err   error
}

func (e *Error) Error() string {
	if e.Field > 0 {
		return fmt.Sprintf("wire: %s (field %d): %v", e.Op, e.Field, e.Err)
	}
	return fmt.Sprintf("wire: %s: %v", e.Op, e.Err)
}

// Unwrap exposes the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

// TruncatedError annotates a truncation with detail and unwraps to
// ErrTruncated so Is(err, ErrTruncated) still matches.
type TruncatedError struct {
	Detail string
}

func (e *TruncatedError) Error() string {
	if e.Detail == "" {
		return ErrTruncated.Error()
	}
	return ErrTruncated.Error() + ": " + e.Detail
}

// Unwrap exposes ErrTruncated as the cause.
func (e *TruncatedError) Unwrap() error { return ErrTruncated }

// AnnotatedError pairs a sentinel cause with extra detail while keeping
// Is(err, sentinel) functional.
type AnnotatedError struct {
	Detail string
	Err    error
}

func (e *AnnotatedError) Error() string { return e.Err.Error() + ": " + e.Detail }

// Unwrap exposes the sentinel cause.
func (e *AnnotatedError) Unwrap() error { return e.Err }

// Is reports whether err or any wrapped cause equals target.
func Is(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// MaxVarintLen is the maximum encoded length of a 64-bit varint.
const MaxVarintLen = 10

// MaxFieldNumber is the largest legal protobuf field number (2^29 - 1).
const MaxFieldNumber = 1<<29 - 1

// AppendVarint encodes v using 7-bit groups, LSB first, continuation bit set.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ConsumeVarint decodes a varint from the head of buf, returning the value
// and the number of bytes consumed. It returns ErrTruncated if buf ends
// mid-varint and ErrOverflow if the encoding exceeds 64 bits.
func ConsumeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf) && i < MaxVarintLen; i++ {
		b := buf[i]
		if i == MaxVarintLen-1 && b > 1 {
			// 10th byte may only contribute a single bit for uint64.
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrTruncated
}

// EncodeZigZag64 maps a signed int64 to an unsigned varint-friendly value.
func EncodeZigZag64(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// DecodeZigZag64 is the inverse of EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// EncodeZigZag32 maps a signed int32 to an unsigned varint-friendly value.
func EncodeZigZag32(v int32) uint64 { return uint64(uint32(v<<1) ^ uint32(v>>31)) }

// DecodeZigZag32 is the inverse of EncodeZigZag32.
func DecodeZigZag32(v uint64) int32 { return int32(int32(v>>1) ^ -int32(v&1)) }

// AppendFixed32 appends v in little-endian order.
func AppendFixed32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

// AppendFixed64 appends v in little-endian order.
func AppendFixed64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// ConsumeFixed32 reads 4 little-endian bytes; ErrTruncated if short.
func ConsumeFixed32(buf []byte) (uint32, int, error) {
	if len(buf) < 4 {
		return 0, 0, ErrTruncated
	}
	return binary.LittleEndian.Uint32(buf), 4, nil
}

// ConsumeFixed64 reads 8 little-endian bytes; ErrTruncated if short.
func ConsumeFixed64(buf []byte) (uint64, int, error) {
	if len(buf) < 8 {
		return 0, 0, ErrTruncated
	}
	return binary.LittleEndian.Uint64(buf), 8, nil
}

// AppendTag appends the (field number, wire type) key varint.
func AppendTag(dst []byte, num int, wt WireType) []byte {
	return AppendVarint(dst, uint64(num)<<3|uint64(wt))
}

// AppendBytes appends a length-delimited payload.
func AppendBytes(dst, payload []byte) []byte {
	dst = AppendVarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}
