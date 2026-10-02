// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format encoder and decoder.
//
// Supported wire types:
//   - Varint (0): 7-bit-per-byte compressed unsigned integers
//   - Fixed64 (1): 8-byte little-endian values
//   - Length-delimited (2): nested messages, strings, packed repeated fields
//   - Fixed32 (5): 4-byte little-endian values
//
// All decoding is bounds-checked: truncated input is reported as ErrTruncated
// and malformed input as dedicated sentinel errors. The decoder never reads
// out of range and never panics on hostile input.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Wire types as defined by the Protocol Buffers binary encoding.
type Type int

const (
	TypeVarint  Type = 0
	TypeFixed64 Type = 1
	TypeBytes   Type = 2
	TypeFixed32 Type = 5
)

// Sentinel errors returned by the decoder.
var (
	// ErrTruncated reports that the input ended before a complete element
	// (tag, varint, fixed value or length-delimited payload) could be read.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow reports a varint longer than 10 bytes or with its 10th
	// byte carrying more than one payload bit (does not fit in uint64).
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrInvalidWireType reports a tag whose wire type is not 0, 1, 2 or 5.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidFieldNumber reports a tag with field number zero.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number 0")
)

// MaxVarintBytes is the maximum encoded length of a 64-bit varint.
const MaxVarintBytes = 10

// ---------------------------------------------------------------------------
// Varint primitives
// ---------------------------------------------------------------------------

// AppendVarint appends v to buf using 7-bit-per-byte little-endian-group
// varint encoding and returns the extended buffer.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// ConsumeVarint decodes a varint from the front of buf and returns the value
// and the number of bytes consumed. It returns ErrTruncated if buf ends
// mid-varint and ErrOverflow if the varint does not fit in 64 bits.
func ConsumeVarint(buf []byte) (v uint64, n int, err error) {
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
		// 10 continuation bytes seen, still unterminated.
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrTruncated
}

// ---------------------------------------------------------------------------
// ZigZag (signed integer) mapping
// ---------------------------------------------------------------------------

