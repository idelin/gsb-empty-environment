// Package wirecodec implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format encoder/decoder.
//
// Supported wire types:
//   - Varint (7-bit compressed unsigned integers, type 0)
//   - Fixed64 (type 1)
//   - Length-delimited (strings, bytes, nested messages, packed repeated, type 2)
//   - Fixed32 (type 5)
//
// ZigZag transforms are provided for signed integers (sint32/sint64).
// The decoder performs strict bounds checking on every read, guards against
// varint overflow and hostile length prefixes, and never panics on malformed
// input. Length-delimited payloads are returned as zero-copy sub-slices of the
// input buffer (capacity-capped), which makes decoding safe for concurrent use
// as long as the input buffer is not mutated.
package wirecodec

import (
	"encoding/binary"
	"errors"
	"math"
)

// Protobuf wire types.
const (
	WireVarint  = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 = 1 // fixed64, sfixed64, double
	WireBytes   = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32 = 5 // fixed32, sfixed32, float
)

// MaxVarintLen is the maximum encoded length of a 64-bit varint.
const MaxVarintLen = 10

// MaxFieldNum is the largest legal protobuf field number (2^29 - 1).
const MaxFieldNum = 1<<29 - 1

var (
	// ErrTruncated is returned when the input ends before a value is complete.
	ErrTruncated = errors.New("wirecodec: truncated input, read would go out of bounds")
	// ErrOverflow is returned when a varint exceeds 64 bits.
	ErrOverflow = errors.New("wirecodec: varint overflows a 64-bit integer")
	// ErrBadWireType is returned for reserved/unsupported wire types (3, 4, 6, 7)
	// or when a field is read with an unexpected wire type.
	ErrBadWireType = errors.New("wirecodec: unsupported or mismatched wire type")
	// ErrInvalidTag is returned for field number 0 or numbers above MaxFieldNum.
	ErrInvalidTag = errors.New("wirecodec: invalid field number in tag")
)

// ---------------------------------------------------------------------------
// Scalar helpers
// ---------------------------------------------------------------------------

// EncodeZigZag maps a signed integer to an unsigned one so that small-magnitude
// negatives encode small: 0->0, -1->1, 1->2, -2->3, ...
func EncodeZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// DecodeZigZag is the inverse of EncodeZigZag.
func DecodeZigZag(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// AppendVarint appends v to dst using 7-bit-per-byte LEB128 compression and
// returns the extended slice.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendTag appends the tag for (fieldNum, wireType) to dst.
func AppendTag(dst []byte, fieldNum, wireType int) []byte {
	return AppendVarint(dst, uint64(fieldNum)<<3|uint64(wireType))
}

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// ---------------------------------------------------------------------------
// Reader: bounds-checked, zero-copy deserialization
// ---------------------------------------------------------------------------

// Reader parses a protobuf wire stream. It is a pure cursor over the input
// buffer: it never mutates and never retains state beyond an offset, so
// independent Readers over a shared read-only buffer are safe for concurrent
// use. Length-delimited reads return zero-copy sub-slices of the input.
type Reader struct {
	buf []byte
	off int
}

// NewReader returns a Reader over buf. buf must not be mutated while in use.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.off }

// Done reports whether the stream has been fully consumed.
func (r *Reader) Done() bool { return r.off == len(r.buf) }

// Offset returns the current read position (useful for zero-copy assertions).
func (r *Reader) Offset() int { return r.off }

// Varint reads one LEB128-encoded unsigned 64-bit integer.
// It fails with ErrTruncated if the buffer ends mid-varint and with
// ErrOverflow if the encoding exceeds 64 bits (more than 10 bytes, or a
// 10th byte greater than 1).
func (r *Reader) Varint() (uint64, error) {
	var v uint64
	for i := 0; i < MaxVarintLen; i++ {
		if r.off >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.off]
		r.off++
		if i == MaxVarintLen-1 {
			// 10th byte may only contribute the single top bit.
			if b > 1 {
				return 0, ErrOverflow
			}
			v |= uint64(b) << 63
			return v, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ZigZag reads a varint and applies the ZigZag inverse transform (sint64).
func (r *Reader) ZigZag() (int64, error) {
	v, err := r.Varint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag(v), nil
}

// Bool reads a varint and interprets any non-zero value as true.
func (r *Reader) Bool() (bool, error) {
	v, err := r.Varint()
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

// Fixed32 reads 4 little-endian bytes.
func (r *Reader) Fixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v, nil
}

// Fixed64 reads 8 little-endian bytes.
func (r *Reader) Fixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.off:])
	r.off += 8
	return v, nil
}

// Float reads a fixed32 and reinterprets it as float32.
func (r *Reader) Float() (float32, error) {
	v, err := r.Fixed32()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(v), nil
}

// Double reads a fixed64 and reinterprets it as float64.
func (r *Reader) Double() (float64, error) {
	v, err := r.Fixed64()
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(v), nil
}

