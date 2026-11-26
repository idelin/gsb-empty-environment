package decimal128

import (
	"errors"
	"math/bits"
)

// MaxPrecision is the largest DECIMAL precision supported by the kernel.
const MaxPrecision = 38

// MaxScale is the largest admissible scale; coefficients stay within the
// signed 128-bit range because 10^38 < 2^127.
const MaxScale = 38

const maxPrecision = MaxPrecision
const maxScale = MaxScale

// RoundingMode selects how an exact result is reduced to a smaller scale.
type RoundingMode int

const (
	// HalfAwayFromZero rounds half values away from zero (commercial rounding).
	HalfAwayFromZero RoundingMode = iota
	// HalfToEven applies banker's rounding: halves go to the even neighbour.
	HalfToEven
	// Truncate rounds toward zero; it is used internally for integer division.
	Truncate
)

// DefaultRoundingMode is used by the top-level arithmetic helpers that do not
// take an explicit mode.
var DefaultRoundingMode = HalfAwayFromZero

var (
	// ErrOverflow means a result's magnitude exceeds 10^38 - 1.
	ErrOverflow = errors.New("decimal128: value out of range (exceeds 38 significant digits)")
	// ErrDivisionByZero is returned for a zero divisor.
	ErrDivisionByZero = errors.New("decimal128: division by zero")
	// ErrSyntax is returned when a decimal string cannot be parsed.
	ErrSyntax = errors.New("decimal128: invalid decimal syntax")
	// ErrInvalidScale is returned when a scale is outside [0, 38].
	ErrInvalidScale = errors.New("decimal128: scale out of range [0, 38]")
)

// Decimal128 is a fixed-point decimal. The represented value is
//
//	coeff * 10^(-scale)
//
// where coeff is a custom signed 128-bit two's-complement integer. Invariants:
//   - 0 <= scale <= 38;
//   - precision is the SQL DECIMAL precision (number of significant digits),
//     1 <= precision <= 38, with precision >= scale;
//   - |coeff| <= 10^38 - 1.
type Decimal128 struct {
	coeff     i128
	precision uint8
	scale     uint8
}

// Precision returns the declared SQL precision.
func (d Decimal128) Precision() int { return int(d.precision) }

// Scale returns the decimal scale (digits after the decimal point).
func (d Decimal128) Scale() int { return int(d.scale) }

// IsZero reports whether the value is zero.
func (d Decimal128) IsZero() bool { return d.coeff.isZero() }

// Sign returns -1, 0 or 1 like math/big.Rat.Sign.
func (d Decimal128) Sign() int { return d.coeff.sign() }

// New constructs a decimal with the given signed coefficient and scale. The
// coefficient must already lie within the 38-digit range.
func New(coeff int64, scale uint8) (Decimal128, error) {
	if int(scale) > maxScale {
		return Decimal128{}, ErrInvalidScale
	}
	c := i128FromInt64(coeff)
	mag, _ := c.magnitude()
	if u256FromU128(mag).cmp(pow10u256[maxPrecision]) >= 0 {
		return Decimal128{}, ErrOverflow
	}
	digits := mag.decimalDigits()
	integerDigits := digits - int(scale)
	if integerDigits < 1 {
		integerDigits = 1
	}
	p := integerDigits + int(scale)
	return Decimal128{coeff: c, precision: uint8(p), scale: scale}, nil
}

// wide is a signed value whose magnitude may need up to 256 bits. It is used
// only for lossless intermediate alignment and multiplication.
type wide struct {
	mag      u256
	negative bool
}

// addWide computes |a +/- b| without signed two's-complement bookkeeping.
func addWide(a, b wide, subtract bool) wide {
	bsign := b.negative
	if subtract {
		bsign = !bsign
	}
	if a.negative == bsign {
		return wide{mag: a.mag.add256(b.mag), negative: a.negative}
	}
	switch a.mag.cmp(b.mag) {
	case 0:
		return wide{}
	case 1:
		return wide{mag: a.mag.sub(b.mag), negative: a.negative}
	default:
		return wide{mag: b.mag.sub(a.mag), negative: bsign}
	}
}

