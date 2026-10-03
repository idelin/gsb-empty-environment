// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format codec: varint (7-bit groups), zigzag, fixed32/64 and
// length-delimited fields. All readers are strictly bounds-checked and never
// read past the end of the input buffer.
package wire

import (
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag of a protobuf field key.
type WireType int

const (
	WireVarint  WireType = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 WireType = 1 // fixed64, sfixed64, double
	WireBytes   WireType = 2 // string, bytes, embedded messages, packed repeated
	WireFixed32 WireType = 5 // fixed32, sfixed32, float
)

var (
	// ErrTruncated reports input that ends before a value is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow reports a varint that does not fit in 64 bits.
	ErrOverflow = errors.New("wire: varint overflows 64-bit integer")
	// ErrInvalidWireType reports a key whose type tag is not 0,1,2,5.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidFieldNumber reports a zero or negative field number.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number")
	// ErrLengthOverflow reports a length-delimited prefix larger than the input.
	ErrLengthOverflow = errors.New("wire: length-delimited field exceeds remaining input")
)

// FieldError annotates a low-level error with the field number being decoded.
type FieldError struct {
	Field int
	Op    string
	Err   error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("wire: field %d: %s: %v", e.Field, e.Op, e.Err)
}

// maxVarintBytes is the longest legal encoding of a 64-bit varint.
const maxVarintBytes = 10

// ConsumeVarint decodes one varint from the front of buf.
// It returns the value and the number of bytes consumed.
// Bounds-safe: never reads beyond len(buf); reports ErrTruncated when the
// buffer ends mid-varint and ErrOverflow for >64-bit encodings.
func ConsumeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf) && i < maxVarintBytes; i++ {
		b := buf[i]
		if i == maxVarintBytes-1 {
			// 10th byte may only contribute the single lowest bit.
			if b > 1 {
				return 0, 0, ErrOverflow
			}
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(buf) < maxVarintBytes {
		return 0, 0, ErrTruncated
	}
	return 0, 0, ErrOverflow
}

// AppendVarint appends the canonical varint encoding of v to dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// VarintLen returns the encoded length of v in bytes.
func VarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ZigZagEncode maps a signed integer to an unsigned one so that small
// magnitudes (negative or positive) stay small: 0->0, -1->1, 1->2, ...
func ZigZagEncode(x int64) uint64 {
	return uint64(x<<1) ^ uint64(x>>63)
}

// ZigZagDecode is the inverse of ZigZagEncode.
func ZigZagDecode(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// ConsumeFixed32 reads 4 little-endian bytes. Bounds-safe.
func ConsumeFixed32(buf []byte) (uint32, int, error) {
	if len(buf) < 4 {
		return 0, 0, ErrTruncated
	}
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24, 4, nil
}

// ConsumeFixed64 reads 8 little-endian bytes. Bounds-safe.
func ConsumeFixed64(buf []byte) (uint64, int, error) {
	if len(buf) < 8 {
		return 0, 0, ErrTruncated
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(buf[i]) << (8 * uint(i))
	}
	return v, 8, nil
}

// AppendFixed32 appends 4 little-endian bytes.
func AppendFixed32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends 8 little-endian bytes.
func AppendFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// Field is one decoded protobuf field. Exactly one of the value slots is
// meaningful, selected by Type.
type Field struct {
	Number  int
	Type    WireType
	Varint  uint64 // WireVarint
	Fixed32 uint32 // WireFixed32
	Fixed64 uint64 // WireFixed64
	Bytes   []byte // WireBytes: sub-slice of the input, not copied
}

// Key returns the encoded field key (number<<3 | type).
func Key(number int, t WireType) uint64 {
	return uint64(number)<<3 | uint64(t)
}

// ParseMessage decodes every top-level field of a protobuf message in buf.
// Nested messages are returned as WireBytes fields and may be parsed
// recursively. Every read is bounds-checked; truncation is reported with
// field context via *FieldError.
func ParseMessage(buf []byte) ([]Field, error) {
	var fields []Field
	for len(buf) > 0 {
		key, n, err := ConsumeVarint(buf)
		if err != nil {
			return fields, &FieldError{Field: -1, Op: "read key", Err: err}
		}
		buf = buf[n:]
		number := int(key >> 3)
		wt := WireType(key & 0x7)
		if number <= 0 {
			return fields, &FieldError{Field: number, Op: "read key", Err: ErrInvalidFieldNumber}
		}
		f := Field{Number: number, Type: wt}
		switch wt {
		case WireVarint:
			v, n, err := ConsumeVarint(buf)
			if err != nil {
				return fields, &FieldError{Field: number, Op: "read varint", Err: err}
			}
			f.Varint = v
			buf = buf[n:]
		case WireFixed64:
			v, n, err := ConsumeFixed64(buf)
			if err != nil {
				return fields, &FieldError{Field: number, Op: "read fixed64", Err: err}
			}
			f.Fixed64 = v
			buf = buf[n:]
		case WireBytes:
			l, n, err := ConsumeVarint(buf)
			if err != nil {
				return fields, &FieldError{Field: number, Op: "read length", Err: err}
			}
			buf = buf[n:]
			if l > uint64(len(buf)) {
				return fields, &FieldError{Field: number, Op: "read bytes", Err: ErrLengthOverflow}
			}
			f.Bytes = buf[:l]
			buf = buf[l:]
		case WireFixed32:
			v, n, err := ConsumeFixed32(buf)
			if err != nil {
				return fields, &FieldError{Field: number, Op: "read fixed32", Err: err}
			}
			f.Fixed32 = v
			buf = buf[n:]
		default:
			return fields, &FieldError{Field: number, Op: "read key", Err: ErrInvalidWireType}
		}
		fields = append(fields, f)
	}
	return fields, nil
}
