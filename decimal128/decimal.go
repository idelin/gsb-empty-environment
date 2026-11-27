// Package decimal128 implements a fixed-point SQL DECIMAL kernel backed by a
// custom signed 128-bit two's-complement coefficient (high 64 bits signed,
// low 64 bits unsigned) together with (precision, scale) metadata.
//
// The representable value range is [-10^38+1, 10^38-1]. All fixed-width
// integer arithmetic (128-bit and the internal 256-bit wide products) is
// implemented in this package on uint64 limbs; math/big is never used by
// non-test code.
package decimal128

import "errors"

// RoundingMode selects how a discarded half-digit is handled when a value
// must be rescaled to a smaller scale.
type RoundingMode int

const (
	// HalfAwayFromZero rounds half-way cases away from zero ("四舍五入").
	HalfAwayFromZero RoundingMode = iota
	// HalfToEven rounds half-way cases to the nearest even coefficient
	// (banker's rounding).
	HalfToEven
)

var (
	// ErrOverflow means the coefficient would leave the
	// [-10^38+1, 10^38-1] range.
	ErrOverflow = errors.New("decimal128: numeric value out of range (overflow)")
	// ErrDivisionByZero is returned by Div and Mod with a zero divisor.
	ErrDivisionByZero = errors.New("decimal128: division by zero")
	// ErrInvalidSyntax is returned by Parse for malformed input.
	ErrInvalidSyntax = errors.New("decimal128: invalid decimal syntax")
	// ErrInvalidPrecision is returned for precision/scale pairs violating
	// 1 <= precision <= 38 and 0 <= scale <= precision.
	ErrInvalidPrecision = errors.New("decimal128: invalid precision or scale")
)

// Decimal128 is a fixed-point decimal: value == coefficient * 10^-scale.
type Decimal128 struct {
	coeff i128
	scale int
	prec  int
}

// Context carries the rounding mode used by an operation sequence. The zero
// value is HalfAwayFromZero, matching plain SQL ROUND semantics.
type Context struct {
	Mode RoundingMode
}

// DefaultContext rounds half-way values away from zero.
var DefaultContext = Context{Mode: HalfAwayFromZero}

// New constructs a DECIMAL(precision, scale) from a signed coefficient.
// The coefficient must satisfy |coeff| < 10^precision.
func New(hi int64, lo uint64, precision, scale int) (Decimal128, error) {
	if precision < 1 || precision > MaxPrecision || scale < 0 || scale > precision {
		return Decimal128{}, ErrInvalidPrecision
	}
	v := i128{hi: hi, lo: lo}
	if v.mag().cmp(limitForPrecision(precision)) >= 0 {
		return Decimal128{}, ErrOverflow
	}
	if v.isZero() {
		v = i128{}
	}
	return Decimal128{coeff: v, scale: scale, prec: precision}, nil
}

// MustNew panics when New returns an error; it is intended for constants.
func MustNew(hi int64, lo uint64, precision, scale int) Decimal128 {
	d, err := New(hi, lo, precision, scale)
	if err != nil {
		panic(err)
	}
	return d
}

// Coeff returns the signed coefficient as (high, low) limbs.
func (d Decimal128) Coeff() (int64, uint64) {
	return d.coeff.hi, d.coeff.lo
}

// Scale returns the decimal scale (digits after the decimal point).
func (d Decimal128) Scale() int { return d.scale }

// Precision returns the declared number of significant digits.
func (d Decimal128) Precision() int { return d.prec }

// IsZero reports whether the value is zero.
func (d Decimal128) IsZero() bool { return d.coeff.isZero() }

// Sign returns -1, 0 or +1.
func (d Decimal128) Sign() int {
	if d.coeff.isZero() {
		return 0
	}
	if d.coeff.hi < 0 {
		return -1
	}
	return 1
}

// Neg returns the additive inverse.
func (d Decimal128) Neg() Decimal128 {
	return Decimal128{coeff: d.coeff.neg(), scale: d.scale, prec: d.prec}
}

// Abs returns the absolute value.
func (d Decimal128) Abs() Decimal128 {
	if d.Sign() < 0 {
		return d.Neg()
	}
	return d
}

// finish validates the magnitude against precision and normalizes -0.
func finish(v i128, precision, scale int) (Decimal128, error) {
	if v.isZero() {
		return Decimal128{scale: scale, prec: precision}, nil
	}
	if v.mag().cmp(limitForPrecision(precision)) >= 0 {
		return Decimal128{}, ErrOverflow
	}
	return Decimal128{coeff: v, scale: scale, prec: precision}, nil
}

// roundMag divides an unsigned magnitude by 10^k (k > 0), applying the
// context's half-way rule exactly. The remainder comparison is performed on
// the full 256-bit magnitude so half points are never misjudged.
func (c Context) roundMag(m u256, k int) (u128, error) {
	div := pow10u256[k]
	q256, rem := m.div(div)
	if !q256.fits128() {
		return u128{}, ErrOverflow
	}
	q := q256.as128()
	if rem.isZero() {
		return q, nil
	}
	twice := rem.shlOne()
	cmp := twice.cmp(div)
	if cmp > 0 {
		q, _ = q.add(u128{lo: 1}, 0)
	} else if cmp == 0 {
		switch c.Mode {
		case HalfAwayFromZero:
			q, _ = q.add(u128{lo: 1}, 0)
		case HalfToEven:
			if q.lo&1 == 1 {
				q, _ = q.add(u128{lo: 1}, 0)
			}
		}
	}
	if q.cmp(limitForPrecision(MaxPrecision)) >= 0 {
		return u128{}, ErrOverflow
	}
	return q, nil
}

// rescaleTo moves v to the requested (smaller) scale, rounding as needed,
// and enforces the 38-digit coefficient bound.
func (c Context) rescaleTo(v i128, fromScale, toScale int) (i128, error) {
	if toScale > fromScale {
		// Lossless scale-up: multiply the coefficient by 10^(toScale-fromScale).
		k := toScale - fromScale
		scaled := v.mag().mul(pow10u128[k])
		if scaled.cmp(limitForPrecision(MaxPrecision)) >= 0 {
			return i128{}, ErrOverflow
		}
		return fromMag(scaled, v.hi < 0), nil
	}
	if toScale == fromScale {
		return v, nil
	}
	qm, err := c.roundMag(v.mag().to256(), fromScale-toScale)
	if err != nil {
		return i128{}, err
	}
	return fromMag(qm, v.hi < 0), nil
}

// precisionFor computes the declared precision of a coefficient at scale s:
// at least one digit, at least scale digits, otherwise the actual digit count.
func precisionFor(v i128, s int) int {
	digits := v.mag().digits()
	if digits < 1 {
		digits = 1
	}
	if digits < s {
		digits = s
	}
	if digits > MaxPrecision {
		digits = MaxPrecision
	}
	return digits
}
