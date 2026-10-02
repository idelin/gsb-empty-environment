// Package wirebuf implements a dependency-free Protocol Buffers binary
// wire-format parser focused on zero-allocation, low-latency decoding
// through memory reuse and object pooling.
package wirebuf

import (
	"encoding/binary"
	"errors"
)

// Wire types as defined by the Protocol Buffers binary encoding.
type WireType int

const (
	WireVarint  WireType = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 WireType = 1 // fixed64, sfixed64, double
	WireBytes   WireType = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32 WireType = 5 // fixed32, sfixed32, float
)

// Sentinel errors. Compare with == (Go 1.11 compatible, no errors.Is).
var (
	// ErrTruncated is returned when the buffer ends before a field is
	// fully read. It is the safe out-of-bounds guard for truncated input.
	ErrTruncated = errors.New("wirebuf: truncated field, buffer exhausted")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than
	// 10 bytes, or a 10th byte carrying more than 1 bit of payload).
	ErrOverflow = errors.New("wirebuf: varint overflows 64-bit integer")
	// ErrInvalidWireType is returned for reserved wire types 3, 4, 6, 7.
	ErrInvalidWireType = errors.New("wirebuf: invalid or unsupported wire type")
	// ErrInvalidFieldNumber is returned for field number 0 or out of range.
	ErrInvalidFieldNumber = errors.New("wirebuf: invalid field number")
)

// MaxFieldNumber is the largest field number allowed by the protobuf spec.
const MaxFieldNumber = 1<<29 - 1

// maxVarintBytes is the maximum encoded length of a 64-bit varint.
const maxVarintBytes = 10

// Reader is a zero-copy cursor over a wire-format byte buffer.
// It is not safe for concurrent use; obtain one per goroutine via
// AcquireReader and return it with Release.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf. For hot paths prefer
// AcquireReader to avoid allocation.
func NewReader(buf []byte) *Reader {
	return &Reader{buf: buf}
}

// Reset repositions the Reader over a new buffer, enabling reuse.
func (r *Reader) Reset(buf []byte) {
	r.buf = buf
	r.pos = 0
}

// Remaining reports how many bytes are left to read.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Offset reports the current read position.
func (r *Reader) Offset() int { return r.pos }

// Done reports whether the buffer has been fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// ReadVarint decodes a base-128 varint (7 bits of payload per byte,
// little-endian group order). It enforces the 10-byte / 64-bit limit
// and reports truncation safely.
func (r *Reader) ReadVarint() (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < maxVarintBytes; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if b < 0x80 {
			if i == maxVarintBytes-1 && b > 1 {
				return 0, ErrOverflow
			}
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, ErrOverflow
}

// ReadZigZag64 decodes a varint and applies the ZigZag transform,
// yielding a signed 64-bit integer (protobuf sint64).
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int64(v>>1) ^ -int64(v&1), nil
}

// ReadZigZag32 decodes a ZigZag-encoded signed 32-bit integer (sint32).
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadZigZag64()
	if err != nil {
		return 0, err
	}
	return int32(v), nil
}

// ReadFixed32 reads a 4-byte little-endian value.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		r.pos = len(r.buf)
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// ReadFixed64 reads an 8-byte little-endian value.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		r.pos = len(r.buf)
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

// ReadBytes reads a length-delimited field and returns a zero-copy
// sub-slice of the underlying buffer. The caller must not retain it
// beyond the lifetime of the source buffer.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		r.pos = len(r.buf)
		return nil, ErrTruncated
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// ReadTag reads a field key and splits it into field number and wire type.
func (r *Reader) ReadTag() (num int, wt WireType, err error) {
	key, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	num = int(key >> 3)
	wt = WireType(key & 0x7)
	if num <= 0 || num > MaxFieldNumber {
		return 0, 0, ErrInvalidFieldNumber
	}
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
		return num, wt, nil
	default:
		return 0, 0, ErrInvalidWireType
	}
}

// SkipField discards one field value of the given wire type.
func (r *Reader) SkipField(wt WireType) error {
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
	case WireBytes:
		_, err := r.ReadBytes()
		return err
	default:
		return ErrInvalidWireType
	}
}

// ReadPackedVarints decodes a packed repeated varint payload (the value
// of a WireBytes field) into out, returning the extended slice. Passing
// a pooled slice with spare capacity keeps this allocation-free.
func ReadPackedVarints(payload []byte, out []uint64) ([]uint64, error) {
	r := Reader{buf: payload}
	for !r.Done() {
		v, err := r.ReadVarint()
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, nil
}
