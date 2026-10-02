// Package wire implements a zero-dependency Protocol Buffers binary
// wire-format codec: varint (7-bit compressed) integers, ZigZag signed
// integers, fixed32/fixed64, and nested length-delimited messages.
// All decoders are bounds-checked and report truncation instead of
// reading out of range.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	Varint          WireType = 0
	Fixed64         WireType = 1
	LengthDelimited WireType = 2
	Fixed32         WireType = 5
)

// MaxDepth bounds recursion when validating nested messages.
const MaxDepth = 100

var (
	ErrTruncated   = errors.New("wire: truncated input")
	ErrOverflow    = errors.New("wire: varint overflows 64 bits")
	ErrBadWireType = errors.New("wire: unknown wire type")
	ErrZeroField   = errors.New("wire: field number 0 is invalid")
	ErrDepthLimit  = errors.New("wire: nesting depth limit exceeded")
)

// FieldError annotates a decode failure with the field number and the
// byte offset at which it happened.
type FieldError struct {
	Offset int
	Field  int
	Err    error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("wire: field %d at offset %d: %v", e.Field, e.Offset, e.Err)
}

func (e *FieldError) Unwrap() error { return e.Err }

// ---------------------------------------------------------------------------
// Varint (7-bit compressed) codec
// ---------------------------------------------------------------------------

// AppendVarint encodes v using 7 bits per byte, LSB first, continuation
// bit in the high bit.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// DecodeVarint decodes one varint from the head of buf, returning the
// value and the number of bytes consumed. It never reads past len(buf):
// short input yields ErrTruncated, an overlong encoding yields ErrOverflow.
func DecodeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf) && i < 10; i++ {
		b := buf[i]
		if i == 9 && b > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(buf) >= 10 {
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrTruncated
}

// ---------------------------------------------------------------------------
// ZigZag signed-integer transform
// ---------------------------------------------------------------------------

// ZigZagEncode maps signed integers to unsigned so small magnitudes
// (negative or positive) stay small: 0->0, -1->1, 1->2, -2->3, ...
func ZigZagEncode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// ZigZagDecode is the inverse of ZigZagEncode.
func ZigZagDecode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// ---------------------------------------------------------------------------
// Fixed-width codec (little endian)
// ---------------------------------------------------------------------------

func AppendFixed32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

func AppendFixed64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func DecodeFixed32(buf []byte) (uint32, error) {
	if len(buf) < 4 {
		return 0, ErrTruncated
	}
	return binary.LittleEndian.Uint32(buf), nil
}

func DecodeFixed64(buf []byte) (uint64, error) {
	if len(buf) < 8 {
		return 0, ErrTruncated
	}
	return binary.LittleEndian.Uint64(buf), nil
}

// ---------------------------------------------------------------------------
// Tags and length-delimited payloads
// ---------------------------------------------------------------------------

// AppendTag writes the key (field number + wire type) of a field.
func AppendTag(buf []byte, num int, wt WireType) []byte {
	return AppendVarint(buf, uint64(num)<<3|uint64(wt))
}

// AppendBytes writes a length-delimited payload (string, bytes, embedded
// message, or packed repeated field).
func AppendBytes(buf, payload []byte) []byte {
	buf = AppendVarint(buf, uint64(len(payload)))
	return append(buf, payload...)
}

// ---------------------------------------------------------------------------
// Message parsing
// ---------------------------------------------------------------------------

// Field is one decoded field of a message. Value holds the payload for
// Varint/Fixed32/Fixed64; Bytes holds the payload for LengthDelimited.
type Field struct {
	Number int
	Type   WireType
	Value  uint64
	Bytes  []byte
}

// Parse decodes every field of one message level. Nested messages are
// returned as LengthDelimited fields whose Bytes can be parsed again.
// Any truncation or malformed tag is reported as a *FieldError and no
// read ever goes past the end of buf.
func Parse(buf []byte) ([]Field, error) {
	var fields []Field
	off := 0
	for off < len(buf) {
		tagOff := off
		tag, n, err := DecodeVarint(buf[off:])
		if err != nil {
			return nil, &FieldError{Offset: tagOff, Err: err}
		}
		off += n
		num := int(tag >> 3)
		if num == 0 {
			return nil, &FieldError{Offset: tagOff, Err: ErrZeroField}
		}
		f := Field{Number: num, Type: WireType(tag & 7)}
		switch f.Type {
		case Varint:
			v, n, err := DecodeVarint(buf[off:])
			if err != nil {
				return nil, &FieldError{Offset: off, Field: num, Err: err}
			}
			f.Value = v
			off += n
		case Fixed64:
			v, err := DecodeFixed64(buf[off:])
			if err != nil {
				return nil, &FieldError{Offset: off, Field: num, Err: err}
			}
			f.Value = v
			off += 8
		case LengthDelimited:
			l, n, err := DecodeVarint(buf[off:])
			if err != nil {
				return nil, &FieldError{Offset: off, Field: num, Err: err}
			}
			off += n
			if l > uint64(len(buf)-off) {
				return nil, &FieldError{Offset: off, Field: num, Err: ErrTruncated}
			}
			f.Bytes = buf[off : off+int(l)]
			off += int(l)
		case Fixed32:
			v, err := DecodeFixed32(buf[off:])
			if err != nil {
				return nil, &FieldError{Offset: off, Field: num, Err: err}
			}
			f.Value = uint64(v)
			off += 4
		default:
			return nil, &FieldError{Offset: tagOff, Field: num, Err: ErrBadWireType}
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// ---------------------------------------------------------------------------
// Packed repeated arrays
// ---------------------------------------------------------------------------

// DecodePackedVarint decodes a packed repeated varint payload.
func DecodePackedVarint(b []byte) ([]uint64, error) {
	var out []uint64
	for len(b) > 0 {
		v, n, err := DecodeVarint(b)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload; the
// payload length must be a multiple of 4.
func DecodePackedFixed32(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint32, 0, len(b)/4)
	for len(b) > 0 {
		out = append(out, binary.LittleEndian.Uint32(b))
		b = b[4:]
	}
	return out, nil
}

// DecodePackedFixed64 decodes a packed repeated fixed64 payload; the
// payload length must be a multiple of 8.
func DecodePackedFixed64(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint64, 0, len(b)/8)
	for len(b) > 0 {
		out = append(out, binary.LittleEndian.Uint64(b))
		b = b[8:]
	}
	return out, nil
}
