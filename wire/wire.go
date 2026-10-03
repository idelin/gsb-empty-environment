// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format parser and encoder.
//
// Supported wire types:
//   - Varint (7-bit compressed encoding, up to 64 bits)
//   - ZigZag signed integers (sint32/sint64)
//   - Fixed32 / Fixed64
//   - Length-delimited (strings, bytes, nested messages, packed repeated)
//
// Every read is bounds-checked: a truncated field never causes a panic or
// an out-of-bounds access, it returns ErrTruncated (or a wrapped variant).
package wire

import (
	"errors"
	"fmt"
)

// Protobuf wire types.
const (
	WireVarint  = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 = 1 // fixed64, sfixed64, double
	WireBytes   = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32 = 5 // fixed32, sfixed32, float
)

// Sentinel errors returned by the parser. They are comparable with ==.
var (
	// ErrTruncated is returned when the input ends in the middle of a field.
	ErrTruncated = errors.New("wire: truncated input, unexpected end of buffer")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than
	// 10 bytes, or a 10th byte greater than 1).
	ErrOverflow = errors.New("wire: varint overflows a 64-bit integer")
	// ErrInvalidWireType is returned for wire types outside {0,1,2,5}.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidTag is returned for a field number of zero or a tag that
	// exceeds the 29-bit field-number space.
	ErrInvalidTag = errors.New("wire: invalid field tag")
	// ErrNegativeLength is returned when a length prefix is malformed.
	ErrNegativeLength = errors.New("wire: invalid length prefix")
)

// MaxVarintBytes is the maximum encoded length of a 64-bit varint.
const MaxVarintBytes = 10

// maxFieldNumber is the largest legal protobuf field number (2^29 - 1).
const maxFieldNumber = 1<<29 - 1

// ---------------------------------------------------------------------------
// ZigZag transforms
// ---------------------------------------------------------------------------

// ZigZag64 maps a signed integer to an unsigned one so that small-magnitude
// negatives encode to small varints: 0->0, -1->1, 1->2, -2->3, ...
func ZigZag64(x int64) uint64 { return uint64(x<<1) ^ uint64(x>>63) }

// UnZigZag64 is the inverse of ZigZag64.
func UnZigZag64(x uint64) int64 { return int64(x>>1) ^ -int64(x&1) }

// ZigZag32 is the 32-bit ZigZag transform.
func ZigZag32(x int32) uint32 { return uint32(x<<1) ^ uint32(x>>31) }

// UnZigZag32 is the inverse of ZigZag32.
func UnZigZag32(x uint32) int32 { return int32(x>>1) ^ -int32(x&1) }

// ---------------------------------------------------------------------------
// Reader: bounds-checked decoder
// ---------------------------------------------------------------------------

// Reader decodes a protobuf wire stream from a byte slice. It is not safe
// for concurrent use; it performs no allocation except on ReadBytesCopy.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Offset reports the current read position (useful for error reporting).
func (r *Reader) Offset() int { return r.pos }

// Done reports whether the buffer has been fully consumed.
func (r *Reader) Done() bool { return r.pos == len(r.buf) }

// ReadVarint reads a base-128 varint (7 bits per byte, little-endian groups).
// It consumes at most MaxVarintBytes bytes and rejects overlong encodings.
func (r *Reader) ReadVarint() (uint64, error) {
	var x uint64
	var shift uint
	for i := 0; i < MaxVarintBytes; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == MaxVarintBytes-1 {
			// The 10th byte may only carry a single payload bit and must
			// terminate the varint; anything else overflows 64 bits.
			if b > 1 {
				return 0, ErrOverflow
			}
		}
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return x, nil
		}
		shift += 7
	}
	return 0, ErrOverflow
}

// ReadZigZag64 reads a varint and applies the inverse ZigZag transform.
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return UnZigZag64(v), nil
}

