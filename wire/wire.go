// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format codec. It supports varint (7-bit compressed) integers,
// ZigZag signed integers, fixed32/fixed64, length-delimited fields, nested
// messages and packed repeated fields. All parsers are bounds-safe: any
// truncation or malformed input is reported as an error, never a panic or
// an out-of-bounds access.
package wire

import (
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	WireVarint  WireType = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 WireType = 1 // fixed64, sfixed64, double
	WireBytes   WireType = 2 // string, bytes, embedded messages, packed repeated
	WireFixed32 WireType = 5 // fixed32, sfixed32, float
)

// Sentinel errors returned by the codec. Compare with == (Go 1.11 has no
// errors.Is); detailed context is carried by *ParseError.
var (
	ErrTruncated   = errors.New("wire: truncated input")
	ErrOverflow    = errors.New("wire: varint overflows 64 bits")
	ErrInvalidWire = errors.New("wire: invalid wire type")
	ErrBadTag      = errors.New("wire: invalid field number")
	ErrNegativeLen = errors.New("wire: negative length prefix")
)

// ParseError describes where and why parsing failed.
type ParseError struct {
	Offset int    // byte offset in the buffer where the failure occurred
	Field  int    // field number being decoded, 0 if not applicable
	Op     string // operation, e.g. "varint", "fixed32", "bytes"
	Err    error  // one of the sentinel errors above
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("wire: offset %d field %d op %s: %v", e.Offset, e.Field, e.Op, e.Err)
}

// Unwrap-free cause accessor (Go 1.11 compatible).
func (e *ParseError) Cause() error { return e.Err }

// ---------------------------------------------------------------------------
// Varint: 7-bit-per-byte little-endian base-128 compression.
// ---------------------------------------------------------------------------

// MaxVarintLen is the maximum encoded length of a 64-bit varint.
const MaxVarintLen = 10

// AppendVarint appends v to b using varint encoding and returns the
// extended buffer.
func AppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
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

// ConsumeVarint decodes a varint from the head of b, returning the value and
// the number of bytes consumed. It reports ErrTruncated if the buffer ends
// mid-varint and ErrOverflow if the encoding exceeds 64 bits.
func ConsumeVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < MaxVarintLen; i++ {
		c := b[i]
		if i == MaxVarintLen-1 && c > 1 {
			// The 10th byte may only contribute a single bit.
			return 0, 0, &ParseError{Offset: i, Op: "varint", Err: ErrOverflow}
		}
		if c < 0x80 {
			return v | uint64(c)<<uint(7*i), i + 1, nil
		}
		v |= uint64(c&0x7f) << uint(7*i)
	}
	if len(b) >= MaxVarintLen {
		return 0, 0, &ParseError{Offset: MaxVarintLen, Op: "varint", Err: ErrOverflow}
	}
	return 0, 0, &ParseError{Offset: len(b), Op: "varint", Err: ErrTruncated}
}

// ---------------------------------------------------------------------------
// ZigZag: maps signed integers to unsigned so small magnitudes stay small.
// ---------------------------------------------------------------------------

// EncodeZigZag64 converts a signed int64 to its ZigZag uint64 form.
func EncodeZigZag64(x int64) uint64 {
	return uint64(x<<1) ^ uint64(x>>63)
}

