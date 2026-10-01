// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format codec: varint (7-bit groups), ZigZag signed integers,
// fixed32/fixed64 and nested length-delimited messages. All reads are
// bounds-checked; truncated input is reported as ErrTruncated and never
// causes an out-of-bounds access.
package wire

import "errors"

// Protobuf wire types (low 3 bits of a tag).
const (
	WTVarint  = 0 // int32, int64, uint32, uint64, sint, bool, enum
	WTFixed64 = 1 // fixed64, sfixed64, double
	WTBytes   = 2 // string, bytes, embedded messages, packed repeated
	WTFixed32 = 5 // fixed32, sfixed32, float
)

var (
	// ErrTruncated is returned when the input ends before a value is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than 10 bytes).
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrBadWireType is returned for a wire type outside [0,5] or a mismatch.
	ErrBadWireType = errors.New("wire: invalid or unexpected wire type")
	// ErrNegativeLength is returned when a length prefix is negative.
	ErrNegativeLength = errors.New("wire: negative length prefix")
)

// Decoder is a cursor over a protobuf binary buffer. It is not safe for
// concurrent use; each goroutine should decode its own buffer.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder returns a Decoder reading from buf. buf is not copied.
func NewDecoder(buf []byte) *Decoder { return &Decoder{buf: buf} }

// More reports whether unread bytes remain.
func (d *Decoder) More() bool { return d.pos < len(d.buf) }

// Remaining returns the number of unread bytes.
func (d *Decoder) Remaining() int { return len(d.buf) - d.pos }

// Offset returns the current read offset (useful for error reporting).
func (d *Decoder) Offset() int { return d.pos }

// Varint reads a base-128 varint (7 payload bits per byte, MSB = continuation).
// At most 10 bytes are consumed; an 11th byte means overflow.
func (d *Decoder) Varint() (uint64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if d.pos >= len(d.buf) {
			return 0, ErrTruncated
		}
		b := d.buf[d.pos]
		d.pos++
		if i == 9 && b > 1 {
			return 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ZigZag reads a varint and undoes ZigZag mapping:
// 0->0, 1->-1, 2->1, 3->-2, ...
func (d *Decoder) ZigZag() (int64, error) {
	u, err := d.Varint()
	if err != nil {
		return 0, err
	}
	return Unzigzag(u), nil
}

// Unzigzag converts a ZigZag-encoded uint64 back to int64.
func Unzigzag(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// Zigzag maps a signed int64 to its ZigZag-encoded uint64 form.
func Zigzag(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// Fixed32 reads 4 bytes, little-endian.
func (d *Decoder) Fixed32() (uint32, error) {
	if len(d.buf)-d.pos < 4 {
		d.pos = len(d.buf)
		return 0, ErrTruncated
	}
	p := d.buf[d.pos:]
	d.pos += 4
	return uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<16 | uint32(p[3])<<24, nil
}

// Fixed64 reads 8 bytes, little-endian.
func (d *Decoder) Fixed64() (uint64, error) {
	if len(d.buf)-d.pos < 8 {
		d.pos = len(d.buf)
		return 0, ErrTruncated
	}
	p := d.buf[d.pos:]
	d.pos += 8
	return uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24 |
		uint64(p[4])<<32 | uint64(p[5])<<40 | uint64(p[6])<<48 | uint64(p[7])<<56, nil
}

// Bytes reads a length-delimited field: a varint length prefix followed by
// that many bytes. The returned slice aliases the input buffer.
func (d *Decoder) Bytes() ([]byte, error) {
	n, err := d.Varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(d.buf)-d.pos) {
		d.pos = len(d.buf)
		return nil, ErrTruncated
	}
	b := d.buf[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return b, nil
}

// Tag reads a field tag and returns (field number, wire type).
func (d *Decoder) Tag() (num int, wt int, err error) {
	v, err := d.Varint()
	if err != nil {
		return 0, 0, err
	}
	num, wt = int(v>>3), int(v&7)
	if num <= 0 {
		return 0, 0, ErrBadWireType
	}
	switch wt {
	case WTVarint, WTFixed64, WTBytes, WTFixed32:
		return num, wt, nil
	}
	return 0, 0, ErrBadWireType
}

// Skip discards one field of the given wire type (for unknown fields).
func (d *Decoder) Skip(wt int) error {
	switch wt {
	case WTVarint:
		_, err := d.Varint()
		return err
	case WTFixed64:
		_, err := d.Fixed64()
		return err
	case WTBytes:
		_, err := d.Bytes()
		return err
	case WTFixed32:
		_, err := d.Fixed32()
		return err
	}
	return ErrBadWireType
}

// Encoder appends protobuf wire-format bytes to a buffer. It never fails;
// the zero value is ready to use.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the encoded output.
func (e *Encoder) Bytes() []byte { return e.buf }

// Reset clears the encoder for reuse.
func (e *Encoder) Reset() { e.buf = e.buf[:0] }

// Varint appends v in base-128 varint form.
func (e *Encoder) Varint(v uint64) {
	for v >= 0x80 {
		e.buf = append(e.buf, byte(v)|0x80)
		v >>= 7
	}
	e.buf = append(e.buf, byte(v))
}

// ZigZag appends a ZigZag-encoded signed integer.
func (e *Encoder) ZigZag(v int64) { e.Varint(Zigzag(v)) }

// Fixed32 appends 4 little-endian bytes.
func (e *Encoder) Fixed32(v uint32) {
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// Fixed64 appends 8 little-endian bytes.
func (e *Encoder) Fixed64(v uint64) {
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// Tag appends the tag for field num with wire type wt.
func (e *Encoder) Tag(num, wt int) { e.Varint(uint64(num)<<3 | uint64(wt)) }

// AppendBytes appends a length-delimited field body.
func (e *Encoder) AppendBytes(b []byte) {
	e.Varint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}