// ReadZigZag32 reads a varint and applies the inverse 32-bit ZigZag transform.
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return UnZigZag32(uint32(v)), nil
}

// ReadFixed32 reads 4 bytes in little-endian order.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		r.pos = len(r.buf) // consume the tail so callers observe a drained buffer
		return 0, ErrTruncated
	}
	p := r.buf[r.pos:]
	r.pos += 4
	return uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<16 | uint32(p[3])<<24, nil
}

// ReadFixed64 reads 8 bytes in little-endian order.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		r.pos = len(r.buf)
		return 0, ErrTruncated
	}
	p := r.buf[r.pos:]
	r.pos += 8
	return uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24 |
		uint64(p[4])<<32 | uint64(p[5])<<40 | uint64(p[6])<<48 | uint64(p[7])<<56, nil
}

// ReadBytes reads a varint length prefix followed by that many bytes.
// The returned slice aliases the input buffer. A declared length that runs
// past the end of the buffer yields ErrTruncated, never an out-of-bounds read.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		r.pos = len(r.buf)
		return nil, ErrTruncated
	}
	out := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

// ReadTag reads a field tag and returns the field number and wire type.
func (r *Reader) ReadTag() (fieldNum int, wireType int, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wireType = int(v & 0x7)
	fieldNum = int(v >> 3)
	switch wireType {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return 0, 0, ErrInvalidWireType
	}
	if fieldNum < 1 || fieldNum > maxFieldNumber {
		return 0, 0, ErrInvalidTag
	}
	return fieldNum, wireType, nil
}

// Skip consumes the value of a field with the given wire type without
// interpreting it. Unknown fields are skipped this way during reflection
// decoding; bounds are still fully checked.
func (r *Reader) Skip(wireType int) error {
	switch wireType {
	case WireVarint:
		_, err := r.ReadVarint()
		return err
	case WireFixed64:
		_, err := r.ReadFixed64()
		return err
	case WireBytes:
		_, err := r.ReadBytes()
		return err
	case WireFixed32:
		_, err := r.ReadFixed32()
		return err
	default:
		return ErrInvalidWireType
	}
}

// ---------------------------------------------------------------------------
// Buffer: encoder
// ---------------------------------------------------------------------------

// Buffer is a protobuf wire-format encoder.
type Buffer struct{ buf []byte }

// NewBuffer returns an empty encoder buffer.
func NewBuffer() *Buffer { return &Buffer{} }

// Bytes returns the encoded output. The slice aliases internal state and is
// invalidated by further appends.
func (b *Buffer) Bytes() []byte { return b.buf }

// Len returns the current encoded length.
func (b *Buffer) Len() int { return len(b.buf) }

// Reset truncates the buffer to zero length, keeping capacity.
func (b *Buffer) Reset() { b.buf = b.buf[:0] }

// AppendVarint encodes x as a base-128 varint.
func (b *Buffer) AppendVarint(x uint64) {
	for x >= 0x80 {
		b.buf = append(b.buf, byte(x)|0x80)
		x >>= 7
	}
	b.buf = append(b.buf, byte(x))
}

// AppendZigZag64 encodes a signed integer with the ZigZag transform.
func (b *Buffer) AppendZigZag64(x int64) { b.AppendVarint(ZigZag64(x)) }

// AppendZigZag32 encodes a signed 32-bit integer with the ZigZag transform.
func (b *Buffer) AppendZigZag32(x int32) { b.AppendVarint(uint64(ZigZag32(x))) }

// AppendFixed32 appends 4 little-endian bytes.
func (b *Buffer) AppendFixed32(x uint32) {
	b.buf = append(b.buf, byte(x), byte(x>>8), byte(x>>16), byte(x>>24))
}

// AppendFixed64 appends 8 little-endian bytes.
func (b *Buffer) AppendFixed64(x uint64) {
	b.buf = append(b.buf,
		byte(x), byte(x>>8), byte(x>>16), byte(x>>24),
		byte(x>>32), byte(x>>40), byte(x>>48), byte(x>>56))
}

