// Package decimal provides exact decimal arithmetic on values of the form
// coeff * 10^exp, with configurable rounding for operations that cannot be
// represented exactly within a given precision budget.
//
// Decimal values are immutable: every operation allocates fresh big.Int
// state and never mutates its operands, so a single value (or expression
// node) may be shared by any number of consumers without risk of one
// computation corrupting another through big.Int reuse.
package decimal

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ErrDivByZero is returned when a division has a zero divisor. Division by
// zero is reported as its own error and never produces a rounded value.
var ErrDivByZero = errors.New("decimal: division by zero")

// RoundingMode selects how a discarded fractional remainder is handled.
type RoundingMode int

const (
	// ToZero rounds toward zero (truncation).
	ToZero RoundingMode = iota
	// ToPositiveInf rounds toward positive infinity (ceiling).
	ToPositiveInf
	// ToNegativeInf rounds toward negative infinity (floor).
	ToNegativeInf
	// TiesToEven rounds to the nearest neighbour, breaking exact halves
	// toward the even neighbour.
	TiesToEven
)

// Decimal is an exact decimal number: value = coeff * 10^exp.
//
// The zero value is not usable; construct values with New, NewInt or Parse.
// All methods are pure: they never modify the receiver or the arguments.
type Decimal struct {
	coeff *big.Int
	exp   int64
}

// Result is the outcome of an operation that may have discarded precision.
// Exact reports whether Value is the exact mathematical result. When the
// precision budget is insufficient, Exact is false and Value is rounded
// according to the requested mode; it is never padded with zeros to
// masquerade as an exact value.
type Result struct {
	Value *Decimal
	Exact bool
}

// mkDecimal takes ownership of coeff and returns the canonical form
// (zero always carries exponent 0).
func mkDecimal(coeff *big.Int, exp int64) *Decimal {
	if coeff.Sign() == 0 {
		exp = 0
	}
	return &Decimal{coeff: coeff, exp: exp}
}

// New returns coeff * 10^exp. The coefficient is copied, so later mutation
// of the caller's big.Int cannot affect the Decimal.
func New(coeff *big.Int, exp int64) *Decimal {
	return mkDecimal(new(big.Int).Set(coeff), exp)
}

// NewInt returns the exact decimal for v.
func NewInt(v int64) *Decimal { return mkDecimal(big.NewInt(v), 0) }

// Copy returns an independent deep copy of d.
func (d *Decimal) Copy() *Decimal {
	return mkDecimal(new(big.Int).Set(d.coeff), d.exp)
}

// Coeff returns a copy of the coefficient c with d = c * 10^d.Exp().
func (d *Decimal) Coeff() *big.Int { return new(big.Int).Set(d.coeff) }

// Exp returns the decimal exponent e with d = d.Coeff() * 10^e.
func (d *Decimal) Exp() int64 { return d.exp }

// Sign returns -1, 0 or +1.
func (d *Decimal) Sign() int { return d.coeff.Sign() }

// pow10 returns a fresh 10^n; n must be non-negative.
func pow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// Neg returns -d.
func (d *Decimal) Neg() *Decimal {
	return mkDecimal(new(big.Int).Neg(d.coeff), d.exp)
}

// Add returns the exact sum d + e.
func (d *Decimal) Add(e *Decimal) *Decimal {
	if d.exp > e.exp {
		return e.Add(d)
	}
	// d.exp <= e.exp: scale e's coefficient down to d's exponent.
	sum := new(big.Int).Mul(e.coeff, pow10(e.exp-d.exp))
	sum.Add(sum, d.coeff)
	return mkDecimal(sum, d.exp)
}

// Sub returns the exact difference d - e.
func (d *Decimal) Sub(e *Decimal) *Decimal { return d.Add(e.Neg()) }

// Mul returns the exact product d * e.
func (d *Decimal) Mul(e *Decimal) *Decimal {
	return mkDecimal(new(big.Int).Mul(d.coeff, e.coeff), d.exp+e.exp)
}

// Quo returns d / e rounded to scale fractional decimal digits using mode.
// The result reports whether the quotient was exact within the budget; an
// inexact quotient is rounded, never zero-padded into a fake exact value.
// A zero divisor yields ErrDivByZero.
func (d *Decimal) Quo(e *Decimal, scale uint, mode RoundingMode) (*Result, error) {
	if e.coeff.Sign() == 0 {
		return nil, ErrDivByZero
	}
	s := int64(scale)
	if d.coeff.Sign() == 0 {
		return &Result{Value: mkDecimal(new(big.Int), 0), Exact: true}, nil
	}
	num := new(big.Int).Set(d.coeff)
	den := new(big.Int).Set(e.coeff)
	// quotient * 10^s = num/den * 10^(d.exp - e.exp + s)
	if k := d.exp - e.exp + s; k >= 0 {
		num.Mul(num, pow10(k))
	} else {
		den.Mul(den, pow10(-k))
	}
	if den.Sign() < 0 { // keep the denominator positive for rounding
		num.Neg(num)
		den.Neg(den)
	}
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	applyRounding(q, r, den, mode)
	return &Result{Value: mkDecimal(q, -s), Exact: r.Sign() == 0}, nil
}

