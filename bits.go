// Package fpcodec provides bit-exact binary encoding of IEEE-754-style
// floating-point values (16/32/64-bit) and correctly rounded, purely
// integer-based format conversion with exception flags.
//
// Storage and text display are strictly separated: the wire format carries
// raw bit patterns (preserving -0, infinities and full NaN payloads), while
// Text renders a human-readable form that is never used for storage.
package fpcodec

import (
	"errors"
	"math/bits"
)

// Width is a floating-point bit width.
type Width int

const (
	Width16 Width = 16
	Width32 Width = 32
	Width64 Width = 64
)

// ErrUnsupportedWidth is returned for any width other than 16, 32 or 64.
var ErrUnsupportedWidth = errors.New("fpcodec: unsupported float width (only 16, 32, 64)")

// layout describes an IEEE-754-style binary interchange format.
type layout struct {
	expBits  uint
	mantBits uint
	bias     int
}

func (l layout) total() uint { return 1 + l.expBits + l.mantBits }
func (l layout) emin() int   { return 1 - l.bias } // min normal unbiased exponent
func (l layout) emax() int   { return l.bias }     // max normal unbiased exponent
func (l layout) expMask() uint64 {
	return (uint64(1) << l.expBits) - 1
}
func (l layout) mantMask() uint64 {
	return (uint64(1) << l.mantBits) - 1
}

func layoutOf(w Width) (layout, error) {
	switch w {
	case Width16:
		return layout{expBits: 5, mantBits: 10, bias: 15}, nil
	case Width32:
		return layout{expBits: 8, mantBits: 23, bias: 127}, nil
	case Width64:
		return layout{expBits: 11, mantBits: 52, bias: 1023}, nil
	}
	return layout{}, ErrUnsupportedWidth
}

// mustLayout is for internal call sites that already validated the width.
func mustLayout(w Width) layout {
	l, err := layoutOf(w)
	if err != nil {
		panic(err)
	}
	return l
}

// maskBits keeps only the low w bits of a pattern.
func maskBits(w Width, b uint64) uint64 {
	if w >= 64 {
		return b
	}
	return b & ((uint64(1) << uint(w)) - 1)
}

type class int

const (
	clsZero class = iota
	clsSubnormal
	clsNormal
	clsInf
	clsNaN
)

// unpacked is the exact integer decomposition of a bit pattern.
// For finite values the value is exactly (-1)^sign * mant * 2^exp.
type unpacked struct {
	sign      uint64
	cls       class
	mant      uint64 // significand; includes implicit bit for normals
	exp       int    // exponent of the least significant mantissa bit
	payload   uint64 // raw NaN payload (mantissa field), quiet bit included
	signaling bool   // NaN with quiet bit clear
}

// unpack decomposes a bit pattern using integer math only.
func unpack(l layout, b uint64) unpacked {
	b = maskBits(Width(l.total()), b)
	u := unpacked{sign: b >> (l.total() - 1)}
	expField := (b >> l.mantBits) & l.expMask()
	mantField := b & l.mantMask()
	switch {
	case expField == l.expMask():
		if mantField == 0 {
			u.cls = clsInf
		} else {
			u.cls = clsNaN
			u.payload = mantField
			u.signaling = mantField&(uint64(1)<<(l.mantBits-1)) == 0
		}
	case expField == 0:
		if mantField == 0 {
			u.cls = clsZero
		} else {
			u.cls = clsSubnormal
			u.mant = mantField
			u.exp = l.emin() - int(l.mantBits)
		}
	default:
		u.cls = clsNormal
		u.mant = mantField | (uint64(1) << l.mantBits)
		u.exp = int(expField) - l.bias - int(l.mantBits)
	}
	return u
}

// roundRNE shifts m right by shift (left if negative) with
// round-to-nearest, ties-to-even. It reports whether bits were lost.
func roundRNE(m uint64, shift int) (q uint64, inexact bool) {
	if shift <= 0 {
		return m << uint(-shift), false
	}
	s := uint(shift)
	if s >= 64 {
		if m == 0 {
			return 0, false
		}
		return 0, true // shifted entirely away
	}
	q = m >> s
	rem := m & ((uint64(1) << s) - 1)
	half := uint64(1) << (s - 1)
	switch {
	case rem > half:
		q++
	case rem == half && q&1 == 1:
		q++
	}
	return q, rem != 0
}

// bitLen returns the number of significant bits in m (m != 0).
func bitLen(m uint64) int { return bits.Len64(m) }
