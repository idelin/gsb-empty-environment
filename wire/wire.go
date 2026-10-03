// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format parser. It supports varint (7-bit compressed) encoding,
// ZigZag signed-integer transforms, fixed32/fixed64 and nested
// length-delimited messages, with strict bounds checking so truncated or
// malicious input can never cause an out-of-range access.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	WireVarint  WireType = 0 // int32, int64, uint32, uint64, sint*, bool, enum
	WireFixed64 WireType = 1 // fixed64, sfixed64, double
	WireBytes   WireType = 2 // string, bytes, embedded messages, packed repeated
	WireFixed32 WireType = 5 // fixed32, sfixed32, float
)

// MaxDepth bounds nested length-delimited messages so adversarial input
// cannot exhaust the stack.
const MaxDepth = 100

var (
	// ErrOverflow is returned when a varint exceeds 64 bits (10 bytes).
	ErrOverflow = errors.New("wire: varint overflows 64-bit integer")
	// ErrInvalidWireType is returned for reserved wire types 3, 4, 6, 7.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidFieldNumber is returned for a zero field number.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number 0")
	// ErrMaxDepth is returned when nesting exceeds MaxDepth.
	ErrMaxDepth = errors.New("wire: maximum nesting depth exceeded")
)

// BoundsError reports a safe out-of-range access attempt: a field claimed
// more bytes than remain in the input (i.e. a truncated field).
type BoundsError struct {
	Op     string // operation that hit the boundary, e.g. "varint", "bytes"
	Offset int    // absolute offset in the current buffer
	Need   int    // bytes required
	Have   int    // bytes actually available
}

func (e *BoundsError) Error() string {
	return fmt.Sprintf("wire: truncated %s at offset %d: need %d byte(s), have %d",
		e.Op, e.Offset, e.Need, e.Have)
}

// IsTruncated reports whether err (or any error it wraps) indicates
// truncated input.
func IsTruncated(err error) bool {
	for err != nil {
		if _, ok := err.(*BoundsError); ok {
			return true
		}
		causer, ok := err.(interface{ Cause() error })
		if !ok {
			return false
		}
		err = causer.Cause()
	}
	return false
}

// Reader is a zero-copy cursor over a protobuf wire buffer. Every read is
// bounds-checked; a short buffer yields *BoundsError, never a panic.
type Reader struct {
	buf   []byte
	pos   int
	depth int
}

// NewReader returns a Reader over buf at nesting depth 0.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

func newReaderDepth(buf []byte, depth int) *Reader {
	return &Reader{buf: buf, depth: depth}
}

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Offset returns the current read offset.
func (r *Reader) Offset() int { return r.pos }

// Done reports whether the buffer is fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// ReadVarint decodes a 7-bit-compressed unsigned varint (max 10 bytes).
func (r *Reader) ReadVarint() (uint64, error) {
	var x uint64
	var shift uint
	for i := 0; i < 10; i++ {
		if r.pos >= len(r.buf) {
			return 0, &BoundsError{Op: "varint", Offset: r.pos, Need: 1, Have: 0}
		}
		b := r.buf[r.pos]
		r.pos++
		if i == 9 && b > 1 {
			return 0, ErrOverflow
		}
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return x, nil
		}
		shift += 7
	}
	return 0, ErrOverflow
}

// ReadTag reads a field key and returns the field number and wire type.
func (r *Reader) ReadTag() (fieldNum int, wt WireType, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wt = WireType(v & 0x7)
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return 0, 0, ErrInvalidWireType
	}
	fieldNum = int(v >> 3)
	if fieldNum == 0 {
		return 0, 0, ErrInvalidFieldNumber
	}
	return fieldNum, wt, nil
}

// ReadFixed32 reads a little-endian 32-bit value.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, &BoundsError{Op: "fixed32", Offset: r.pos, Need: 4, Have: r.Remaining()}
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// ReadFixed64 reads a little-endian 64-bit value.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, &BoundsError{Op: "fixed64", Offset: r.pos, Need: 8, Have: r.Remaining()}
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

// ReadBytes reads a length-delimited blob, guarding against length prefixes
// that overrun the buffer.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, &BoundsError{Op: "bytes", Offset: r.pos, Need: int(n), Have: r.Remaining()}
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// ReadString reads a length-delimited UTF-8 string.
func (r *Reader) ReadString() (string, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReadZigZag32 reads a varint and ZigZag-decodes it to int32.
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int32(ZigZagDecode64(v)), nil
}

// ReadZigZag64 reads a varint and ZigZag-decodes it to int64.
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return ZigZagDecode64(v), nil
}

// ReadMessage reads a nested length-delimited message as a sub-Reader whose
// depth is incremented; exceeding MaxDepth yields ErrMaxDepth.
func (r *Reader) ReadMessage() (*Reader, error) {
	if r.depth+1 > MaxDepth {
		return nil, ErrMaxDepth
	}
	b, err := r.ReadBytes()
	if err != nil {
		return nil, err
	}
	return newReaderDepth(b, r.depth+1), nil
}

// Skip discards one field of the given wire type.
func (r *Reader) Skip(wt WireType) error {
	switch wt {
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

// ZigZagEncode64 maps a signed integer to an unsigned one so small-magnitude
// negatives stay small: 0->0, -1->1, 1->2, -2->3, ...
func ZigZagEncode64(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// ZigZagDecode64 is the inverse of ZigZagEncode64.
func ZigZagDecode64(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// ZigZagEncode32 is the 32-bit ZigZag transform.
func ZigZagEncode32(v int32) uint32 {
	return uint32(v<<1) ^ uint32(v>>31)
}

// ZigZagDecode32 is the inverse of ZigZagEncode32.
func ZigZagDecode32(v uint32) int32 {
	return int32(v>>1) ^ -int32(v&1)
}
