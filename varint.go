// Package varint implements a strict streaming codec for telemetry
// integers.
//
// Unsigned values are encoded as little-endian base-128 varints: each
// byte carries a 7-bit payload and its most-significant bit is the
// continuation flag. Signed values are mapped to unsigned ones with
// ZigZag encoding before being written.
//
// The codec is strict. Values are limited to 64 bits, encodings must be
// minimal (shortest form), and every malformed value is rejected with
// an *Error reporting the exact byte range it consumed. The next value
// in a stream always resumes at an explicit frame boundary: the byte
// immediately after the consumed range.
package varint

import "strconv"

// MaxLen is the maximum number of bytes in an encoded 64-bit value:
// nine full 7-bit groups plus one byte carrying the final bit.
const MaxLen = 10

// Kind classifies a decoding failure.
type Kind int

const (
	// ErrTruncated indicates the input ended in the middle of a value.
	ErrTruncated Kind = iota + 1
	// ErrNonCanonical indicates a non-minimal encoding: the value
	// could have been written in fewer bytes.
	ErrNonCanonical
	// ErrOverlong indicates an encoding longer than MaxLen bytes.
	ErrOverlong
	// ErrOverflow indicates a value that does not fit in 64 bits.
	ErrOverflow
)

// String returns a short human-readable name for the failure kind.
func (k Kind) String() string {
	switch k {
	case ErrTruncated:
		return "truncated value"
	case ErrNonCanonical:
		return "non-minimal encoding"
	case ErrOverlong:
		return "encoding exceeds 10 bytes"
	case ErrOverflow:
		return "value overflows 64 bits"
	}
	return "unknown error"
}

// Error describes a malformed value. Offset is the position of the
// value's first byte (an absolute stream offset for Decoder, or an
// index into the buffer for DecodeAt), and Consumed is the exact
// number of bytes the malformed value spans.
type Error struct {
	Kind     Kind
	Offset   int64
	Consumed int
}

// Error returns a human-readable description of the failure.
func (e *Error) Error() string {
	return "varint: " + e.Kind.String() + " at offset " +
		strconv.FormatInt(e.Offset, 10) + " (" +
		strconv.Itoa(e.Consumed) + " bytes consumed)"
}

// Boundary returns the explicit frame boundary immediately after the
// malformed value: the position at which the next value resumes.
func (e *Error) Boundary() int64 { return e.Offset + int64(e.Consumed) }

// DecodeAt decodes one unsigned value from buf starting at index off.
// It returns the value and next, the index of the first byte after the
// value's frame.
//
// On failure the returned error is an *Error whose Offset and Consumed
// delimit the exact byte range examined, and next equals off+Consumed,
// the explicit frame boundary at which the following value resumes.
func DecodeAt(buf []byte, off int) (v uint64, next int, err error) {
	for i := 0; i < MaxLen; i++ {
		pos := off + i
		if pos >= len(buf) {
			return 0, pos, &Error{Kind: ErrTruncated, Offset: int64(off), Consumed: i}
		}
		b := buf[pos]
		if i == MaxLen-1 {
			// Tenth byte: only bit 63 remains, so the payload
			// must be exactly 1 and the value must end here.
			switch {
			case b&0x80 != 0:
				return 0, pos + 1, &Error{Kind: ErrOverlong, Offset: int64(off), Consumed: MaxLen}
			case b > 1:
				return 0, pos + 1, &Error{Kind: ErrOverflow, Offset: int64(off), Consumed: MaxLen}
			case b == 0:
				return 0, pos + 1, &Error{Kind: ErrNonCanonical, Offset: int64(off), Consumed: MaxLen}
			}
			return v | uint64(b)<<63, pos + 1, nil
		}
		if b&0x80 == 0 {
			if b == 0 && i > 0 {
				// A zero payload in the final group means the
				// value fits in fewer bytes.
				return 0, pos + 1, &Error{Kind: ErrNonCanonical, Offset: int64(off), Consumed: i + 1}
			}
			return v | uint64(b)<<(7*uint(i)), pos + 1, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
	}
	panic("varint: unreachable")
}

// Decode decodes one unsigned value from the start of buf. It is
// shorthand for DecodeAt(buf, 0).
func Decode(buf []byte) (v uint64, n int, err error) {
	return DecodeAt(buf, 0)
}

// DecodeInt64At decodes one ZigZag-encoded signed value from buf
// starting at index off. It behaves like DecodeAt otherwise.
func DecodeInt64At(buf []byte, off int) (x int64, next int, err error) {
	u, next, err := DecodeAt(buf, off)
	if err != nil {
		return 0, next, err
	}
	return ZigZagDecode(u), next, nil
}

// DecodeInt64 decodes one ZigZag-encoded signed value from the start
// of buf. It is shorthand for DecodeInt64At(buf, 0).
func DecodeInt64(buf []byte) (x int64, n int, err error) {
	return DecodeInt64At(buf, 0)
}

// AppendUint64 appends the minimal encoding of v to dst and returns
// the extended buffer.
func AppendUint64(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendInt64 appends the minimal ZigZag encoding of x to dst and
// returns the extended buffer.
func AppendInt64(dst []byte, x int64) []byte {
	return AppendUint64(dst, ZigZagEncode(x))
}

// EncodedLen returns the number of bytes in the minimal encoding of v.
func EncodedLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ZigZagEncode maps a signed integer to an unsigned one so that values
// with small absolute magnitude produce small varints: 0, -1, 1, -2,
// 2 encode as 0, 1, 2, 3, 4.
func ZigZagEncode(x int64) uint64 {
	return uint64(x)<<1 ^ uint64(x>>63)
}

// ZigZagDecode is the inverse of ZigZagEncode.
func ZigZagDecode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}
