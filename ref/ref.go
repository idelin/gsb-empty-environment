// Package ref is an independent reference implementation of the floatconv
// conversion semantics, used to validate the production code. It models
// every value as an exact rational (math/big.Rat) and derives results with
// rational arithmetic, a deliberately different code path from the
// production integer rounding engine. It is not optimized for speed.
package ref

import (
	"math/big"
	"strings"

	"floatconv"
)

// Class identifies the decoded class of a bit pattern.
type Class int

const (
	Zero Class = iota
	Finite
	Inf
	NaN
)

var one = big.NewInt(1)

func signShift(f floatconv.Format) uint     { return uint(f.ExpBits + f.MantBits) }
func mantMask(f floatconv.Format) uint64    { return 1<<f.MantBits - 1 }
func maxExpField(f floatconv.Format) uint64 { return 1<<f.ExpBits - 1 }
func reservedTop(f floatconv.Format) bool   { return f.HasInf || f.HasNaN }

// Decode interprets bits under f and returns the class, the sign, and —
// for finite values — the exact rational value (always non-negative).
func Decode(f floatconv.Format, bits uint64) (Class, uint64, *big.Rat) {
	sign := bits >> signShift(f) & 1
	exp := bits >> f.MantBits & maxExpField(f)
	mant := bits & mantMask(f)
	if reservedTop(f) && exp == maxExpField(f) {
		if mant == 0 && f.HasInf {
			return Inf, sign, nil
		}
		return NaN, sign, nil
	}
	p := f.Precision()
	if exp == 0 {
		if mant == 0 {
			return Zero, sign, nil
		}
		return Finite, sign, scaledIntRat(mant, f.EMin()-(p-1))
	}
	return Finite, sign, scaledIntRat(1<<(p-1)|mant, int(exp)-f.Bias-(p-1))
}

// scaledIntRat returns m * 2^e as an exact rational.
func scaledIntRat(m uint64, e int) *big.Rat {
	num := new(big.Int).SetUint64(m)
	den := big.NewInt(1)
	if e >= 0 {
		num.Lsh(num, uint(e))
	} else {
		den.Lsh(den, uint(-e))
	}
	return new(big.Rat).SetFrac(num, den)
}

// pow2Rat returns 2^e as an exact rational.
func pow2Rat(e int) *big.Rat {
	num := big.NewInt(1)
	den := big.NewInt(1)
	if e >= 0 {
		num.Lsh(num, uint(e))
	} else {
		den.Lsh(den, uint(-e))
	}
	return new(big.Rat).SetFrac(num, den)
}

// ratFloorLog2 returns floor(log2(v)) for a positive rational v.
func ratFloorLog2(v *big.Rat) int {
	num, den := v.Num(), v.Denom()
	t := num.BitLen() - den.BitLen()
	var c int
	if t >= 0 {
		c = num.Cmp(new(big.Int).Lsh(den, uint(t)))
	} else {
		c = new(big.Int).Lsh(num, uint(-t)).Cmp(den)
	}
	if c < 0 {
		return t - 1
	}
	return t
}

// roundUpRat decides whether the truncated quotient must be incremented,
// comparing the remainder fraction rem/den against 1/2 rationally.
func roundUpRat(mode floatconv.RoundingMode, sign uint64, quo, rem, den *big.Int) bool {
	if rem.Sign() == 0 {
		return false
	}
	switch mode {
	case floatconv.RoundTowardZero:
		return false
	case floatconv.RoundTowardPositive:
		return sign == 0
	case floatconv.RoundTowardNegative:
		return sign != 0
	default:
		frac := new(big.Rat).SetFrac(rem, den)
		half := big.NewRat(1, 2)
		c := frac.Cmp(half)
		return c > 0 || (c == 0 && quo.Bit(0) != 0)
	}
}

