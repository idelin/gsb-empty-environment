// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format codec: varint (7-bit compression), zigzag signed
// integer mapping, fixed32/fixed64 and length-delimited primitives.
//
// All reads are bounds-checked: any truncation is reported as ErrTruncated
// and never causes an out-of-bounds access.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Sentinel errors (Go 1.11 compatible: compare with ==).
var (
	// ErrTruncated is returned when the buffer ends before a value is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than 10 bytes).
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrNegativeLength is returned when a length prefix is negative.
	ErrNegativeLength = errors.New("wire: negative length")
)

// MaxVarintBytes is the maximum encoded length of a 64-bit varint.
const MaxVarintBytes = 10

// Reader is a cursor over a byte slice. It is not safe for concurrent use.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Done reports whether all bytes have been consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// Pos returns the current read offset (useful for error reporting).
func (r *Reader) Pos() int { return r.pos }

// ReadVarint reads a base-128 varint: 7 payload bits per byte, MSB is the
// continuation flag. Returns ErrTruncated if the buffer ends mid-varint and
// ErrOverflow if more than 10 bytes are used.
func (r *Reader) ReadVarint() (uint64, error) {
	var v uint64
	for i := 0; i < MaxVarintBytes; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == MaxVarintBytes-1 {
			// 10th byte may only carry 1 payload bit for a uint64.
			if b > 1 {
				return 0, ErrOverflow
			}
			v |= uint64(b) << 63
			return v, nil
		}
		v |= uint64(b&0x7f) << (uint(i) * 7)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag64 reads a varint and undoes the zigzag mapping for sint64.
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return ZigZagDecode64(v), nil
}

// ReadZigZag32 reads a varint and undoes the zigzag mapping for sint32.
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return ZigZagDecode32(uint32(v)), nil
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

// ReadBytes reads exactly n bytes, bounds-checked.
func (r *Reader) ReadBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeLength
	}
	if r.Remaining() < n {
		r.pos = len(r.buf)
		return nil, ErrTruncated
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// TruncatedError describes a length prefix that overruns the buffer.
// IsTruncated recognizes it as a truncation condition.
type TruncatedError struct {
	Need int64 // declared length prefix
	Have int   // bytes actually remaining
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("wire: length prefix %d exceeds remaining %d (truncated)", e.Need, e.Have)
}

// Truncated implements the truncation marker used by IsTruncated.
func (e *TruncatedError) Truncated() bool { return true }

// ReadLengthDelimited reads a varint length prefix followed by that many
// bytes. The length is validated against the remaining buffer before any
// slice is taken, so a corrupt length can never escape the buffer.
func (r *Reader) ReadLengthDelimited() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		remain := r.Remaining()
		r.pos = len(r.buf)
		return nil, &TruncatedError{Need: int64(n), Have: remain}
	}
	return r.ReadBytes(int(n))
}

// IsTruncated reports whether err represents a truncation condition,
// including wrapped length-prefix truncations.
func IsTruncated(err error) bool {
	if err == ErrTruncated {
		return true
	}
	if te, ok := err.(interface{ Truncated() bool }); ok {
		return te.Truncated()
	}
	return false
}

// ZigZagEncode32 maps a signed int32 to an unsigned varint-friendly value.
func ZigZagEncode32(v int32) uint32 {
	return uint32((v << 1) ^ (v >> 31))
}

// ZigZagDecode32 undoes ZigZagEncode32.
func ZigZagDecode32(v uint32) int32 {
	return int32((v >> 1) ^ uint32(-int32(v&1)))
}

// ZigZagEncode64 maps a signed int64 to an unsigned varint-friendly value.
func ZigZagEncode64(v int64) uint64 {
	return uint64((v << 1) ^ (v >> 63))
}

// ZigZagDecode64 undoes ZigZagEncode64.
func ZigZagDecode64(v uint64) int64 {
	return int64((v >> 1) ^ (0 - (v & 1)))
}

// Writer appends wire-format primitives to a buffer.
type Writer struct {
	buf []byte
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// Bytes returns the accumulated output.
func (w *Writer) Bytes() []byte { return w.buf }

// Len returns the number of bytes written so far.
func (w *Writer) Len() int { return len(w.buf) }

// AppendVarint appends v using 7-bit-per-byte compression.
func (w *Writer) AppendVarint(v uint64) {
	var tmp [MaxVarintBytes]byte
	n := binary.PutUvarint(tmp[:], v)
	w.buf = append(w.buf, tmp[:n]...)
}

// AppendZigZag64 appends a zigzag-encoded sint64.
func (w *Writer) AppendZigZag64(v int64) { w.AppendVarint(ZigZagEncode64(v)) }

// AppendZigZag32 appends a zigzag-encoded sint32.
func (w *Writer) AppendZigZag32(v int32) { w.AppendVarint(uint64(ZigZagEncode32(v))) }

// AppendFixed32 appends 4 bytes little-endian.
func (w *Writer) AppendFixed32(v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	w.buf = append(w.buf, tmp[:]...)
}

// AppendFixed64 appends 8 bytes little-endian.
func (w *Writer) AppendFixed64(v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	w.buf = append(w.buf, tmp[:]...)
}

// AppendBytes appends raw bytes (no length prefix).
func (w *Writer) AppendBytes(b []byte) { w.buf = append(w.buf, b...) }

// AppendLengthDelimited appends a varint length prefix followed by b.
func (w *Writer) AppendLengthDelimited(b []byte) {
	w.AppendVarint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}
