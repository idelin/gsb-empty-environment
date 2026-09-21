package floatconv

import "math/big"

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
)

// exactValue is an exact real number: (-1)^sign * num/den * 2^exp2,
// with num >= 0 and den >= 1. Production conversions reduce every input
// (source bit pattern or decimal string) to this form and round it into
// the destination format using integer arithmetic only.
type exactValue struct {
	sign uint64
	num  *big.Int
	den  *big.Int
	exp2 int
}

type valueClass int

const (
	classZero valueClass = iota
	classFinite
	classInf
	classNaN
)

type decoded struct {
	class valueClass
	sign  uint64
	num   *big.Int // finite: integer significand, value = num * 2^exp2
	exp2  int
	mant  uint64 // NaN: raw mantissa (payload) field
}

// decode interprets bits under format f. Bits above the format width are
// ignored.
func decode(f Format, bits uint64) decoded {
	sign := bits >> f.signShift() & 1
	exp := bits >> f.MantBits & f.maxExpField()
	mant := bits & f.mantMask()
	if f.reservedTop() && exp == f.maxExpField() {
		if mant == 0 && f.HasInf {
			return decoded{class: classInf, sign: sign}
		}
		return decoded{class: classNaN, sign: sign, mant: mant}
	}
	p := f.Precision()
	if exp == 0 {
		if mant == 0 {
			return decoded{class: classZero, sign: sign}
		}
		// Subnormal: value = mant * 2^(emin-(p-1)).
		return decoded{
			class: classFinite, sign: sign,
			num:  new(big.Int).SetUint64(mant),
			exp2: f.EMin() - (p - 1),
		}
	}
	// Normal: value = (2^(p-1) + mant) * 2^(exp-bias-(p-1)).
	return decoded{
		class: classFinite, sign: sign,
		num:  new(big.Int).SetUint64(1<<(p-1) | mant),
		exp2: int(exp) - f.Bias - (p - 1),
	}
}

// Convert converts a single element from the source format to the
// destination format under the given rounding mode, returning the
// destination bit pattern and the raised exception flags.
func Convert(dst, src Format, bits uint64, mode RoundingMode) (uint64, Flags, error) {
	if err := dst.Validate(); err != nil {
		return 0, 0, err
	}
	if err := src.Validate(); err != nil {
		return 0, 0, err
	}
	out, fl := convert(dst, src, bits, mode)
	return out, fl, nil
}

// convert is the unvalidated inner conversion used by ConvertBatch.
func convert(dst, src Format, bits uint64, mode RoundingMode) (uint64, Flags) {
	d := decode(src, bits)
	switch d.class {
	case classZero:
		// Signed zeros are preserved.
		return d.sign << dst.signShift(), 0
	case classInf:
		if dst.HasInf {
			return dst.infBits(d.sign), 0
		}
		// Infinity into a finite-only format saturates to max finite.
		return dst.maxFiniteBits(d.sign), FlagOverflow | FlagInexact
	case classNaN:
		return convertNaN(dst, src, d.sign, d.mant)
	default:
		return roundToFormat(dst, exactValue{sign: d.sign, num: d.num, den: bigOne, exp2: d.exp2}, mode)
	}
}

// convertNaN maps a NaN between formats.
//
// Rules: the sign is preserved. The payload is left-aligned: when
// narrowing, the most significant payload bits are kept (truncation); when
// widening, low bits are zero-filled. The destination quiet bit (MSB of
// the mantissa field) is always set, so a signaling NaN becomes quiet and
// raises FlagInvalid. The forced quiet bit also guarantees the result
// never degenerates to an infinity encoding.
func convertNaN(dst, src Format, sign, srcMant uint64) (uint64, Flags) {
	var fl Flags
	if srcMant != 0 && srcMant&src.quietBit() == 0 {
		fl |= FlagInvalid // signaling NaN
	}
	var m uint64
	switch {
	case src.MantBits >= dst.MantBits:
		m = srcMant >> (src.MantBits - dst.MantBits)
	default:
		m = srcMant << (dst.MantBits - src.MantBits)
	}
	if !dst.HasNaN {
		return dst.maxFiniteBits(sign), fl | FlagInvalid
	}
	m |= dst.quietBit()
	return sign<<dst.signShift() | dst.maxExpField()<<dst.MantBits | m, fl
}