// RoundRat rounds an exact non-negative rational value with the given sign
// into format f, returning the bit pattern and exception flags.
func RoundRat(f floatconv.Format, sign uint64, v *big.Rat, mode floatconv.RoundingMode) (uint64, floatconv.Flags) {
	p := f.Precision()
	if v.Sign() == 0 {
		return sign << signShift(f), 0
	}
	e := ratFloorLog2(v)
	normal := e >= f.EMin()
	var f0 int
	if normal {
		f0 = e - (p - 1)
	} else {
		f0 = f.EMin() - (p - 1)
	}
	// scaled = v / 2^f0; its floor is the truncated mantissa.
	scaled := new(big.Rat).Mul(v, pow2Rat(-f0))
	quo, rem := new(big.Int), new(big.Int)
	quo.QuoRem(scaled.Num(), scaled.Denom(), rem)
	inexact := rem.Sign() != 0
	if roundUpRat(mode, sign, quo, rem, scaled.Denom()) {
		quo.Add(quo, one)
	}

	var fl floatconv.Flags
	if inexact {
		fl |= floatconv.FlagInexact
	}
	var expField, mantField uint64
	if normal {
		if quo.BitLen() > p {
			quo.Rsh(quo, 1)
			e++
		}
		if e > f.EMax() {
			return overflow(f, sign, mode)
		}
		expField = uint64(e + f.Bias)
		mantField = quo.Uint64() & mantMask(f)
	} else if quo.BitLen() >= p {
		expField, mantField = 1, 0
	} else {
		expField, mantField = 0, quo.Uint64()
		if inexact {
			fl |= floatconv.FlagUnderflow
		}
	}
	return sign<<signShift(f) | expField<<f.MantBits | mantField, fl
}

func overflow(f floatconv.Format, sign uint64, mode floatconv.RoundingMode) (uint64, floatconv.Flags) {
	fl := floatconv.FlagOverflow | floatconv.FlagInexact
	toInf := mode == floatconv.RoundNearestEven ||
		(mode == floatconv.RoundTowardPositive && sign == 0) ||
		(mode == floatconv.RoundTowardNegative && sign == 1)
	if toInf && f.HasInf {
		return sign<<signShift(f) | maxExpField(f)<<f.MantBits, fl
	}
	exp := maxExpField(f)
	if reservedTop(f) {
		exp--
	}
	return sign<<signShift(f) | exp<<f.MantBits | mantMask(f), fl
}

// Convert converts one element from src to dst, mirroring the documented
// floatconv semantics through the rational code path.
func Convert(dst, src floatconv.Format, bits uint64, mode floatconv.RoundingMode) (uint64, floatconv.Flags) {
	cls, sign, val := Decode(src, bits)
	switch cls {
	case Zero:
		return sign << signShift(dst), 0
	case Inf:
		if dst.HasInf {
			return sign<<signShift(dst) | maxExpField(dst)<<dst.MantBits, 0
		}
		exp := maxExpField(dst)
		if reservedTop(dst) {
			exp--
		}
		return sign<<signShift(dst) | exp<<dst.MantBits | mantMask(dst), floatconv.FlagOverflow | floatconv.FlagInexact
	case NaN:
		return convertNaN(dst, src, sign, bits&mantMask(src))
	}
	return RoundRat(dst, sign, val, mode)
}

func convertNaN(dst, src floatconv.Format, sign, srcMant uint64) (uint64, floatconv.Flags) {
	var fl floatconv.Flags
	if srcMant != 0 && srcMant&(1<<(src.MantBits-1)) == 0 {
		fl |= floatconv.FlagInvalid
	}
	var m uint64
	if src.MantBits >= dst.MantBits {
		m = srcMant >> (src.MantBits - dst.MantBits)
	} else {
		m = srcMant << (dst.MantBits - src.MantBits)
	}
	if !dst.HasNaN {
		exp := maxExpField(dst)
		if reservedTop(dst) {
			exp--
		}
		return sign<<signShift(dst) | exp<<dst.MantBits | mantMask(dst), fl | floatconv.FlagInvalid
	}
	m |= 1 << (dst.MantBits - 1)
	return sign<<signShift(dst) | maxExpField(dst)<<dst.MantBits | m, fl
}

// ParseDecimal converts an exact decimal string into f by first building
// the exact rational value (big.Rat.SetString handles decimal exponents)
// and then rounding once.
func ParseDecimal(f floatconv.Format, s string, mode floatconv.RoundingMode) (uint64, floatconv.Flags, error) {
	t := s
	var sign uint64
	if strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-") {
		if t[0] == '-' {
			sign = 1
		}
		t = t[1:]
	}
	switch strings.ToLower(t) {
	case "inf", "infinity":
		if f.HasInf {
			return sign<<signShift(f) | maxExpField(f)<<f.MantBits, 0, nil
		}
		exp := maxExpField(f)
		if reservedTop(f) {
			exp--
		}
		return sign<<signShift(f) | exp<<f.MantBits | mantMask(f), floatconv.FlagOverflow | floatconv.FlagInexact, nil
	case "nan":
		b, fl := convertNaN(f, f, sign, 1<<(f.MantBits-1))
		return b, fl, nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, 0, floatconv.ErrSyntax
	}
	if r.Sign() < 0 {
		r.Neg(r)
	}
	bits, fl := RoundRat(f, sign, r, mode)
	return bits, fl, nil
}
