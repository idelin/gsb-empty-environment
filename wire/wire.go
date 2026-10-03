// Package wire implements a zero-dependency Protocol Buffers binary
// wire-format reader and writer.
//
// Supported primitives:
//   - Varint   (7-bit compressed unsigned integers, max 10 bytes)
//   - ZigZag   (signed integer mapping on top of varint)
//   - Fixed32 / Fixed64 (little-endian)
//   - Bytes    (length-delimited, used for strings, embedded messages
//     and packed repeated fields)
//
// Every read is bounds-checked: a truncated input never causes a panic
// or an out-of-bounds access, it yields a *TruncatedError instead.
package wire

import (
	"errors"
	"fmt"
)

// Type is a protobuf wire type (the low 3 bits of a tag).
type Type int

const (
	Varint     Type = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	Fixed64    Type = 1 // fixed64, sfixed64, double
	Bytes      Type = 2 // string, bytes, embedded messages, packed repeated
	StartGroup Type = 3 // deprecated, rejected by this parser
	EndGroup   Type = 4 // deprecated, rejected by this parser
	Fixed32    Type = 5 // fixed32, sfixed32, float
)

func (t Type) String() string {
	switch t {
	case Varint:
		return "varint"
	case Fixed64:
		return "fixed64"
	case Bytes:
		return "bytes"
	case StartGroup:
		return "start-group"
	case EndGroup:
		return "end-group"
	case Fixed32:
		return "fixed32"
	default:
		return fmt.Sprintf("invalid(%d)", int(t))
	}
}

// ErrOverflow is returned when a varint does not fit into 64 bits
// (more than 10 bytes, or a 10th byte carrying more than 1 bit).
var ErrOverflow = errors.New("wire: varint overflows 64-bit value")

// TruncatedError reports a read that would run past the end of the
// buffer. It is the safe-bounds guard required when parsing untrusted
// or corrupted protobuf streams.
type TruncatedError struct {
	What   string // human readable primitive name, e.g. "varint", "fixed32"
	Need   int    // bytes still required
	Have   int    // bytes actually remaining
	Offset int    // absolute offset in the buffer where the read started
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("wire: truncated %s at offset %d: need %d byte(s), have %d",
		e.What, e.Offset, e.Need, e.Have)
}

// IsTruncated reports whether err (or any error wrapping it) is a
// truncation error.
func IsTruncated(err error) bool {
	var te *TruncatedError
	return errors.As(err, &te)
}

// Reader consumes a protobuf wire-format buffer sequentially.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over b. The buffer is not copied.
func NewReader(b []byte) *Reader { return &Reader{buf: b} }

// Offset returns the absolute read position in the buffer.
func (r *Reader) Offset() int { return r.pos }

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Done reports whether the buffer has been fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// Varint reads a base-128 varint (7 payload bits per byte, little-endian
// group order). At most 10 bytes are consumed; anything longer is an
// overflow error, never an over-read.
func (r *Reader) Varint() (uint64, error) {
	start := r.pos
	var v uint64
	for i := 0; i < 10; i++ {
		if r.pos >= len(r.buf) {
			return 0, &TruncatedError{What: "varint", Need: 1, Have: 0, Offset: start}
		}
		b := r.buf[r.pos]
		r.pos++
		if i == 9 && b > 1 {
			// The 10th byte may only contribute a single bit (bit 63).
			return 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ZigZag reads a varint and applies the ZigZag signed-integer mapping:
// 0->0, 1->-1, 2->1, 3->-2, ...
func (r *Reader) ZigZag() (int64, error) {
	v, err := r.Varint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag(v), nil
}

// DecodeZigZag maps an unsigned varint back to its signed value.
func DecodeZigZag(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// EncodeZigZag maps a signed value to its unsigned varint representation.
func EncodeZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// Fixed32 reads 4 bytes little-endian.
func (r *Reader) Fixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, &TruncatedError{What: "fixed32", Need: 4, Have: r.Remaining(), Offset: r.pos}
	}
	v := uint32(r.buf[r.pos]) |
		uint32(r.buf[r.pos+1])<<8 |
		uint32(r.buf[r.pos+2])<<16 |
		uint32(r.buf[r.pos+3])<<24
	r.pos += 4
	return v, nil
}

// Fixed64 reads 8 bytes little-endian.
func (r *Reader) Fixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, &TruncatedError{What: "fixed64", Need: 8, Have: r.Remaining(), Offset: r.pos}
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

// Bytes reads a length-delimited byte slice: a varint length prefix
// followed by that many bytes. The returned slice aliases the input
// buffer. A declared length larger than the remaining input is a
// truncation error, never an out-of-bounds slice.
func (r *Reader) Bytes() ([]byte, error) {
	start := r.pos
	n, err := r.Varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, &TruncatedError{
			What:   "bytes",
			Need:   int(n),
			Have:   r.Remaining(),
			Offset: start,
		}
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// Tag reads a field key: (field_number << 3) | wire_type.
// Field number 0, unknown wire types (6, 7) and deprecated groups are
// rejected.
func (r *Reader) Tag() (num int, typ Type, err error) {
	start := r.pos
	v, err := r.Varint()
	if err != nil {
		return 0, 0, err
	}
	num = int(v >> 3)
	typ = Type(v & 7)
	if num <= 0 {
		return 0, 0, fmt.Errorf("wire: invalid field number %d at offset %d", num, start)
	}
	switch typ {
	case Varint, Fixed64, Bytes, Fixed32:
		return num, typ, nil
	case StartGroup, EndGroup:
		return 0, 0, fmt.Errorf("wire: deprecated group wire type %q at offset %d", typ, start)
	default:
		return 0, 0, fmt.Errorf("wire: unknown wire type %d at offset %d", int(typ), start)
	}
}

// Writer builds protobuf wire-format buffers. The zero value is ready
// to use.
type Writer struct {
	buf []byte
}

// AppendVarint appends v in base-128 varint form.
func (w *Writer) AppendVarint(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

// AppendZigZag appends a ZigZag-encoded signed integer.
func (w *Writer) AppendZigZag(v int64) {
	w.AppendVarint(EncodeZigZag(v))
}

// AppendTag appends a field key.
func (w *Writer) AppendTag(num int, typ Type) {
	w.AppendVarint(uint64(num)<<3 | uint64(typ))
}

// AppendFixed32 appends 4 bytes little-endian.
func (w *Writer) AppendFixed32(v uint32) {
	w.buf = append(w.buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends 8 bytes little-endian.
func (w *Writer) AppendFixed64(v uint64) {
	w.buf = append(w.buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendBytes appends a length-delimited byte slice.
func (w *Writer) AppendBytes(b []byte) {
	w.AppendVarint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}

// Bytes returns the accumulated buffer.
func (w *Writer) Bytes() []byte { return w.buf }

// Len returns the number of bytes written so far.
func (w *Writer) Len() int { return len(w.buf) }

// Reset discards the accumulated buffer.
func (w *Writer) Reset() { w.buf = w.buf[:0] }