// EncodeZigZag maps a signed integer to an unsigned one so that numbers with
// small absolute value stay small: 0->0, -1->1, 1->2, -2->3, ...
func EncodeZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// DecodeZigZag is the inverse of EncodeZigZag.
func DecodeZigZag(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// AppendZigZag appends the varint-encoded ZigZag form of v.
func AppendZigZag(buf []byte, v int64) []byte {
	return AppendVarint(buf, EncodeZigZag(v))
}

// ---------------------------------------------------------------------------
// Fixed-width primitives (little-endian)
// ---------------------------------------------------------------------------

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

// ConsumeFixed32 reads 4 little-endian bytes from the front of buf.
func ConsumeFixed32(buf []byte) (uint32, int, error) {
	if len(buf) < 4 {
		return 0, 0, ErrTruncated
	}
	return binary.LittleEndian.Uint32(buf), 4, nil
}

// ConsumeFixed64 reads 8 little-endian bytes from the front of buf.
func ConsumeFixed64(buf []byte) (uint64, int, error) {
	if len(buf) < 8 {
		return 0, 0, ErrTruncated
	}
	return binary.LittleEndian.Uint64(buf), 8, nil
}

// ---------------------------------------------------------------------------
// Tags and length-delimited payloads
// ---------------------------------------------------------------------------

// AppendTag appends the tag (field number + wire type) for a field.
func AppendTag(buf []byte, fieldNum int, typ Type) []byte {
	return AppendVarint(buf, uint64(fieldNum)<<3|uint64(typ))
}

// AppendBytes appends a length-delimited payload (length prefix + data).
func AppendBytes(buf, data []byte) []byte {
	buf = AppendVarint(buf, uint64(len(data)))
	return append(buf, data...)
}

// ConsumeBytes reads a length-delimited payload from the front of buf.
// The returned slice aliases buf.
func ConsumeBytes(buf []byte) ([]byte, int, error) {
	l, n, err := ConsumeVarint(buf)
	if err != nil {
		return nil, 0, err
	}
	if l > uint64(len(buf)-n) {
		return nil, 0, ErrTruncated
	}
	return buf[n : n+int(l)], n + int(l), nil
}

// ---------------------------------------------------------------------------
// Streaming decoder
// ---------------------------------------------------------------------------

// Field is one decoded field of a message. Exactly one of the value members
// is meaningful, selected by Type.
type Field struct {
	Number  int
	Type    Type
	Varint  uint64
	Fixed32 uint32
	Fixed64 uint64
	Bytes   []byte // aliases the decoder's input buffer
}

// Svarint returns the field's varint value mapped back through ZigZag.
func (f Field) Svarint() int64 { return DecodeZigZag(f.Varint) }

// Decoder walks a buffer field by field. The zero value is not usable;
// construct one with NewDecoder.
type Decoder struct {
	buf []byte
	off int
}

// NewDecoder returns a Decoder reading from buf.
func NewDecoder(buf []byte) *Decoder { return &Decoder{buf: buf} }

// Done reports whether the buffer has been fully consumed.
func (d *Decoder) Done() bool { return d.off >= len(d.buf) }

// Next decodes the next field. It returns io.EOF-style completion via
// Done(); callers should check Done before calling Next. Every error return
// is one of the package's sentinel errors, wrapped with the byte offset.
func (d *Decoder) Next() (Field, error) {
	var f Field
	tag, n, err := ConsumeVarint(d.buf[d.off:])
	if err != nil {
		return f, d.wrapErr(err)
	}
	d.off += n
	f.Number = int(tag >> 3)
	f.Type = Type(tag & 7)
	if f.Number == 0 {
		return f, d.wrapErr(ErrInvalidFieldNumber)
	}
	switch f.Type {
	case TypeVarint:
		v, m, err := ConsumeVarint(d.buf[d.off:])
		if err != nil {
			return f, d.wrapErr(err)
		}
		d.off += m
		f.Varint = v
	case TypeFixed64:
		v, m, err := ConsumeFixed64(d.buf[d.off:])
		if err != nil {
			return f, d.wrapErr(err)
		}
		d.off += m
		f.Fixed64 = v
	case TypeBytes:
		b, m, err := ConsumeBytes(d.buf[d.off:])
		if err != nil {
			return f, d.wrapErr(err)
		}
		d.off += m
		f.Bytes = b
	case TypeFixed32:
		v, m, err := ConsumeFixed32(d.buf[d.off:])
		if err != nil {
			return f, d.wrapErr(err)
		}
		d.off += m
		f.Fixed32 = v
	default:
		return f, d.wrapErr(ErrInvalidWireType)
	}
	return f, nil
}

func (d *Decoder) wrapErr(err error) error {
	return fmt.Errorf("wire: at offset %d: %v", d.off, err)
}

// ParseMessage decodes all fields of buf into a slice.
func ParseMessage(buf []byte) ([]Field, error) {
	d := NewDecoder(buf)
	var out []Field
	for !d.Done() {
		f, err := d.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Packed repeated fields
// ---------------------------------------------------------------------------

// DecodePackedVarints decodes a packed repeated varint payload.
func DecodePackedVarints(buf []byte) ([]uint64, error) {
	var out []uint64
	for len(buf) > 0 {
		v, n, err := ConsumeVarint(buf)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		buf = buf[n:]
	}
	return out, nil
}

// DecodePackedSvarints decodes a packed repeated sint payload (ZigZag).
func DecodePackedSvarints(buf []byte) ([]int64, error) {
	us, err := DecodePackedVarints(buf)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = DecodeZigZag(u)
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload. The payload
// length must be a multiple of 4.
func DecodePackedFixed32(buf []byte) ([]uint32, error) {
	if len(buf)%4 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint32, 0, len(buf)/4)
	for len(buf) > 0 {
		out = append(out, binary.LittleEndian.Uint32(buf))
		buf = buf[4:]
	}
	return out, nil
}

// DecodePackedFixed64 decodes a packed repeated fixed64 payload. The payload
// length must be a multiple of 8.
func DecodePackedFixed64(buf []byte) ([]uint64, error) {
	if len(buf)%8 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint64, 0, len(buf)/8)
	for len(buf) > 0 {
		out = append(out, binary.LittleEndian.Uint64(buf))
		buf = buf[8:]
	}
	return out, nil
}

// AppendPackedVarints encodes values as a packed varint payload (no tag).
func AppendPackedVarints(buf []byte, vs ...uint64) []byte {
	for _, v := range vs {
		buf = AppendVarint(buf, v)
	}
	return buf
}

// AppendPackedFixed32 encodes values as a packed fixed32 payload (no tag).
func AppendPackedFixed32(buf []byte, vs ...uint32) []byte {
	for _, v := range vs {
		buf = AppendFixed32(buf, v)
	}
	return buf
}

// AppendPackedFixed64 encodes values as a packed fixed64 payload (no tag).
func AppendPackedFixed64(buf []byte, vs ...uint64) []byte {
	for _, v := range vs {
		buf = AppendFixed64(buf, v)
	}
	return buf
}

// Float32/Float64 convenience helpers for fixed-width fields.

func (f Field) Float32() float32 { return math.Float32frombits(f.Fixed32) }
func (f Field) Float64() float64 { return math.Float64frombits(f.Fixed64) }