// AppendTag appends a field tag (field number + wire type).
func (b *Buffer) AppendTag(fieldNum, wireType int) {
	b.AppendVarint(uint64(fieldNum)<<3 | uint64(wireType))
}

// AppendBytes appends a length-delimited byte string.
func (b *Buffer) AppendBytes(p []byte) {
	b.AppendVarint(uint64(len(p)))
	b.buf = append(b.buf, p...)
}

// AppendString appends a length-delimited string.
func (b *Buffer) AppendString(s string) { b.AppendBytes([]byte(s)) }

// ---------------------------------------------------------------------------
// Generic message walker
// ---------------------------------------------------------------------------

// Field is one decoded field of a message, produced by ParseMessage.
type Field struct {
	Number   int
	WireType int
	Varint   uint64 // valid when WireType == WireVarint
	Fixed32  uint32 // valid when WireType == WireFixed32
	Fixed64  uint64 // valid when WireType == WireFixed64
	Bytes    []byte // valid when WireType == WireBytes (aliases input)
}

// ParseMessage decodes every field of a wire-format message. Unknown field
// numbers are preserved, not an error. Any truncation or malformed tag
// aborts parsing with a descriptive error.
func ParseMessage(data []byte) ([]Field, error) {
	r := NewReader(data)
	var fields []Field
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return nil, fmt.Errorf("wire: field tag at offset %d: %v", r.Offset(), err)
		}
		f := Field{Number: num, WireType: wt}
		switch wt {
		case WireVarint:
			if f.Varint, err = r.ReadVarint(); err != nil {
				return nil, fmt.Errorf("wire: varint field %d at offset %d: %v", num, r.Offset(), err)
			}
		case WireFixed64:
			if f.Fixed64, err = r.ReadFixed64(); err != nil {
				return nil, fmt.Errorf("wire: fixed64 field %d at offset %d: %v", num, r.Offset(), err)
			}
		case WireBytes:
			if f.Bytes, err = r.ReadBytes(); err != nil {
				return nil, fmt.Errorf("wire: bytes field %d at offset %d: %v", num, r.Offset(), err)
			}
		case WireFixed32:
			if f.Fixed32, err = r.ReadFixed32(); err != nil {
				return nil, fmt.Errorf("wire: fixed32 field %d at offset %d: %v", num, r.Offset(), err)
			}
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// ---------------------------------------------------------------------------
// Packed repeated field helpers
// ---------------------------------------------------------------------------

// DecodePackedVarints decodes a packed repeated varint payload.
func DecodePackedVarints(data []byte) ([]uint64, error) {
	r := NewReader(data)
	var out []uint64
	for !r.Done() {
		v, err := r.ReadVarint()
		if err != nil {
			return nil, fmt.Errorf("wire: packed varint element at offset %d: %v", r.Offset(), err)
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload. The length
// must be a multiple of 4.
func DecodePackedFixed32(data []byte) ([]uint32, error) {
	if len(data)%4 != 0 {
		return nil, ErrTruncated
	}
	r := NewReader(data)
	out := make([]uint32, 0, len(data)/4)
	for !r.Done() {
		v, err := r.ReadFixed32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodePackedFixed64 decodes a packed repeated fixed64 payload. The length
// must be a multiple of 8.
func DecodePackedFixed64(data []byte) ([]uint64, error) {
	if len(data)%8 != 0 {
		return nil, ErrTruncated
	}
	r := NewReader(data)
	out := make([]uint64, 0, len(data)/8)
	for !r.Done() {
		v, err := r.ReadFixed64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// EncodePackedVarints encodes values as a packed repeated varint payload.
func EncodePackedVarints(vals []uint64) []byte {
	b := NewBuffer()
	for _, v := range vals {
		b.AppendVarint(v)
	}
	return b.Bytes()
}
