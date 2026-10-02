// Package floatwire provides bit-exact serialization and conversion of
// IEEE 754 binary16/binary32/binary64 floating-point values.
//
// Design contract:
//   - Values are handled as raw bit patterns (uint64), never through host
//     float arithmetic, so signed zero, infinities and NaN payloads survive
//     every round trip.
//   - Text display (Format) is fully separated from exact storage (Encode).
//   - The wire form is a fixed-width byte sequence; canonical order is
//     big-endian, with an explicit little-endian option. Either order
//     restores bit-identical values.
//   - Decode validates the input length before allocating or reading
//     anything, and never executes host code paths driven by the payload.
//   - Unsupported bit widths are rejected up front with ErrUnsupportedWidth.
//   - NaN must never be compared with host "=="; use Equal, which compares
//     bit patterns, so NaN values are not misjudged as unequal-to-self.
package floatwire

import (
	"encoding/binary"
	"errors"
)

// Width is a floating-point bit width. Only 16, 32 and 64 are supported.
type Width int

const (
	Float16 Width = 16
	Float32 Width = 32
	Float64 Width = 64
)

var (
	// ErrUnsupportedWidth is returned for any width other than 16/32/64.
	ErrUnsupportedWidth = errors.New("floatwire: unsupported bit width")
	// ErrShortBuffer is returned when the encoded input has the wrong length.
	ErrShortBuffer = errors.New("floatwire: encoded input has wrong length")
	// ErrBadFrame is returned for a malformed framed record.
	ErrBadFrame = errors.New("floatwire: malformed frame")
)

// Valid reports whether w is a supported width.
func (w Width) Valid() bool {
	return w == Float16 || w == Float32 || w == Float64
}

// Bytes returns the encoded byte length of one value of width w.
func (w Width) Bytes() int { return int(w) / 8 }

// Order is a byte order for the wire form.
type Order int

const (
	// BigEndian is the canonical network order (default).
	BigEndian Order = iota
	// LittleEndian stores the least significant byte first.
	LittleEndian
)

// mask returns the low n bits set.
func mask(n uint) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << n) - 1
}

// MaskBits truncates bits to the width w. Unsupported widths return 0.
func MaskBits(w Width, bits uint64) uint64 {
	if !w.Valid() {
		return 0
	}
	return bits & mask(uint(w))
}

// Encode serializes the low w bits of bits in canonical big-endian order.
// The width is validated before any allocation happens.
func Encode(w Width, bits uint64) ([]byte, error) {
	return EncodeOrder(w, bits, BigEndian)
}

// EncodeOrder serializes the low w bits of bits using the given byte order.
func EncodeOrder(w Width, bits uint64, order Order) ([]byte, error) {
	if !w.Valid() {
		return nil, ErrUnsupportedWidth
	}
	out := make([]byte, w.Bytes())
	put(order, out, bits)
	return out, nil
}

// Decode parses exactly w.Bytes() bytes in canonical big-endian order.
// The length is validated before any byte is read.
func Decode(w Width, data []byte) (uint64, error) {
	return DecodeOrder(w, data, BigEndian)
}

// DecodeOrder parses exactly w.Bytes() bytes using the given byte order.
// Length validation happens before any read; the payload only ever feeds
// shifts and ORs, never host code.
func DecodeOrder(w Width, data []byte, order Order) (uint64, error) {
	if !w.Valid() {
		return 0, ErrUnsupportedWidth
	}
	if len(data) != w.Bytes() {
		return 0, ErrShortBuffer
	}
	return get(order, data), nil
}

func put(order Order, out []byte, bits uint64) {
	switch order {
	case LittleEndian:
		switch len(out) {
		case 2:
			binary.LittleEndian.PutUint16(out, uint16(bits))
		case 4:
			binary.LittleEndian.PutUint32(out, uint32(bits))
		case 8:
			binary.LittleEndian.PutUint64(out, bits)
		}
	default:
		switch len(out) {
		case 2:
			binary.BigEndian.PutUint16(out, uint16(bits))
		case 4:
			binary.BigEndian.PutUint32(out, uint32(bits))
		case 8:
			binary.BigEndian.PutUint64(out, bits)
		}
	}
}

