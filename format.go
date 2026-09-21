// Package floatconv converts between configurable binary floating-point
// formats. A format is described by its sign bit, exponent width, mantissa
// width, exponent bias and special-value rules. All conversion logic works
// directly on bit patterns and exact integers (math/big); host floating
// point types are never used, so conversions never suffer from double
// rounding through an intermediate host format.
package floatconv

import (
	"errors"
	"fmt"
)

// Format describes a binary floating-point format.
//
// Layout (total 1+ExpBits+MantBits bits, stored little-endian in batch
// buffers):
//
//	[sign:1][exponent:ExpBits][mantissa:MantBits]
//
// Special-value rules:
//   - If HasInf or HasNaN is set, the all-ones exponent field is reserved.
//     With HasInf, an all-ones exponent with a zero mantissa encodes
//     infinity. With HasNaN, an all-ones exponent with a non-zero mantissa
//     encodes NaN (the most significant mantissa bit is the quiet bit).
//     If HasNaN is set but HasInf is not, an all-ones exponent with a zero
//     mantissa encodes a canonical quiet NaN.
//   - If neither is set, every exponent value encodes a finite number.
type Format struct {
	Name     string
	ExpBits  int // number of exponent bits
	MantBits int // number of stored mantissa bits (implicit leading 1 excluded)
	Bias     int // exponent bias
	HasInf   bool
	HasNaN   bool
}

// Predefined formats.
var (
	Binary16 = Format{Name: "binary16", ExpBits: 5, MantBits: 10, Bias: 15, HasInf: true, HasNaN: true}
	BFloat16 = Format{Name: "bfloat16", ExpBits: 8, MantBits: 7, Bias: 127, HasInf: true, HasNaN: true}
	Binary32 = Format{Name: "binary32", ExpBits: 8, MantBits: 23, Bias: 127, HasInf: true, HasNaN: true}
	Binary64 = Format{Name: "binary64", ExpBits: 11, MantBits: 52, Bias: 1023, HasInf: true, HasNaN: true}
)

// ErrInvalidFormat is returned (wrapped) when a Format fails Validate.
var ErrInvalidFormat = errors.New("floatconv: invalid format")

// Validate reports whether the format description is structurally usable.
func (f Format) Validate() error {
	if f.ExpBits < 2 || f.ExpBits > 15 {
		return fmt.Errorf("%w: exponent bits %d outside [2,15]", ErrInvalidFormat, f.ExpBits)
	}
	if f.MantBits < 1 || f.MantBits > 61 {
		return fmt.Errorf("%w: mantissa bits %d outside [1,61]", ErrInvalidFormat, f.MantBits)
	}
	total := 1 + f.ExpBits + f.MantBits
	if total%8 != 0 || total > 64 {
		return fmt.Errorf("%w: total width %d is not a multiple of 8 bits within 64", ErrInvalidFormat, total)
	}
	if f.Bias < 0 || f.Bias > 1<<15 {
		return fmt.Errorf("%w: exponent bias %d outside [0,32768]", ErrInvalidFormat, f.Bias)
	}
	if f.HasInf && !f.HasNaN {
		return fmt.Errorf("%w: infinity support requires NaN support", ErrInvalidFormat)
	}
	return nil
}

func (f Format) String() string {
	if f.Name != "" {
		return f.Name
	}
	return fmt.Sprintf("fp<E%d,M%d,B%d>", f.ExpBits, f.MantBits, f.Bias)
}

// Size returns the element size in bytes.
func (f Format) Size() int { return (1 + f.ExpBits + f.MantBits) / 8 }

// Precision returns the significand precision in bits, including the
// implicit leading bit of normal numbers.
func (f Format) Precision() int { return f.MantBits + 1 }

// EMin returns the minimum normal unbiased exponent.
func (f Format) EMin() int { return 1 - f.Bias }

// EMax returns the maximum normal unbiased exponent.
func (f Format) EMax() int {
	maxField := int(f.maxExpField())
	if f.reservedTop() {
		maxField--
	}
	return maxField - f.Bias
}

func (f Format) reservedTop() bool { return f.HasInf || f.HasNaN }

func (f Format) maxExpField() uint64 { return 1<<f.ExpBits - 1 }

func (f Format) mantMask() uint64 { return 1<<f.MantBits - 1 }

func (f Format) signShift() uint { return uint(f.ExpBits + f.MantBits) }

func (f Format) quietBit() uint64 { return 1 << (f.MantBits - 1) }

func (f Format) maxFiniteBits(sign uint64) uint64 {
	exp := f.maxExpField()
	if f.reservedTop() {
		exp--
	}
	return sign<<f.signShift() | exp<<f.MantBits | f.mantMask()
}

func (f Format) infBits(sign uint64) uint64 {
	return sign<<f.signShift() | f.maxExpField()<<f.MantBits
}