func (u u256) add256(v u256) u256 {
	var r u256
	var carry uint64
	for i := 0; i < 4; i++ {
		r.v[i], carry = bits.Add64(u.v[i], v.v[i], carry)
	}
	return r
}

// canonicalize reduces a lossless wide coefficient at the given scale to a
// valid Decimal128: when more than 38 significant digits are required, the
// scale is reduced (digits discarded) using mode, and overflow past
// 10^38 - 1 is reported.
func canonicalize(w wide, scale int, mode RoundingMode) (Decimal128, error) {
	if scale < 0 || scale > 2*maxScale {
		return Decimal128{}, ErrInvalidScale
	}
	if w.mag.isZero() {
		s := scale
		if s > maxScale {
			s = maxScale
		}
		return Decimal128{coeff: i128{}, precision: uint8(maxInt(1, s)), scale: uint8(s)}, nil
	}

	// digits is the raw coefficient length; integer digits of the represented
	// value is max(digits-scale, 0).
	digits := w.mag.decimalDigits()
	intDigits := digits - scale
	if intDigits < 0 {
		intDigits = 0
	}
	// The result scale starts at the SQL multiplication scale (s1+s2, possibly
	// above 38) and is reduced by the significant-digit limit below.
	target := scale
	if target > maxScale {
		target = maxScale
	}
	if digits > maxPrecision {
		// Keep at most 38 significant digits: reduce the scale (fraction
		// digits) by the coefficient excess.
		if reduced := scale - (digits - maxPrecision); reduced < target {
			target = reduced
		}
	}
	if target < 0 || intDigits > maxPrecision {
		return Decimal128{}, ErrOverflow
	}

	mag := w.mag
	if target < scale {
		drop := scale - target
		divisor := pow10u256[drop]
		q, rem := mag.divMod(divisor)
		half := divisor.shr1()
		up := false
		switch mode {
		case Truncate:
			up = false
		case HalfAwayFromZero:
			up = rem.cmp(half) >= 0
		case HalfToEven:
			switch rem.cmp(half) {
			case 1:
				up = true
			case 0:
				up = q.v[0]&1 == 1
			}
		}
		if up {
			q = q.add64(1)
		}
		mag = q
	}

	if mag.cmp(pow10u256[maxPrecision]) >= 0 {
		return Decimal128{}, ErrOverflow
	}
	if mag.v[2] != 0 || mag.v[3] != 0 {
		return Decimal128{}, ErrOverflow
	}
	coeff := i128FromMagnitude(u128{lo: mag.v[0], hi: mag.v[1]}, w.negative)

	finalDigits := mag.decimalDigits()
	// Precision is the SQL significant-digit count: integer digits plus scale,
	// never below the actual coefficient length and never above 38.
	integerPart := finalDigits - target
	precision := maxInt(finalDigits, 1)
	if integerPart > 1 {
		precision = integerPart + target
	}
	if precision > maxPrecision {
		return Decimal128{}, ErrOverflow
	}
	return Decimal128{coeff: coeff, precision: uint8(precision), scale: uint8(target)}, nil
}

// shr1 returns u >> 1.
func (u u256) shr1() u256 {
	var r u256
	for i := 0; i < 3; i++ {
		r.v[i] = (u.v[i] >> 1) | (u.v[i+1] << 63)
	}
	r.v[3] = u.v[3] >> 1
	return r
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func wideFromDecimal(d Decimal128) wide {
	mag, sign := d.coeff.magnitude()
	return wide{mag: u256FromU128(mag), negative: sign < 0}
}

// align scales both operands up to the larger scale without information loss.
// Each coefficient has at most 38 digits, so multiplying by 10^k with
// k <= 38 stays inside 256 bits.
func alignWide(a, b Decimal128) (wide, wide, int) {
	wa := wideFromDecimal(a)
	wb := wideFromDecimal(b)
	switch {
	case a.scale < b.scale:
		wa.mag = wa.mag.mulPow10(int(b.scale - a.scale))
	case a.scale > b.scale:
		wb.mag = wb.mag.mulPow10(int(a.scale - b.scale))
	}
	return wa, wb, maxInt(int(a.scale), int(b.scale))
}