func get(order Order, data []byte) uint64 {
	switch order {
	case LittleEndian:
		switch len(data) {
		case 2:
			return uint64(binary.LittleEndian.Uint16(data))
		case 4:
			return uint64(binary.LittleEndian.Uint32(data))
		case 8:
			return binary.LittleEndian.Uint64(data)
		}
	default:
		switch len(data) {
		case 2:
			return uint64(binary.BigEndian.Uint16(data))
		case 4:
			return uint64(binary.BigEndian.Uint32(data))
		case 8:
			return binary.BigEndian.Uint64(data)
		}
	}
	return 0
}

// Field layout of a supported format.
type layout struct {
	width   Width
	manBits uint // stored mantissa bits (10, 23, 52)
	expBits uint // exponent field bits (5, 8, 11)
	bias    int  // exponent bias (15, 127, 1023)
}

func layoutOf(w Width) (layout, bool) {
	switch w {
	case Float16:
		return layout{w, 10, 5, 15}, true
	case Float32:
		return layout{w, 23, 8, 127}, true
	case Float64:
		return layout{w, 52, 11, 1023}, true
	}
	return layout{}, false
}

func (l layout) signMask() uint64 { return uint64(1) << (uint(l.width) - 1) }
func (l layout) expMask() uint64  { return mask(l.expBits) << l.manBits }
func (l layout) manMask() uint64  { return mask(l.manBits) }
func (l layout) quietBit() uint64 { return uint64(1) << (l.manBits - 1) }

// Class is the IEEE 754 classification of a bit pattern.
type Class int

const (
	Zero Class = iota
	Subnormal
	Normal
	Infinite
	NaN
)

// Classify returns the class of a bit pattern without host float arithmetic.
func Classify(w Width, bits uint64) Class {
	l, ok := layoutOf(w)
	if !ok {
		return NaN
	}
	exp := (bits & l.expMask()) >> l.manBits
	man := bits & l.manMask()
	switch {
	case exp == mask(l.expBits):
		if man == 0 {
			return Infinite
		}
		return NaN
	case exp == 0:
		if man == 0 {
			return Zero
		}
		return Subnormal
	}
	return Normal
}

// Sign returns the sign bit (0 or 1) of the pattern.
func Sign(w Width, bits uint64) uint64 {
	l, ok := layoutOf(w)
	if !ok {
		return 0
	}
	return (bits & l.signMask()) >> (uint(l.width) - 1)
}

// IsNaN reports whether the pattern is any NaN.
func IsNaN(w Width, bits uint64) bool { return Classify(w, bits) == NaN }

// IsSignaling reports whether the pattern is a signaling NaN
// (quiet bit clear). Returns false for non-NaN patterns.
func IsSignaling(w Width, bits uint64) bool {
	l, ok := layoutOf(w)
	if !ok || Classify(w, bits) != NaN {
		return false
	}
	return bits&l.quietBit() == 0
}

// NaNPayload returns the payload bits of a NaN with the quiet/signaling
// bit excluded. Returns 0 for non-NaN patterns.
func NaNPayload(w Width, bits uint64) uint64 {
	l, ok := layoutOf(w)
	if !ok || Classify(w, bits) != NaN {
		return 0
	}
	return bits & (l.quietBit() - 1)
}

// Equal compares two patterns bit-for-bit. Unlike host "==" on floats,
// identical NaN patterns compare equal and +0 differs from -0, so NaN
// values are never misjudged by ordinary equality and the negative-zero
// sign is observable.
func Equal(w Width, a, b uint64) bool {
	return MaskBits(w, a) == MaskBits(w, b)
}
