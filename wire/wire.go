// Package wire defines the canonical binary serialization used by the
// negotiation protocol. The supported data model is deliberately small:
// unsigned varints, zigzag varints, booleans, length-prefixed strings and
// byte strings, and counted sequences of those. Decoding is pure data
// handling: it never reflects into, allocates, or executes host code based
// on wire contents, and every length is validated against both the hard
// limits below and the bytes actually remaining before any allocation is
// made.
package wire

import (
	"encoding/binary"
	"errors"
)

// Hard limits of the supported serialization range. Anything outside these
// bounds is not representable and is rejected on encode and on decode.
const (
	MaxStringLen = 1 << 16 // max bytes in a string
	MaxBytesLen  = 1 << 20 // max bytes in an opaque byte string
	MaxCount     = 1 << 14 // max elements in a counted sequence
	MaxDepth     = 32      // max nesting depth of composite values
)

var (
	ErrTruncated = errors.New("wire: truncated input")
	ErrLength    = errors.New("wire: length exceeds supported limit")
	ErrDepth     = errors.New("wire: nesting depth exceeds supported limit")
	ErrTrailing  = errors.New("wire: trailing bytes after value")
	ErrOverflow  = errors.New("wire: varint overflow")
	ErrBool      = errors.New("wire: invalid boolean byte")
)

// Encoder appends canonical encodings to a buffer.
type Encoder struct {
	buf []byte
}

// Out returns the encoded bytes.
func (e *Encoder) Out() []byte { return e.buf }

// Reset clears the encoder for reuse.
func (e *Encoder) Reset() { e.buf = e.buf[:0] }

func (e *Encoder) Uvarint(v uint64) { e.buf = binary.AppendUvarint(e.buf, v) }

// Varint encodes a signed integer with zigzag varint encoding.
func (e *Encoder) Varint(v int64) { e.buf = binary.AppendVarint(e.buf, v) }

func (e *Encoder) Bool(b bool) {
	if b {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

func (e *Encoder) String(s string) {
	e.Uvarint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *Encoder) Bytes(b []byte) {
	e.Uvarint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// Decoder reads values from a byte slice. It is not safe for concurrent use.
type Decoder struct {
	data  []byte
	off   int
	depth int
}

func NewDecoder(data []byte) *Decoder { return &Decoder{data: data} }

// Remaining reports how many input bytes are left.
func (d *Decoder) Remaining() int { return len(d.data) - d.off }

// Done reports success only if the whole input was consumed.
func (d *Decoder) Done() error {
	if d.off != len(d.data) {
		return ErrTrailing
	}
	return nil
}

// Enter/Leave track composite nesting so hostile inputs cannot force
// unbounded recursion.
func (d *Decoder) Enter() error {
	d.depth++
	if d.depth > MaxDepth {
		return ErrDepth
	}
	return nil
}

func (d *Decoder) Leave() { d.depth-- }

func (d *Decoder) Uvarint() (uint64, error) {
	v, n := binary.Uvarint(d.data[d.off:])
	if n == 0 {
		return 0, ErrTruncated
	}
	if n < 0 {
		return 0, ErrOverflow
	}
	d.off += n
	return v, nil
}

func (d *Decoder) Varint() (int64, error) {
	v, n := binary.Varint(d.data[d.off:])
	if n == 0 {
		return 0, ErrTruncated
	}
	if n < 0 {
		return 0, ErrOverflow
	}
	d.off += n
	return v, nil
}

func (d *Decoder) Bool() (bool, error) {
	if d.Remaining() < 1 {
		return false, ErrTruncated
	}
	b := d.data[d.off]
	d.off++
	switch b {
	case 0:
		return false, nil
	case 1:
		return true, nil
	}
	return false, ErrBool
}

// Count reads a sequence element count. Every element of a counted sequence
// encodes to at least one byte, so a count larger than the remaining input
// is impossible and is rejected before the caller allocates anything.
func (d *Decoder) Count(limit int) (int, error) {
	v, err := d.Uvarint()
	if err != nil {
		return 0, err
	}
	if v > uint64(limit) {
		return 0, ErrLength
	}
	if v > uint64(d.Remaining()) {
		return 0, ErrTruncated
	}
	return int(v), nil
}

// String reads a length-prefixed string, validating the length against
// MaxStringLen and the remaining input before allocating.
func (d *Decoder) String() (string, error) {
	n, err := d.Count(MaxStringLen)
	if err != nil {
		return "", err
	}
	s := string(d.data[d.off : d.off+n])
	d.off += n
	return s, nil
}

// Bytes reads a length-prefixed byte string with the same validation
// discipline as String.
func (d *Decoder) Bytes() ([]byte, error) {
	n, err := d.Count(MaxBytesLen)
	if err != nil {
		return nil, err
	}
	b := make([]byte, n)
	copy(b, d.data[d.off:d.off+n])
	d.off += n
	return b, nil
}