// Bytes reads a length-delimited payload and returns it as a ZERO-COPY
// sub-slice of the input buffer. The returned slice is capacity-capped so
// appends cannot scribble over the remaining stream. A hostile length prefix
// larger than the remaining input yields ErrTruncated, never a panic.
func (r *Reader) Bytes() ([]byte, error) {
	n, err := r.Varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, ErrTruncated
	}
	out := r.buf[r.off : r.off+int(n) : r.off+int(n)]
	r.off += int(n)
	return out, nil
}

// String reads a length-delimited payload as a string. The conversion copies;
// use Bytes for zero-copy access.
func (r *Reader) String() (string, error) {
	b, err := r.Bytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Tag reads a field tag and returns the field number and wire type.
func (r *Reader) Tag() (fieldNum, wireType int, err error) {
	v, err := r.Varint()
	if err != nil {
		return 0, 0, err
	}
	fieldNum = int(v >> 3)
	wireType = int(v & 0x7)
	if fieldNum <= 0 || fieldNum > MaxFieldNum {
		return 0, 0, ErrInvalidTag
	}
	return fieldNum, wireType, nil
}

// Skip consumes one field of the given wire type, discarding its value.
// This is what gives decoding forward compatibility: fields written by a
// newer schema are skipped cleanly. Unknown/reserved wire types are rejected.
func (r *Reader) Skip(wireType int) error {
	switch wireType {
	case WireVarint:
		_, err := r.Varint()
		return err
	case WireFixed64:
		return r.skipN(8)
	case WireBytes:
		_, err := r.Bytes()
		return err
	case WireFixed32:
		return r.skipN(4)
	default:
		return ErrBadWireType
	}
}

func (r *Reader) skipN(n int) error {
	if r.Remaining() < n {
		return ErrTruncated
	}
	r.off += n
	return nil
}

// ---------------------------------------------------------------------------
// Writer: compact binary encoding
// ---------------------------------------------------------------------------

// Writer accumulates an encoded protobuf stream.
type Writer struct {
	buf []byte
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// Bytes returns the encoded stream.
func (w *Writer) Bytes() []byte { return w.buf }

// Len returns the current encoded length.
func (w *Writer) Len() int { return len(w.buf) }

// Reset discards the encoded stream, keeping capacity for reuse.
func (w *Writer) Reset() { w.buf = w.buf[:0] }

// Tag writes the tag for (fieldNum, wireType).
func (w *Writer) Tag(fieldNum, wireType int) {
	w.buf = AppendTag(w.buf, fieldNum, wireType)
}

// Varint writes a raw varint (no tag).
func (w *Writer) Varint(v uint64) { w.buf = AppendVarint(w.buf, v) }

// ZigZag writes a ZigZag-transformed signed integer (no tag).
func (w *Writer) ZigZag(v int64) { w.buf = AppendVarint(w.buf, EncodeZigZag(v)) }

// Fixed32 writes 4 little-endian bytes (no tag).
func (w *Writer) Fixed32(v uint32) { w.buf = AppendFixed32(w.buf, v) }

// Fixed64 writes 8 little-endian bytes (no tag).
func (w *Writer) Fixed64(v uint64) { w.buf = AppendFixed64(w.buf, v) }

// WriteBytes writes a length-delimited payload (no tag).
func (w *Writer) WriteBytes(b []byte) {
	w.buf = AppendVarint(w.buf, uint64(len(b)))
	w.buf = append(w.buf, b...)
}

// WriteString writes a length-delimited string (no tag).
func (w *Writer) WriteString(s string) {
	w.buf = AppendVarint(w.buf, uint64(len(s)))
	w.buf = append(w.buf, s...)
}

// --- tagged convenience writers ---

// Uint64Field writes fieldNum as a varint field.
func (w *Writer) Uint64Field(fieldNum int, v uint64) {
	w.Tag(fieldNum, WireVarint)
	w.Varint(v)
}

// Sint64Field writes fieldNum as a ZigZag varint field.
func (w *Writer) Sint64Field(fieldNum int, v int64) {
	w.Tag(fieldNum, WireVarint)
	w.ZigZag(v)
}

// Fixed32Field writes fieldNum as a fixed32 field.
func (w *Writer) Fixed32Field(fieldNum int, v uint32) {
	w.Tag(fieldNum, WireFixed32)
	w.Fixed32(v)
}

// Fixed64Field writes fieldNum as a fixed64 field.
func (w *Writer) Fixed64Field(fieldNum int, v uint64) {
	w.Tag(fieldNum, WireFixed64)
	w.Fixed64(v)
}

// DoubleField writes fieldNum as a fixed64 float field.
func (w *Writer) DoubleField(fieldNum int, v float64) {
	w.Fixed64Field(fieldNum, math.Float64bits(v))
}

// StringField writes fieldNum as a length-delimited string field.
func (w *Writer) StringField(fieldNum int, s string) {
	w.Tag(fieldNum, WireBytes)
	w.WriteString(s)
}

// BytesField writes fieldNum as a length-delimited bytes field.
func (w *Writer) BytesField(fieldNum int, b []byte) {
	w.Tag(fieldNum, WireBytes)
	w.WriteBytes(b)
}

// MessageField writes fieldNum as an embedded length-delimited message.
func (w *Writer) MessageField(fieldNum int, encoded []byte) {
	w.Tag(fieldNum, WireBytes)
	w.WriteBytes(encoded)
}