// DecodeZigZag64 converts a ZigZag uint64 back to a signed int64.
func DecodeZigZag64(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// EncodeZigZag32 converts a signed int32 to its ZigZag uint32 form.
func EncodeZigZag32(x int32) uint32 {
	return uint32(x<<1) ^ uint32(x>>31)
}

// DecodeZigZag32 converts a ZigZag uint32 back to a signed int32.
func DecodeZigZag32(v uint32) int32 {
	return int32(v>>1) ^ -int32(v&1)
}

// ---------------------------------------------------------------------------
// Fixed-width little-endian integers.
// ---------------------------------------------------------------------------

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

// ConsumeFixed32 reads 4 little-endian bytes from the head of b.
func ConsumeFixed32(b []byte) (uint32, int, error) {
	if len(b) < 4 {
		return 0, 0, &ParseError{Offset: len(b), Op: "fixed32", Err: ErrTruncated}
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, 4, nil
}

// ConsumeFixed64 reads 8 little-endian bytes from the head of b.
func ConsumeFixed64(b []byte) (uint64, int, error) {
	if len(b) < 8 {
		return 0, 0, &ParseError{Offset: len(b), Op: "fixed64", Err: ErrTruncated}
	}
	v := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
	return v, 8, nil
}

// ---------------------------------------------------------------------------
// Tags and length-delimited fields.
// ---------------------------------------------------------------------------

// AppendTag appends the tag for field number num with wire type wt.
func AppendTag(b []byte, num int, wt WireType) []byte {
	return AppendVarint(b, uint64(num)<<3|uint64(wt))
}

// ConsumeTag decodes a tag, returning the field number and wire type.
func ConsumeTag(b []byte) (num int, wt WireType, n int, err error) {
	v, n, err := ConsumeVarint(b)
	if err != nil {
		return 0, 0, 0, err
	}
	wt = WireType(v & 7)
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return 0, 0, 0, &ParseError{Offset: n, Op: "tag", Err: ErrInvalidWire}
	}
	num = int(v >> 3)
	if num <= 0 {
		return 0, 0, 0, &ParseError{Offset: n, Op: "tag", Err: ErrBadTag}
	}
	return num, wt, n, nil
}

// AppendBytes appends a length-delimited field: varint length + payload.
func AppendBytes(b, payload []byte) []byte {
	b = AppendVarint(b, uint64(len(payload)))
	return append(b, payload...)
}

// ConsumeBytes reads a length-delimited field, returning the payload slice
// (a sub-slice of b, no copy) and the total bytes consumed.
func ConsumeBytes(b []byte) ([]byte, int, error) {
	l, n, err := ConsumeVarint(b)
	if err != nil {
		return nil, 0, err
	}
	if l > uint64(len(b)-n) {
		return nil, 0, &ParseError{Offset: n, Op: "bytes", Err: ErrTruncated}
	}
	return b[n : n+int(l)], n + int(l), nil
}

// ---------------------------------------------------------------------------
// Message-level iteration.
// ---------------------------------------------------------------------------

// Field is one decoded field of a message. Exactly one of the value members
// is meaningful, selected by Type.
type Field struct {
	Number  int
	Type    WireType
	Varint  uint64 // WireVarint
	Fixed32 uint32 // WireFixed32
	Fixed64 uint64 // WireFixed64
	Bytes   []byte // WireBytes (sub-slice of the input)
}

// Fields parses b as a sequence of protobuf fields, stopping at the end of
// the buffer. Any malformed or truncated field yields an error.
func Fields(b []byte) ([]Field, error) {
	var out []Field
	for len(b) > 0 {
		f, n, err := NextField(b)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		b = b[n:]
	}
	return out, nil
}

// NextField decodes the first field in b and returns it with the number of
// bytes consumed, enabling manual streaming iteration.
func NextField(b []byte) (Field, int, error) {
	var f Field
	num, wt, n, err := ConsumeTag(b)
	if err != nil {
		return f, 0, err
	}
	f.Number, f.Type = num, wt
	body := b[n:]
	switch wt {
	case WireVarint:
		v, m, err := ConsumeVarint(body)
		if err != nil {
			return f, 0, withField(err, num)
		}
		f.Varint = v
		return f, n + m, nil
	case WireFixed64:
		v, m, err := ConsumeFixed64(body)
		if err != nil {
			return f, 0, withField(err, num)
		}
		f.Fixed64 = v
		return f, n + m, nil
	case WireFixed32:
		v, m, err := ConsumeFixed32(body)
		if err != nil {
			return f, 0, withField(err, num)
		}
		f.Fixed32 = v
		return f, n + m, nil
	case WireBytes:
		v, m, err := ConsumeBytes(body)
		if err != nil {
			return f, 0, withField(err, num)
		}
		f.Bytes = v
		return f, n + m, nil
	}
	return f, 0, &ParseError{Offset: n, Field: num, Op: "field", Err: ErrInvalidWire}
}

func withField(err error, num int) error {
	if pe, ok := err.(*ParseError); ok {
		cp := *pe
		cp.Field = num
		return &cp
	}
	return err
}

// ---------------------------------------------------------------------------
// Packed repeated fields (length-delimited runs of scalar values).
// ---------------------------------------------------------------------------

// ConsumePackedVarints decodes a packed repeated varint field payload.
func ConsumePackedVarints(b []byte) ([]uint64, error) {
	var out []uint64
	for len(b) > 0 {
		v, n, err := ConsumeVarint(b)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}

// ConsumePackedFixed32 decodes a packed repeated fixed32 field payload.
func ConsumePackedFixed32(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, &ParseError{Offset: len(b), Op: "packed-fixed32", Err: ErrTruncated}
	}
	out := make([]uint32, 0, len(b)/4)
	for len(b) > 0 {
		v, n, _ := ConsumeFixed32(b)
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}

// ConsumePackedFixed64 decodes a packed repeated fixed64 field payload.
func ConsumePackedFixed64(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, &ParseError{Offset: len(b), Op: "packed-fixed64", Err: ErrTruncated}
	}
	out := make([]uint64, 0, len(b)/8)
	for len(b) > 0 {
		v, n, _ := ConsumeFixed64(b)
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}