// Round returns d rounded to scale fractional decimal digits using mode.
// Exact is true when no precision was discarded.
func (d *Decimal) Round(scale uint, mode RoundingMode) *Result {
	target := -int64(scale)
	if d.exp >= target {
		return &Result{Value: d.Copy(), Exact: true}
	}
	m := pow10(target - d.exp)
	q, r := new(big.Int).QuoRem(d.coeff, m, new(big.Int))
	applyRounding(q, r, m, mode)
	return &Result{Value: mkDecimal(q, target), Exact: r.Sign() == 0}
}

// applyRounding adjusts the truncated quotient q in place given the
// remainder r of num/den with den > 0. r carries the sign of the value.
func applyRounding(q, r, den *big.Int, mode RoundingMode) {
	if r.Sign() == 0 {
		return
	}
	switch mode {
	case ToZero:
		// QuoRem already truncated toward zero.
	case ToPositiveInf:
		if r.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
	case ToNegativeInf:
		if r.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		}
	case TiesToEven:
		twice := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
		cmp := twice.Cmp(den)
		if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
			if r.Sign() > 0 {
				q.Add(q, big.NewInt(1))
			} else {
				q.Sub(q, big.NewInt(1))
			}
		}
	}
}

// Cmp compares d and e, returning -1, 0 or +1.
func (d *Decimal) Cmp(e *Decimal) int {
	if d.coeff.Sign() != e.coeff.Sign() {
		if d.coeff.Sign() < e.coeff.Sign() {
			return -1
		}
		return 1
	}
	if d.coeff.Sign() == 0 {
		return 0
	}
	a, b := d.coeff, e.coeff
	switch {
	case d.exp < e.exp:
		b = new(big.Int).Mul(b, pow10(e.exp-d.exp))
	case e.exp < d.exp:
		a = new(big.Int).Mul(a, pow10(d.exp-e.exp))
	}
	return a.Cmp(b)
}

// Equal reports whether d and e represent the same number.
func (d *Decimal) Equal(e *Decimal) bool { return d.Cmp(e) == 0 }

// Rat returns the exact value of d as a big.Rat.
func (d *Decimal) Rat() *big.Rat {
	r := new(big.Rat).SetInt(d.coeff)
	if d.exp >= 0 {
		return r.Mul(r, new(big.Rat).SetInt(pow10(d.exp)))
	}
	return r.Quo(r, new(big.Rat).SetInt(pow10(-d.exp)))
}

// Parse converts a decimal literal such as "123", "-0.05", "1.5e10" or
// "2E-7" into an exact Decimal.
func Parse(s string) (*Decimal, error) {
	orig := s
	neg := false
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	} else if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	mant := s
	var exp int64
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant = s[:i]
		e, err := strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decimal: invalid exponent in %q", orig)
		}
		exp = e
	}
	intPart, fracPart := mant, ""
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		intPart, fracPart = mant[:i], mant[i+1:]
	}
	digits := intPart + fracPart
	if digits == "" {
		return nil, fmt.Errorf("decimal: no digits in %q", orig)
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < 48 || digits[i] > 57 {
			return nil, fmt.Errorf("decimal: invalid digit in %q", orig)
		}
	}
	coeff, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("decimal: invalid digits in %q", orig)
	}
	if neg {
		coeff.Neg(coeff)
	}
	return mkDecimal(coeff, exp-int64(len(fracPart))), nil
}

// String returns d in plain decimal notation, switching to scientific
// notation when the plain form would be unreasonably long.
func (d *Decimal) String() string {
	if d.coeff.Sign() == 0 {
		return "0"
	}
	sign := ""
	if d.coeff.Sign() < 0 {
		sign = "-"
	}
	digits := new(big.Int).Abs(d.coeff).String()
	point := int64(len(digits)) + d.exp // digits with the point inserted at this offset
	switch {
	case point > int64(len(digits))+64 || point < -64:
		mant := digits[:1]
		if len(digits) > 1 {
			mant += "." + digits[1:]
		}
		return fmt.Sprintf("%s%se%d", sign, mant, point-1)
	case point <= 0:
		return sign + "0." + strings.Repeat("0", int(-point)) + digits
	case point >= int64(len(digits)):
		return sign + digits + strings.Repeat("0", int(point)-len(digits))
	default:
		return sign + digits[:point] + "." + digits[point:]
	}
}
