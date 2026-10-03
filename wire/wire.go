// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format codec: varint (7-bit groups), ZigZag signed integers,
// fixed32/fixed64 and nested length-delimited messages. Every read is
// bounds-checked so truncated input is reported, never panics.
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
	Varint      Type = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	Fixed64     Type = 1 // fixed64, sfixed64, double
	LengthDelim Type = 2 // string, bytes, embedded messages, packed repeated
	Fixed32     Type = 5 // fixed32, sfixed32, float
)

func (t Type) String() string {
	switch t {
	case Varint:
		return "varint"
	case Fixed64:
		return "fixed64"
	case LengthDelim:
		return "length-delimited"
	case Fixed32:
		return "fixed32"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

var (
	// ErrTruncated is returned when the buffer ends before a field is complete.
	ErrTruncated = errors.New("wire: truncated input, buffer ended mid-field")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than 10
	// bytes, or a 10th byte carrying more than one payload bit).
	ErrOverflow = errors.New("wire: varint overflows 64-bit integer")
	// ErrInvalidTag is returned for a tag with field number 0 or a reserved wire type.
	ErrInvalidTag = errors.New("wire: invalid tag")
)

// Reader consumes a protobuf wire stream with strict bounds checking.
type Reader struct {
	buf []byte
	pos int
}

func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Done reports whether the stream has been fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// ReadVarint decodes a base-128 varint (7 payload bits per byte, LSB first).
func (r *Reader) ReadVarint() (uint64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == 9 {
			// 10th byte may carry at most 1 payload bit.
			if b > 1 {
				return 0, ErrOverflow
			}
			v |= uint64(b) << 63
			return v, nil
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag decodes a ZigZag-mapped signed 64-bit integer (sint64).
func (r *Reader) ReadZigZag() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int64(v>>1) ^ -int64(v&1), nil
}

// ReadFixed32 reads 4 bytes little-endian.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		r.pos = len(r.buf)
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// ReadFixed64 reads 8 bytes little-endian.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		r.pos = len(r.buf)
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

// ReadBytes reads a length-delimited byte slice. The returned slice aliases
// the underlying buffer; copy it if it must outlive the buffer.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		r.pos = len(r.buf)
		return nil, fmt.Errorf("%w: length-delimited field declares %d bytes, only %d remain",
			ErrTruncated, n, r.Remaining())
	}
	p := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return p, nil
}

// ReadTag reads a field key and splits it into field number and wire type.
func (r *Reader) ReadTag() (fieldNum int, wt Type, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wt = Type(v & 0x7)
	fieldNum = int(v >> 3)
	if fieldNum <= 0 {
		return 0, 0, fmt.Errorf("%w: field number %d", ErrInvalidTag, fieldNum)
	}
	switch wt {
	case Varint, Fixed64, LengthDelim, Fixed32:
		return fieldNum, wt, nil
	default:
		return 0, 0, fmt.Errorf("%w: unsupported wire type %d", ErrInvalidTag, wt)
	}
}

// Skip discards one field value of the given wire type (unknown-field handling).
func (r *Reader) Skip(wt Type) error {
	switch wt {
	case Varint:
		_, err := r.ReadVarint()
		return err
	case Fixed64:
		_, err := r.ReadFixed64()
		return err
	case LengthDelim:
		_, err := r.ReadBytes()
		return err
	case Fixed32:
		_, err := r.ReadFixed32()
		return err
	default:
		return fmt.Errorf("%w: cannot skip wire type %d", ErrInvalidTag, wt)
	}
}

// EncodeZigZag maps a signed integer to its ZigZag unsigned form.
func EncodeZigZag(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// DecodeZigZag is the inverse of EncodeZigZag.
func DecodeZigZag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// Buffer is a protobuf wire-format encoder.
type Buffer struct{ buf []byte }

func NewBuffer() *Buffer { return &Buffer{} }

func (b *Buffer) Bytes() []byte { return b.buf }

func (b *Buffer) AppendVarint(v uint64) {
	b.buf = binary.AppendUvarint(b.buf, v)
}

func (b *Buffer) AppendZigZag(v int64) { b.AppendVarint(EncodeZigZag(v)) }

func (b *Buffer) AppendFixed32(v uint32) {
	var p [4]byte
	binary.LittleEndian.PutUint32(p[:], v)
	b.buf = append(b.buf, p[:]...)
}

func (b *Buffer) AppendFixed64(v uint64) {
	var p [8]byte
	binary.LittleEndian.PutUint64(p[:], v)
	b.buf = append(b.buf, p[:]...)
}

func (b *Buffer) AppendFloat32(f float32) { b.AppendFixed32(math.Float32bits(f)) }
func (b *Buffer) AppendFloat64(f float64) { b.AppendFixed64(math.Float64bits(f)) }

// AppendTag writes a field key (field number + wire type).
func (b *Buffer) AppendTag(fieldNum int, wt Type) {
	b.AppendVarint(uint64(fieldNum)<<3 | uint64(wt))
}

// AppendBytes writes a length-delimited payload.
func (b *Buffer) AppendBytes(p []byte) {
	b.AppendVarint(uint64(len(p)))
	b.buf = append(b.buf, p...)
}