// overflowResult delivers the overflow result required by the rounding
// mode: nearest and same-direction modes produce infinity (when the format
// has one), the others saturate to the largest finite magnitude.
func overflowResult(f Format, sign uint64, mode RoundingMode) (uint64, Flags) {
	fl := FlagOverflow | FlagInexact
	toInf := false
	switch mode {
	case RoundNearestEven:
		toInf = true
	case RoundTowardPositive:
		toInf = sign == 0
	case RoundTowardNegative:
		toInf = sign == 1
	}
	if toInf && f.HasInf {
		return f.infBits(sign), fl
	}
	return f.maxFiniteBits(sign), fl
}

// floorLog2 returns floor(log2(num/den)) for positive num, den.
func floorLog2(num, den *big.Int) int {
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

// roundUp decides whether the truncated quotient quo (with remainder
// rem/den) must be incremented under the rounding mode.
func roundUp(mode RoundingMode, sign uint64, quo, rem, den *big.Int) bool {
	if rem.Sign() == 0 {
		return false
	}
	switch mode {
	case RoundTowardZero:
		return false
	case RoundTowardPositive:
		return sign == 0
	case RoundTowardNegative:
		return sign != 0
	default: // RoundNearestEven
		c := new(big.Int).Lsh(rem, 1).Cmp(den)
		return c > 0 || (c == 0 && quo.Bit(0) != 0)
	}
}

// roundToFormat rounds an exact finite value into format f. This is the
// production rounding engine; it uses only exact integer arithmetic.
func roundToFormat(f Format, v exactValue, mode RoundingMode) (uint64, Flags) {
	p := f.Precision()
	if v.num.Sign() == 0 {
		return v.sign << f.signShift(), 0
	}

	// e = floor(log2|v|).
	e := floorLog2(v.num, v.den) + v.exp2

	// f0 is the weight of the least significant delivered bit: the result
	// mantissa m satisfies result = m * 2^f0. For normals m has p bits;
	// for subnormals the scale is frozen at emin-(p-1).
	normal := e >= f.EMin()
	var f0 int
	if normal {
		f0 = e - (p - 1)
	} else {
		f0 = f.EMin() - (p - 1)
	}

	// m = round(v / 2^f0) = round(num * 2^(exp2-f0) / den).
	num, den := v.num, v.den
	if s := v.exp2 - f0; s >= 0 {
		num = new(big.Int).Lsh(num, uint(s))
	} else {
		den = new(big.Int).Lsh(den, uint(-s))
	}
	quo, rem := new(big.Int), new(big.Int)
	quo.QuoRem(num, den, rem)
	inexact := rem.Sign() != 0
	if roundUp(mode, v.sign, quo, rem, den) {
		quo.Add(quo, bigOne)
	}

	var fl Flags
	if inexact {
		fl |= FlagInexact
	}

	var expField, mantField uint64
	if normal {
		if quo.BitLen() > p { // rounding carried out of the significand
			quo.Rsh(quo, 1)
			e++
		}
		if e > f.EMax() {
			return overflowResult(f, v.sign, mode)
		}
		expField = uint64(e + f.Bias)
		mantField = quo.Uint64() & f.mantMask()
	} else if quo.BitLen() >= p {
		// Rounded up to the smallest normal: not tiny after rounding,
		// so no underflow is signaled.
		expField, mantField = 1, 0
	} else {
		// Subnormal or zero result: tiny after rounding. Underflow is
		// signaled exactly when the result is also inexact.
		expField, mantField = 0, quo.Uint64()
		if inexact {
			fl |= FlagUnderflow
		}
	}
	return v.sign<<f.signShift() | expField<<f.MantBits | mantField, fl
}
