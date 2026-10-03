// Package wire implements the low-level Protocol Buffers binary wire format:
// varint (7-bit groups), zigzag, fixed32/64 and length-delimited records.
// It is a hand-rolled, dependency-free parser with strict bounds checking.
package wire

import (
	"encoding/binary"
	"errors"
)

// Wire types as defined by the Protocol Buffers binary encoding.
const (
	WireVarint  = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 = 1 // fixed64, sfixed64, double
	WireBytes   = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32 = 5 // fixed32, sfixed32, float
)

// MaxFieldNumber is the largest field number allowed by the protobuf spec
// (29 bits, minus the reserved 19000-19999 range which we still accept).
const MaxFieldNumber = 1<<29 - 1

// MaxVarintLen is the maximum encoded length of a 64-bit varint.
const MaxVarintLen = 10

var (
	// ErrTruncated is returned when the input ends before a value is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow is returned when a varint exceeds 64 bits (10 bytes).
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrBadWireType is returned for reserved/unknown wire types (3, 4, 6, 7).
	ErrBadWireType = errors.New("wire: unsupported wire type")
	// ErrBadFieldNumber is returned for field numbers outside [1, MaxFieldNumber].
	ErrBadFieldNumber = errors.New("wire: invalid field number")
)

// Reader is a bounds-checked cursor over an immutable byte slice, suitable
// for parsing memory-mapped buffers: every read is validated against the
// remaining length before any pointer arithmetic happens.
type Reader struct {
	buf []byte
	off int
}

// NewReader returns a Reader over b. b must not be mutated while in use.
func NewReader(b []byte) *Reader { return &Reader{buf: b} }

// Len reports the number of unread bytes.
func (r *Reader) Len() int { return len(r.buf) - r.off }

// Done reports whether the buffer has been fully consumed.
func (r *Reader) Done() bool { return r.off == len(r.buf) }

// Offset reports the absolute read position in the original buffer.
func (r *Reader) Offset() int { return r.off }

// Slice returns the absolute range [start, end) of the underlying buffer
// without copying. Callers capture start via Offset before a read.
func (r *Reader) Slice(start, end int) []byte { return r.buf[start:end] }

// Varint decodes a base-128 varint (7 bits per byte, little-endian groups).
// It rejects encodings longer than 10 bytes and 10-byte encodings whose
// final byte carries bits beyond the 64th, as well as truncated input.
func (r *Reader) Varint() (uint64, error) {
	b := r.buf[r.off:]
	n := len(b)
	if n > MaxVarintLen {
		n = MaxVarintLen
	}
	var v uint64
	for i := 0; i < n; i++ {
		c := b[i]
		if i == MaxVarintLen-1 && c > 1 {
			return 0, ErrOverflow
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			r.off += i + 1
			return v, nil
		}
	}
	if len(b) < MaxVarintLen {
		return 0, ErrTruncated
	}
	return 0, ErrOverflow
}

// Zigzag decodes a varint and applies the zigzag transform to recover a
// signed 64-bit integer: 0->0, 1->-1, 2->1, 3->-2, ...
func (r *Reader) Zigzag() (int64, error) {
	v, err := r.Varint()
	if err != nil {
		return 0, err
	}
	return int64(v>>1) ^ -int64(v&1), nil
}

// Fixed32 reads 4 bytes little-endian.
func (r *Reader) Fixed32() (uint32, error) {
	if r.Len() < 4 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v, nil
}

// Fixed64 reads 8 bytes little-endian.
func (r *Reader) Fixed64() (uint64, error) {
	if r.Len() < 8 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.off:])
	r.off += 8
	return v, nil
}

// Bytes reads a varint length prefix followed by that many bytes.
// The returned slice aliases the input (zero-copy). A length that exceeds
// the remaining input is reported as truncation, never as an allocation.
func (r *Reader) Bytes() ([]byte, error) {
	n, err := r.Varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Len()) {
		return nil, ErrTruncated
	}
	start := r.off
	r.off += int(n)
	return r.buf[start:r.off], nil
}

// Tag reads a field key: field number in the high bits, wire type in the
// low 3 bits.
func (r *Reader) Tag() (num int, wt int, err error) {
	v, err := r.Varint()
	if err != nil {
		return 0, 0, err
	}
	num = int(v >> 3)
	if num <= 0 || num > MaxFieldNumber {
		return 0, 0, ErrBadFieldNumber
	}
	return num, int(v & 7), nil
}

// Skip consumes one value of the given wire type, discarding it.
func (r *Reader) Skip(wt int) error {
	switch wt {
	case WireVarint:
		_, err := r.Varint()
		return err
	case WireFixed64:
		return r.skip(8)
	case WireBytes:
		_, err := r.Bytes()
		return err
	case WireFixed32:
		return r.skip(4)
	default:
		return ErrBadWireType
	}
}

func (r *Reader) skip(n int) error {
	if r.Len() < n {
		return ErrTruncated
	}
	r.off += n
	return nil
}

// Writer is an append-only encoder for the wire format.
type Writer struct {
	buf []byte
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// Out returns the encoded output. It aliases internal state; copy it if
// the Writer will be reused.
func (w *Writer) Out() []byte { return w.buf }

// Len reports the number of bytes written so far.
func (w *Writer) Len() int { return len(w.buf) }

// Varint appends v as a base-128 varint.
func (w *Writer) Varint(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

// Zigzag appends a signed integer using zigzag varint encoding.
func (w *Writer) Zigzag(v int64) {
	w.Varint(uint64(v<<1) ^ uint64(v>>63))
}

// Fixed32 appends 4 bytes little-endian.
func (w *Writer) Fixed32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

// Fixed64 appends 8 bytes little-endian.
func (w *Writer) Fixed64(v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

// Tag appends a field key.
func (w *Writer) Tag(num, wt int) {
	w.Varint(uint64(num)<<3 | uint64(wt))
}

// Bytes appends a length-delimited record.
func (w *Writer) Bytes(b []byte) {
	w.Varint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}

// BytesRaw appends already-encoded wire data verbatim (no length prefix).
func (w *Writer) BytesRaw(b []byte) {
	w.buf = append(w.buf, b...)
}

// EncodeZigzag converts a signed integer to its zigzag varint preimage.
func EncodeZigzag(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// DecodeZigzag reverses EncodeZigzag.
func DecodeZigzag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }
