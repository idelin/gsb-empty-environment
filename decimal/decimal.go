// Package decimal provides exact fixed-point decimal and rational
// arithmetic for instrument (gauge) values and unit conversion factors.
//
// Values are stored as normalized big-integer fractions, so multiply and
// divide chains are evaluated exactly and never pass through floating
// point. Rounding to a fixed number of decimal places happens only when
// explicitly requested, using half-even (banker's) rounding. Zero has a
// unique representation, so negative zero can never occur.
package decimal

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrDivisionByZero is returned by Div, NewRational and NewRationalBig
// when the divisor or denominator is zero.
var ErrDivisionByZero = errors.New("decimal: division by zero")

// Value is an exact rational number num/den with den > 0 and
// gcd(|num|, den) = 1. Zero is uniquely represented as 0/1.
//
// A Value must be treated as immutable: methods never mutate their
// receivers or arguments, but the big.Ints obtained from Num and Den
// are copies and may be used freely.
type Value struct {
	num *big.Int
	den *big.Int
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// normalize reduces num/den to lowest terms with a positive denominator
// and takes ownership of both arguments.
func normalize(num, den *big.Int) Value {
	if den.Sign() == 0 {
		panic("decimal: zero denominator")
	}
	if num.Sign() == 0 {
		return Value{num: new(big.Int), den: big.NewInt(1)}
	}
	if den.Sign() < 0 {
		num.Neg(num)
		den.Neg(den)
	}
	g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(num), den)
	num.Quo(num, g)
	den.Quo(den, g)
	return Value{num: num, den: den}
}

// Zero returns the zero value.
func Zero() Value { return Value{num: new(big.Int), den: big.NewInt(1)} }

// NewFromInt returns the exact value n.
func NewFromInt(n int64) Value {
	return Value{num: big.NewInt(n), den: big.NewInt(1)}
}

// NewRational returns the exact value num/den. It reports
// ErrDivisionByZero when den == 0.
func NewRational(num, den int64) (Value, error) {
	return NewRationalBig(big.NewInt(num), big.NewInt(den))
}

// NewRationalBig returns the exact value num/den. It reports
// ErrDivisionByZero when den == 0.
func NewRationalBig(num, den *big.Int) (Value, error) {
	if den.Sign() == 0 {
		return Value{}, ErrDivisionByZero
	}
	return normalize(new(big.Int).Set(num), new(big.Int).Set(den)), nil
}

// Parse reads a decimal fixed-point literal such as "123", "-0.001" or
// "42." into an exact Value. Exponent notation is rejected: gauge values
// are fixed-point by contract. A leading "-" on a zero literal still
// yields the unique zero (no negative zero).
func Parse(s string) (Value, error) {
	orig := s
	neg := false
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	} else if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if strings.IndexByte(fracPart, '.') >= 0 {
			return Value{}, fmt.Errorf("decimal: malformed fixed-point literal %q", orig)
		}
	}
	if intPart == "" && fracPart == "" {
		return Value{}, fmt.Errorf("decimal: malformed fixed-point literal %q", orig)
	}
	for _, part := range []string{intPart, fracPart} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return Value{}, fmt.Errorf("decimal: malformed fixed-point literal %q", orig)
			}
		}
	}
	num := new(big.Int)
	if _, ok := num.SetString(intPart+fracPart, 10); !ok {
		return Value{}, fmt.Errorf("decimal: malformed fixed-point literal %q", orig)
	}
	if neg {
		num.Neg(num)
	}
	return normalize(num, pow10(len(fracPart))), nil
}

// MustParse is like Parse but panics on error. Intended for tests and
// constant declarations.
func MustParse(s string) Value {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Num returns a copy of the normalized numerator.
func (v Value) Num() *big.Int { return new(big.Int).Set(v.num) }

// Den returns a copy of the normalized denominator (always positive).
func (v Value) Den() *big.Int { return new(big.Int).Set(v.den) }

// Rat returns the value as a big.Rat.
func (v Value) Rat() *big.Rat {
	return new(big.Rat).SetFrac(new(big.Int).Set(v.num), new(big.Int).Set(v.den))
}

// Sign returns -1, 0 or +1.
func (v Value) Sign() int { return v.num.Sign() }

// IsZero reports whether v == 0.
func (v Value) IsZero() bool { return v.num.Sign() == 0 }

// Cmp compares v and w, returning -1, 0 or +1.
func (v Value) Cmp(w Value) int {
	l := new(big.Int).Mul(v.num, w.den)
	r := new(big.Int).Mul(w.num, v.den)
	return l.Cmp(r)
}

// Add returns v + w, exactly.
func (v Value) Add(w Value) Value {
	n := new(big.Int).Mul(v.num, w.den)
	n.Add(n, new(big.Int).Mul(w.num, v.den))
	return normalize(n, new(big.Int).Mul(v.den, w.den))
}

// Neg returns -v.
func (v Value) Neg() Value {
	return Value{num: new(big.Int).Neg(v.num), den: new(big.Int).Set(v.den)}
}

// Mul returns v * w, exactly.
func (v Value) Mul(w Value) Value {
	return normalize(
		new(big.Int).Mul(v.num, w.num),
		new(big.Int).Mul(v.den, w.den),
	)
}

// Div returns v / w, exactly. It reports ErrDivisionByZero when w == 0.
func (v Value) Div(w Value) (Value, error) {
	if w.num.Sign() == 0 {
		return Value{}, ErrDivisionByZero
	}
	return normalize(
		new(big.Int).Mul(v.num, w.den),
		new(big.Int).Mul(v.den, w.num),
	), nil
}

// RoundEvidence documents a single half-even rounding step so callers
// can audit exactly what was discarded and why the result moved.
type RoundEvidence struct {
	Scale     int   // decimal places requested
	Exact     Value // value before rounding
	Truncated Value // Exact truncated toward zero at Scale
	Remainder Value // Exact - Truncated (signed, magnitude < 1 ulp)
	Tie       bool  // Remainder was exactly half a ulp
	Adjusted  bool  // result moved one ulp away from zero
	Result    Value // value after rounding
}

func (e RoundEvidence) String() string {
	return fmt.Sprintf("scale=%d exact=%s truncated=%s remainder=%s tie=%t adjusted=%t result=%s",
		e.Scale, e.Exact, e.Truncated, e.Remainder, e.Tie, e.Adjusted, e.Result)
}

// Round rounds v to scale decimal places using half-even (banker's)
// rounding and returns the rounded value together with auditable
// evidence. The computation is exact: v.num * 10^scale is divided by
// v.den with truncated quotient/remainder, the remainder is compared to
// half the denominator, and ties resolve to the even quotient digit.
func (v Value) Round(scale int) (Value, RoundEvidence, error) {
	if scale < 0 {
		return Value{}, RoundEvidence{}, fmt.Errorf("decimal: negative scale %d", scale)
	}
	p := pow10(scale)
	t := new(big.Int).Mul(v.num, p)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(t, v.den, r) // truncated toward zero; r has the sign of t

	truncated := normalize(new(big.Int).Set(q), new(big.Int).Set(p))
	remainder := normalize(new(big.Int).Set(r), new(big.Int).Mul(v.den, p))

	twice := new(big.Int).Abs(r)
	twice.Lsh(twice, 1)
	cmp := twice.Cmp(v.den)
	tie := cmp == 0
	adjusted := false
	if cmp > 0 || (tie && new(big.Int).Abs(q).Bit(0) == 1) {
		if t.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
		adjusted = true
	}
	result := normalize(q, p)
	return result, RoundEvidence{
		Scale:     scale,
		Exact:     v,
		Truncated: truncated,
		Remainder: remainder,
		Tie:       tie,
		Adjusted:  adjusted,
		Result:    result,
	}, nil
}

// finiteScale returns the number of decimal places needed to represent
// 1/den exactly, or false when the decimal expansion is infinite.
func finiteScale(den *big.Int) (int, bool) {
	rem := new(big.Int).Set(den)
	c2, c5 := 0, 0
	two, five := big.NewInt(2), big.NewInt(5)
	for new(big.Int).Mod(rem, two).Sign() == 0 {
		rem.Quo(rem, two)
		c2++
	}
	for new(big.Int).Mod(rem, five).Sign() == 0 {
		rem.Quo(rem, five)
		c5++
	}
	if rem.Cmp(big.NewInt(1)) != 0 {
		return 0, false
	}
	if c2 > c5 {
		return c2, true
	}
	return c5, true
}

// String renders v as a finite decimal when possible ("-12.5"),
// otherwise as a fraction ("1/3"). Zero always renders as "0".
func (v Value) String() string {
	if scale, ok := finiteScale(v.den); ok {
		s, err := v.FixedString(scale)
		if err == nil {
			return s
		}
	}
	return v.num.String() + "/" + v.den.String()
}

// FixedString renders v with exactly scale decimal places. It fails
// when v cannot be represented exactly at that scale; use Round first
// when an approximation is acceptable.
func (v Value) FixedString(scale int) (string, error) {
	if scale < 0 {
		return "", fmt.Errorf("decimal: negative scale %d", scale)
	}
	t := new(big.Int).Mul(v.num, pow10(scale))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(t, v.den, r)
	if r.Sign() != 0 {
		return "", fmt.Errorf("decimal: %s not representable at scale %d", v.String(), scale)
	}
	neg := q.Sign() < 0
	digits := new(big.Int).Abs(q).String()
	for len(digits) < scale+1 {
		digits = "0" + digits
	}
	if scale > 0 {
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if neg {
		return "-" + digits, nil
	}
	return digits, nil
}
