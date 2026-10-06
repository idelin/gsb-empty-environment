package varint

import (
	"bufio"
	"io"
)

// Decoder reads a stream of consecutive varint values from an
// io.Reader, tracking the absolute offset of every value's frame.
//
// If a value is malformed, ReadUint64 or ReadInt64 returns an *Error
// describing the exact byte range consumed; the Decoder is left
// positioned at Error.Boundary, so the next read resumes at the
// explicit frame boundary of the following value.
type Decoder struct {
	br  io.ByteReader
	off int64
	buf [MaxLen]byte
}

// NewDecoder returns a Decoder reading from r. If r does not implement
// io.ByteReader it is wrapped in a bufio.Reader.
func NewDecoder(r io.Reader) *Decoder {
	if br, ok := r.(io.ByteReader); ok {
		return &Decoder{br: br}
	}
	return &Decoder{br: bufio.NewReader(r)}
}

// Offset returns the absolute offset of the next value's frame
// boundary: the number of bytes consumed from the stream so far.
func (d *Decoder) Offset() int64 { return d.off }

// ReadUint64 reads the next unsigned value from the stream.
//
// It returns io.EOF when the stream ends exactly at a frame boundary,
// and an *Error with Kind ErrTruncated when it ends mid-value.
func (d *Decoder) ReadUint64() (uint64, error) {
	n := 0
	for n < MaxLen {
		b, err := d.br.ReadByte()
		if err != nil {
			if n == 0 {
				return 0, err
			}
			start := d.off
			d.off += int64(n)
			return 0, &Error{Kind: ErrTruncated, Offset: start, Consumed: n}
		}
		d.buf[n] = b
		n++
		if b&0x80 == 0 {
			break
		}
	}
	start := d.off
	d.off += int64(n)
	v, _, err := DecodeAt(d.buf[:n], 0)
	if err != nil {
		e := err.(*Error)
		e.Offset = start
		return 0, e
	}
	return v, nil
}

// ReadInt64 reads the next ZigZag-encoded signed value from the
// stream. It behaves like ReadUint64 otherwise.
func (d *Decoder) ReadInt64() (int64, error) {
	u, err := d.ReadUint64()
	if err != nil {
		return 0, err
	}
	return ZigZagDecode(u), nil
}

// Encoder writes a stream of consecutive varint values to an
// io.Writer, tracking the absolute offset of every value's frame.
type Encoder struct {
	w   io.Writer
	off int64
	buf [MaxLen]byte
}

// NewEncoder returns an Encoder writing to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Offset returns the absolute offset of the next value's frame
// boundary: the number of bytes written to the stream so far.
func (e *Encoder) Offset() int64 { return e.off }

// WriteUint64 writes the minimal encoding of v to the stream.
func (e *Encoder) WriteUint64(v uint64) error {
	b := AppendUint64(e.buf[:0], v)
	n, err := e.w.Write(b)
	e.off += int64(n)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

// WriteInt64 writes the minimal ZigZag encoding of x to the stream.
func (e *Encoder) WriteInt64(x int64) error {
	return e.WriteUint64(ZigZagEncode(x))
}
