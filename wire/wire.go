// Package wire implements a zero-dependency, hand-rolled parser and encoder
// for the Protocol Buffers binary wire format. It supports varint (7-bit
// compressed) integers, ZigZag signed integers, fixed 32/64-bit values and
// length-delimited (nested) fields. Every read is bounds-checked so truncated
// input is reported as an error instead of causing an out-of-bounds panic.
package wire

import (
	"errors"
	"fmt"
)

// Protobuf wire types (the low 3 bits of a tag).
const (
	WireVarint  = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 = 1 // fixed64, sfixed64, double
	WireBytes   = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32 = 5 // fixed32, sfixed32, float
)

var (
	// ErrTruncated is returned when the input ends before a value is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow is returned when a varint does not fit in 64 bits.
	ErrOverflow = errors.New("wire: varint overflows 64-bit integer")
	// ErrInvalidWireType is returned for reserved/unsupported wire types.
	ErrInvalidWireType = errors.New("wire: invalid or unsupported wire type")
	// ErrInvalidFieldNumber is returned for a tag with field number zero.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number 0")
	// ErrLengthOverflow is returned when a length prefix exceeds the input.
	ErrLengthOverflow = errors.New("wire: length prefix overflows remaining input")
)

// maxVarintBytes is the maximum number of bytes in a 64-bit varint.
const maxVarintBytes = 10

// Reader is a cursor over a protobuf-encoded byte slice. It is not safe for
// concurrent use; each goroutine should use its own Reader.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Done reports whether the input has been fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// Offset returns the current read offset, useful for error reporting.
func (r *Reader) Offset() int { return r.pos }

// ReadVarint reads a base-128 varint (7 bits of payload per byte).
func (r *Reader) ReadVarint() (uint64, error) {
	var v uint64
	for i := 0; i < maxVarintBytes; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == maxVarintBytes-1 {
			// The 10th byte may carry at most 1 payload bit for a uint64.
			if b > 1 {
				return 0, ErrOverflow
			}
			v |= uint64(b) << 63
			return v, nil
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag reads a varint and undoes the ZigZag signed-integer mapping.
func (r *Reader) ReadZigZag() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int64(v>>1) ^ -int64(v&1), nil
}

// ReadFixed32 reads a 4-byte little-endian value.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, ErrTruncated
	}
	v := uint32(r.buf[r.pos]) |
		uint32(r.buf[r.pos+1])<<8 |
		uint32(r.buf[r.pos+2])<<16 |
		uint32(r.buf[r.pos+3])<<24
	r.pos += 4
	return v, nil
}

// ReadFixed64 reads an 8-byte little-endian value.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, ErrTruncated
	}
	v := uint64(r.buf[r.pos]) |
		uint64(r.buf[r.pos+1])<<8 |
		uint64(r.buf[r.pos+2])<<16 |
		uint64(r.buf[r.pos+3])<<24 |
		uint64(r.buf[r.pos+4])<<32 |
		uint64(r.buf[r.pos+5])<<40 |
		uint64(r.buf[r.pos+6])<<48 |
		uint64(r.buf[r.pos+7])<<56
	r.pos += 8
	return v, nil
}

// ReadBytes reads a length-delimited field, returning a sub-slice of the
// input (no copy). A length prefix larger than the remaining input is
// reported as truncation, never as an out-of-bounds access.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, fmt.Errorf("%w: need %d bytes, have %d", ErrTruncated, n, r.Remaining())
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
	fieldNum = int(v >> 3)
	wireType = int(v & 0x7)
	if fieldNum == 0 {
		return 0, 0, ErrInvalidFieldNumber
	}
	switch wireType {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return 0, 0, fmt.Errorf("%w: %d", ErrInvalidWireType, wireType)
	}
	return fieldNum, wireType, nil
}

// SkipValue discards one value of the given wire type. Unknown fields are
// skipped this way so streams with newer fields remain decodable.
func (r *Reader) SkipValue(wireType int) error {
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
		return fmt.Errorf("%w: %d", ErrInvalidWireType, wireType)
	}
}

// AppendVarint appends v as a base-128 varint.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// AppendZigZag appends v using the ZigZag signed-integer mapping.
func AppendZigZag(buf []byte, v int64) []byte {
	return AppendVarint(buf, uint64(v<<1)^uint64(v>>63))
}

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(buf []byte, v uint64) []byte {
	return append(buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendTag appends the tag for the given field number and wire type.
func AppendTag(buf []byte, fieldNum, wireType int) []byte {
	return AppendVarint(buf, uint64(fieldNum)<<3|uint64(wireType))
}

// AppendBytes appends a length-delimited field.
func AppendBytes(buf, data []byte) []byte {
	buf = AppendVarint(buf, uint64(len(data)))
	return append(buf, data...)
}
