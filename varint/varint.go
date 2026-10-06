// Package varint implements a streaming 64-bit LEB128 codec for
// telemetry integers.
//
// Encoding: an unsigned value is split into 7-bit payload groups,
// least-significant group first; every byte except the last carries a
// continuation bit (0x80). Signed values are ZigZag-mapped to unsigned
// before encoding. Encodings must be minimal: the final payload group
// must be non-zero unless the whole value fits in a single byte.
// Values are limited to 64 bits, so an encoding is at most MaxLen
// bytes and the last byte may carry only a single payload bit.
//
// Decoding is streaming: a Decoder tracks the offset of the next frame
// boundary in its buffer. Hard errors (NonMinimal, Overlong, Overflow)
// consume the malformed frame, so the next Decode call resumes at the
// frame boundary right after it. Truncated consumes nothing, so the
// caller can append more input and retry from the same boundary. Every
// DecodeError reports the exact consumed byte range [Start, End).
package varint

import "fmt"

// MaxLen is the maximum encoded length of a 64-bit value.
const MaxLen = 10

// Kind classifies a DecodeError.
type Kind int

const (
	// Truncated means the buffer ended before a complete value.
	// Nothing is consumed; resume at Start once more data arrives.
	Truncated Kind = iota
	// NonMinimal means the encoding is not the shortest form: the
	// final payload group is zero while a shorter encoding exists.
	NonMinimal
	// Overlong means the encoding exceeds MaxLen bytes.
	Overlong
	// Overflow means the encoding carries payload bits beyond 64.
	Overflow
)

func (k Kind) String() string {
	switch k {
	case Truncated:
		return "truncated"
	case NonMinimal:
		return "non-minimal encoding"
	case Overlong:
		return "overlong encoding"
	case Overflow:
		return "64-bit overflow"
	}
	return "unknown error"
}

// DecodeError reports a malformed frame. Start and End are absolute
// offsets into the decoder's buffer delimiting the exact byte range
// consumed by the bad frame.
type DecodeError struct {
	Kind  Kind
	Start int
	End   int
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("varint: %s at bytes [%d,%d)", e.Kind, e.Start, e.End)
}

// Zigzag maps a signed value onto an unsigned one: 0,-1,1,-2,2,... ->
// 0,1,2,3,4,...
func Zigzag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// Unzigzag is the inverse of Zigzag.
func Unzigzag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// AppendUvarint appends the minimal encoding of v to dst.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendVarint ZigZag-maps v and appends its minimal encoding to dst.
func AppendVarint(dst []byte, v int64) []byte {
	return AppendUvarint(dst, Zigzag(v))
}

// Decoder decodes a stream of values from a byte buffer, tracking the
// offset of the next frame boundary.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder returns a Decoder reading from buf.
func NewDecoder(buf []byte) *Decoder {
	return &Decoder{buf: buf}
}

// Pos returns the offset of the next frame boundary.
func (d *Decoder) Pos() int {
	return d.pos
}

// Seek repositions the decoder to an explicit frame boundary.
func (d *Decoder) Seek(pos int) {
	d.pos = pos
}

// Append adds more input after a Truncated error. The decoder position
// is preserved, so decoding resumes at the incomplete frame.
func (d *Decoder) Append(more []byte) {
	d.buf = append(d.buf, more...)
}

// DecodeUvarint decodes one unsigned value. On success the decoder
// advances past the value. On a hard error the malformed frame is
// consumed and the decoder resumes after it; on Truncated the decoder
// stays at the frame start.
func (d *Decoder) DecodeUvarint() (uint64, error) {
	start := d.pos
	var v uint64
	for i := 0; i < MaxLen; i++ {
		idx := start + i
		if idx >= len(d.buf) {
			return 0, &DecodeError{Kind: Truncated, Start: start, End: len(d.buf)}
		}
		b := d.buf[idx]
		payload := b & 0x7f
		if i == MaxLen-1 {
			if b&0x80 != 0 {
				d.pos = idx + 1
				return 0, &DecodeError{Kind: Overlong, Start: start, End: idx + 1}
			}
			if payload > 1 {
				d.pos = idx + 1
				return 0, &DecodeError{Kind: Overflow, Start: start, End: idx + 1}
			}
		}
		v |= uint64(payload) << (7 * uint(i))
		if b&0x80 == 0 {
			if i > 0 && payload == 0 {
				d.pos = idx + 1
				return 0, &DecodeError{Kind: NonMinimal, Start: start, End: idx + 1}
			}
			d.pos = idx + 1
			return v, nil
		}
	}
	panic("unreachable")
}

// DecodeVarint decodes one ZigZag-mapped signed value.
func (d *Decoder) DecodeVarint() (int64, error) {
	u, err := d.DecodeUvarint()
	if err != nil {
		return 0, err
	}
	return Unzigzag(u), nil
}
