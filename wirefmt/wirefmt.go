// Package wirefmt implements a zero-dependency, hand-written Protocol
// Buffers binary wire-format parser. It supports varint (7-bit groups),
// ZigZag signed integers, fixed32/fixed64 and nested length-delimited
// messages, with strict bounds checking so truncated input is reported
// as an error instead of causing an out-of-bounds access.
package wirefmt

import (
	"encoding/binary"
	"errors"
)

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	WireVarint          WireType = 0
	WireFixed64         WireType = 1
	WireLengthDelimited WireType = 2
	WireFixed32         WireType = 5
)

var (
	ErrTruncated       = errors.New("wirefmt: unexpected end of input (truncated field)")
	ErrVarintOverflow  = errors.New("wirefmt: varint exceeds 64 bits")
	ErrInvalidWireType = errors.New("wirefmt: unsupported wire type")
	ErrInvalidFieldNum = errors.New("wirefmt: field number out of range")
)

// MaxFieldNum is the largest field number allowed by the protobuf spec.
const MaxFieldNum = 1<<29 - 1

// Reader parses a protobuf wire-format buffer with safe bounds checking.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf. buf is not copied.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Len reports how many unread bytes remain.
func (r *Reader) Len() int { return len(r.buf) - r.pos }

// EOF reports whether the buffer has been fully consumed.
func (r *Reader) EOF() bool { return r.pos >= len(r.buf) }

// ReadVarint reads a base-128 varint (7 bits per byte, LSB first).
// It consumes at most 10 bytes and rejects values overflowing 64 bits.
func (r *Reader) ReadVarint() (uint64, error) {
	var x uint64
	for i := 0; i < 10; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == 9 && b > 1 {
			return 0, ErrVarintOverflow
		}
		x |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return x, nil
		}
	}
	return 0, ErrVarintOverflow
}

// ReadSint32 reads a varint and ZigZag-decodes it to int32.
func (r *Reader) ReadSint32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag32(uint32(v)), nil
}

// ReadSint64 reads a varint and ZigZag-decodes it to int64.
func (r *Reader) ReadSint64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag64(v), nil
}

// ReadFixed32 reads 4 little-endian bytes.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Len() < 4 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// ReadFixed64 reads 8 little-endian bytes.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Len() < 8 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

// ReadBytes reads a length-delimited byte slice. The returned slice
// aliases the underlying buffer.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Len()) {
		return nil, ErrTruncated
	}
	out := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

// ReadString reads a length-delimited UTF-8 string.
func (r *Reader) ReadString() (string, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReadTag reads a field key: field number shifted left by 3, OR'd with
// the wire type.
func (r *Reader) ReadTag() (fieldNum int, wt WireType, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wt = WireType(v & 0x7)
	fieldNum = int(v >> 3)
	if fieldNum <= 0 || fieldNum > MaxFieldNum {
		return 0, 0, ErrInvalidFieldNum
	}
	switch wt {
	case WireVarint, WireFixed64, WireLengthDelimited, WireFixed32:
	default:
		return 0, 0, ErrInvalidWireType
	}
	return fieldNum, wt, nil
}

// ReadMessage reads a length-delimited nested message and returns a
// sub-Reader scoped to exactly its bytes.
func (r *Reader) ReadMessage() (*Reader, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return nil, err
	}
	return NewReader(b), nil
}

// Skip discards one field value of the given wire type.
func (r *Reader) Skip(wt WireType) error {
	switch wt {
	case WireVarint:
		_, err := r.ReadVarint()
		return err
	case WireFixed64:
		_, err := r.ReadFixed64()
		return err
	case WireFixed32:
		_, err := r.ReadFixed32()
		return err
	case WireLengthDelimited:
		_, err := r.ReadBytes()
		return err
	default:
		return ErrInvalidWireType
	}
}

// ReadPackedVarints decodes a packed repeated varint field.
func (r *Reader) ReadPackedVarints() ([]uint64, error) {
	sub, err := r.ReadMessage()
	if err != nil {
		return nil, err
	}
	var out []uint64
	for !sub.EOF() {
		v, err := sub.ReadVarint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ReadPackedSint32s decodes a packed repeated sint32 field.
func (r *Reader) ReadPackedSint32s() ([]int32, error) {
	sub, err := r.ReadMessage()
	if err != nil {
		return nil, err
	}
	var out []int32
	for !sub.EOF() {
		v, err := sub.ReadSint32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ReadPackedFixed32s decodes a packed repeated fixed32 field.
func (r *Reader) ReadPackedFixed32s() ([]uint32, error) {
	sub, err := r.ReadMessage()
	if err != nil {
		return nil, err
	}
	if sub.Len()%4 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint32, 0, sub.Len()/4)
	for !sub.EOF() {
		v, err := sub.ReadFixed32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ReadPackedFixed64s decodes a packed repeated fixed64 field.
func (r *Reader) ReadPackedFixed64s() ([]uint64, error) {
	sub, err := r.ReadMessage()
	if err != nil {
		return nil, err
	}
	if sub.Len()%8 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint64, 0, sub.Len()/8)
	for !sub.EOF() {
		v, err := sub.ReadFixed64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
