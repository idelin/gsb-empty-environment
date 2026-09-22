// Package fp implements conversion between configurable binary floating-point
// formats using exact integer arithmetic.
//
// A format is defined by the number of exponent bits, trailing significand
// (mantissa) bits and the exponent bias. Special-value rules follow the
// IEEE 754 conventions: the largest exponent encoding denotes Infinity (zero
// mantissa) and NaN (nonzero mantissa); the most significant mantissa bit is
// the quiet bit.
package fp

import (
	"errors"
)

// Rounding direction.
type Round uint8

const (
	ToNearestEven Round = iota // round ties to even
	ToZero                     // truncate toward zero
	ToPositiveInf              // round toward +infinity
	ToNegativeInf              // round toward -infinity
)

// Flags are raised by conversion operations and reported to the caller.
type Flags uint8

const (
	FlagInvalid   Flags = 1 << iota // invalid operation (e.g. sNaN)
	FlagOverflow                    // rounded result magnitude exceeds the finite range
	FlagUnderflow                   // result is tiny AND inexact
	FlagInexact                     // result differs from the exact value
)

func (f Flags) String() string {
	var b []byte
	add := func(c byte, on bool) {
		if on {
			if len(b) > 0 {
				b = append(b, '|')
			}
			b = append(b, c)
		}
	}
	add('V', f&FlagInvalid != 0)
	add('O', f&FlagOverflow != 0)
	add('U', f&FlagUnderflow != 0)
	add('X', f&FlagInexact != 0)
	if len(b) == 0 {
		return "ok"
	}
	return string(b)
}

// Format describes a binary floating-point encoding.
//
// Bits holds the encoded sign, exponent and trailing significand. The quiet
// bit is the most significant trailing-significand bit. Total width is
// ExpBits+MantBits+1 and must be at most 64 and a multiple of 8.
type Format struct {
	ExpBits  uint // exponent field width, 1..63
	MantBits uint // trailing significand field width, 0..62
	Bias     int  // exponent bias

	total uint
	p     uint // precision = MantBits + 1
	emax  int
	emin  int
}

// NewFormat validates and prepares a format definition.
func NewFormat(expBits, mantBits uint, bias int) (*Format, error) {
	f := &Format{ExpBits: expBits, MantBits: mantBits, Bias: bias}
	if err := f.validate(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *Format) validate() error {
	if f.ExpBits < 1 || f.ExpBits > 63 {
		return errors.New("fp: exponent bits out of range")
	}
	if f.MantBits > 62 {
		return errors.New("fp: mantissa bits out of range")
	}
	total := f.ExpBits + f.MantBits + 1
	if total > 64 || total%8 != 0 {
		return errors.New("fp: total bit width must be a byte multiple and at most 64")
	}
	if f.Bias < 1 {
		return errors.New("fp: bias must be positive")
	}
	// Biased exponent 0 is reserved for zero/subnormals and biased
	// exponent 2^expbits-1 for Infinity/NaN, so the normal range is
	// biased 1 .. 2^expbits-2.
	emax := (1<<f.ExpBits - 2) - f.Bias
	if emax < 1 {
		return errors.New("fp: bias inconsistent with exponent range")
	}
	f.total = total
	f.p = f.MantBits + 1
	f.emax = emax
	f.emin = 1 - emax
	return nil
}

// Common built-in formats.
var (
	Binary16 = mustFormat(5, 10, 15)
	BFloat16 = mustFormat(8, 7, 127)
	Binary32 = mustFormat(8, 23, 127)
	Binary64 = mustFormat(11, 52, 1023)
)

func mustFormat(e, m uint, b int) *Format {
	f, err := NewFormat(e, m, b)
	if err != nil {
		panic(err)
	}
	return f
}

// Width reports the encoded size in bytes.
func (f *Format) Width() int { return int(f.total / 8) }

// maxFinite is the largest finite encoded value.
func (f *Format) maxFinite() uint64 {
	return (uint64(1)<<f.total - 1) &^ (uint64(1) << f.MantBits)
}

// pack builds an encoded bit pattern from sign, biased exponent and mantissa.
func (f *Format) pack(neg bool, exp uint64, mant uint64) uint64 {
	v := mant
	if neg {
		v |= uint64(1) << (f.total - 1)
	}
	v |= exp << f.MantBits
	return v
}

// unpack splits an encoded bit pattern.
func (f *Format) unpack(raw uint64) (neg bool, exp uint64, mant uint64) {
	neg = raw>>(f.total-1) != 0
	exp = (raw >> f.MantBits) & ((uint64(1) << f.ExpBits) - 1)
	mant = raw & (uint64(1)<<f.MantBits - 1)
	return
}

func (f *Format) quietBit() uint64 { return uint64(1) << (f.MantBits - 1) }
func (f *Format) mantMask() uint64 { return uint64(1)<<f.MantBits - 1 }

// infBits returns the Infinity encoding.
func (f *Format) infBits(neg bool) uint64 {
	return f.pack(neg, uint64(1)<<f.ExpBits-1, 0)
}

// zeroBits returns the signed zero encoding.
func (f *Format) zeroBits(neg bool) uint64 {
	var v uint64
	if neg {
		v = uint64(1) << (f.total - 1)
	}
	return v
}

// qnanBits builds a quiet NaN with the given payload and sign.
func (f *Format) qnanBits(neg bool, payload uint64) uint64 {
	if f.MantBits == 0 {
		return f.pack(neg, uint64(1)<<f.ExpBits-1, 0)
	}
	m := payload & f.mantMask()
	m |= f.quietBit()
	return f.pack(neg, uint64(1)<<f.ExpBits-1, m)
}
