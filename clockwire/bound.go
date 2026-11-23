// Package clockwire contains the small data types shared by the release host
// and the offline authorization solver.
package clockwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// EncodedBoundSize is the size of Bound.MarshalBinary.
const EncodedBoundSize = 16

// ErrOverflow reports signed 64-bit arithmetic that cannot be represented.
var ErrOverflow = errors.New("clockwire: signed 64-bit overflow")

// Bound is an inclusive interval of signed 64-bit integers.
//
// Bound deliberately keeps the established signed-byte wire representation:
// the two two's-complement int64 values are written big-endian, with no tag,
// varint or zig-zag transformation.  Consequently every int64 value,
// including math.MinInt64, has its usual 8-byte representation.
type Bound struct {
	Lo int64
	Hi int64
}

// New returns the inclusive interval [lo, hi].
func New(lo, hi int64) Bound { return Bound{Lo: lo, Hi: hi} }

// Empty reports whether the interval contains no integer.
func (b Bound) Empty() bool { return b.Hi < b.Lo }

// Contains reports whether v is in the inclusive interval.
func (b Bound) Contains(v int64) bool { return b.Lo <= v && v <= b.Hi }

// Low and High make the field names convenient for callers that use the
// longer spelling.
func (b Bound) Low() int64  { return b.Lo }
func (b Bound) High() int64 { return b.Hi }
func (b Bound) Min() int64  { return b.Lo }
func (b Bound) Max() int64  { return b.Hi }

// MarshalBinary encodes the bound in the historical signed-byte format.
func (b Bound) MarshalBinary() ([]byte, error) {
	out := make([]byte, EncodedBoundSize)
	binary.BigEndian.PutUint64(out[0:8], uint64(b.Lo))
	binary.BigEndian.PutUint64(out[8:16], uint64(b.Hi))
	return out, nil
}

// AppendTo appends the signed-byte representation to dst.
func (b Bound) AppendTo(dst []byte) []byte {
	var raw [EncodedBoundSize]byte
	binary.BigEndian.PutUint64(raw[0:8], uint64(b.Lo))
	binary.BigEndian.PutUint64(raw[8:16], uint64(b.Hi))
	return append(dst, raw[:]...)
}

// UnmarshalBinary decodes the signed-byte representation.
func (b *Bound) UnmarshalBinary(data []byte) error {
	if len(data) != EncodedBoundSize {
		return fmt.Errorf("clockwire: bound must be %d bytes, got %d", EncodedBoundSize, len(data))
	}
	b.Lo = int64(binary.BigEndian.Uint64(data[0:8]))
	b.Hi = int64(binary.BigEndian.Uint64(data[8:16]))
	return nil
}

// AppendBound appends b to dst.  It is the package-level spelling of
// Bound.AppendTo.
func AppendBound(dst []byte, b Bound) []byte { return b.AppendTo(dst) }

// Append is an alias for AppendBound.
func Append(dst []byte, b Bound) []byte { return b.AppendTo(dst) }

// Bytes returns the signed-byte representation.
func (b Bound) Bytes() []byte {
	out, _ := b.MarshalBinary()
	return out
}

// Encode is an alias for Bytes.
func (b Bound) Encode() []byte { return b.Bytes() }

// ParseBound decodes one bound.
func ParseBound(data []byte) (Bound, error) {
	var b Bound
	err := b.UnmarshalBinary(data)
	return b, err
}

// DecodeBound is an alias for ParseBound.
func DecodeBound(data []byte) (Bound, error) { return ParseBound(data) }

// Parse and Decode are short aliases for ParseBound.
func Parse(data []byte) (Bound, error)  { return ParseBound(data) }
func Decode(data []byte) (Bound, error) { return ParseBound(data) }

// Add returns a+b, or ErrOverflow if the mathematical result is outside the
// signed 64-bit range.
func Add(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Sub returns a-b, or ErrOverflow if the mathematical result is outside the
// signed 64-bit range.
func Sub(a, b int64) (int64, error) {
	if (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b) {
		return 0, ErrOverflow
	}
	return a - b, nil
}

// Neg returns -a, or ErrOverflow for math.MinInt64.
func Neg(a int64) (int64, error) {
	if a == math.MinInt64 {
		return 0, ErrOverflow
	}
	return -a, nil
}
