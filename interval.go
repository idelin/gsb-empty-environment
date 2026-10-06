// Package interval implements exact rational interval arithmetic with
// outward-rounded decimal output for error analysis.
//
// Intervals are stored exactly as pairs of *big.Rat endpoints, so all
// arithmetic is exact. Rounding happens only at output time, where the
// lower endpoint is rounded down and the upper endpoint is rounded up to
// the requested number of decimal places, guaranteeing the printed
// interval contains the true value.
package interval

import (
	"fmt"
	"math/big"
	"strings"
)

// Interval is a closed rational interval [Lo, Hi].
// If Unbounded is true, the interval represents (-inf, +inf) and
// Lo/Hi are meaningless; this arises from division by an interval
// containing zero.
type Interval struct {
	Lo, Hi    *big.Rat
	Unbounded bool
}

// New returns the interval [lo, hi]. It panics if lo > hi.
func New(lo, hi *big.Rat) Interval {
	if lo.Cmp(hi) > 0 {
		panic("interval: lo > hi")
	}
	return Interval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}
}

// FromRat returns the degenerate interval [x, x].
func FromRat(x *big.Rat) Interval { return New(x, x) }

// FromInt64 returns the degenerate interval [x, x].
func FromInt64(x int64) Interval { return FromRat(big.NewRat(x, 1)) }

// FromFrac returns the degenerate interval [num/den, num/den].
func FromFrac(num, den int64) Interval { return FromRat(big.NewRat(num, den)) }

// Parse parses a decimal or fraction string (big.Rat syntax) into a
// degenerate interval.
func Parse(s string) (Interval, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return Interval{}, fmt.Errorf("interval: cannot parse %q", s)
	}
	return FromRat(r), nil
}

var unbounded = Interval{Unbounded: true}

// UnboundedInterval returns the interval (-inf, +inf).
func UnboundedInterval() Interval { return unbounded }

// ContainsZero reports whether 0 lies in [Lo, Hi].
func (i Interval) ContainsZero() bool {
	if i.Unbounded {
		return true
	}
	return i.Lo.Sign() <= 0 && i.Hi.Sign() >= 0
}

// Contains reports whether x lies in the interval.
func (i Interval) Contains(x *big.Rat) bool {
	if i.Unbounded {
		return true
	}
	return i.Lo.Cmp(x) <= 0 && i.Hi.Cmp(x) >= 0
}

// Add returns a + b.
func Add(a, b Interval) Interval {
	if a.Unbounded || b.Unbounded {
		return unbounded
	}
	return Interval{
		Lo: new(big.Rat).Add(a.Lo, b.Lo),
		Hi: new(big.Rat).Add(a.Hi, b.Hi),
	}
}

// Neg returns -a.
func Neg(a Interval) Interval {
	if a.Unbounded {
		return unbounded
	}
	return Interval{Lo: new(big.Rat).Neg(a.Hi), Hi: new(big.Rat).Neg(a.Lo)}
}

// Sub returns a - b.
func Sub(a, b Interval) Interval { return Add(a, Neg(b)) }

// Mul returns a * b using the exact four-endpoint product model.
func Mul(a, b Interval) Interval {
	if a.Unbounded || b.Unbounded {
		return unbounded
	}
	prods := [4]*big.Rat{
		new(big.Rat).Mul(a.Lo, b.Lo),
		new(big.Rat).Mul(a.Lo, b.Hi),
		new(big.Rat).Mul(a.Hi, b.Lo),
		new(big.Rat).Mul(a.Hi, b.Hi),
	}
	lo, hi := prods[0], prods[0]
	for _, p := range prods[1:] {
		if p.Cmp(lo) < 0 {
			lo = p
		}
		if p.Cmp(hi) > 0 {
			hi = p
		}
	}
	return Interval{Lo: lo, Hi: hi}
}

// Div returns a / b. If b contains zero anywhere (including its
// interior, not only zero endpoints), the result is unbounded.
func Div(a, b Interval) Interval {
	if a.Unbounded || b.Unbounded {
		return unbounded
	}
	if b.ContainsZero() {
		return unbounded
	}
	quots := [4]*big.Rat{
		new(big.Rat).Quo(a.Lo, b.Lo),
		new(big.Rat).Quo(a.Lo, b.Hi),
		new(big.Rat).Quo(a.Hi, b.Lo),
		new(big.Rat).Quo(a.Hi, b.Hi),
	}
	lo, hi := quots[0], quots[0]
	for _, q := range quots[1:] {
		if q.Cmp(lo) < 0 {
			lo = q
		}
		if q.Cmp(hi) > 0 {
			hi = q
		}
	}
	return Interval{Lo: lo, Hi: hi}
}

// pow10 returns 10^n as a *big.Int.
func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// floorDiv returns floor(a/b) for b > 0.
func floorDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 && a.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// ceilDiv returns ceil(a/b) for b > 0.
func ceilDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 && a.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// formatScaled renders s * 10^(-prec) as a decimal string.
func formatScaled(s *big.Int, prec int) string {
	neg := s.Sign() < 0
	digits := new(big.Int).Abs(s).String()
	if prec == 0 {
		if neg {
			return "-" + digits
		}
		return digits
	}
	if len(digits) <= prec {
		digits = strings.Repeat("0", prec-len(digits)+1) + digits
	}
	intPart := digits[:len(digits)-prec]
	fracPart := digits[len(digits)-prec:]
	if neg {
		return "-" + intPart + "." + fracPart
	}
	return intPart + "." + fracPart
}

// Decimal returns decimal bounds with prec fractional digits such that
// the true interval is contained in [lo, hi]: the lower bound is rounded
// down (toward -inf) and the upper bound is rounded up (toward +inf).
func (i Interval) Decimal(prec int) (lo, hi string) {
	if i.Unbounded {
		return "-inf", "+inf"
	}
	scale := pow10(prec)
	loScaled := floorDiv(new(big.Int).Mul(i.Lo.Num(), scale), i.Lo.Denom())
	hiScaled := ceilDiv(new(big.Int).Mul(i.Hi.Num(), scale), i.Hi.Denom())
	return formatScaled(loScaled, prec), formatScaled(hiScaled, prec)
}

// String formats the interval with prec decimal digits per endpoint,
// rounded outward.
func (i Interval) String(prec int) string {
	lo, hi := i.Decimal(prec)
	return "[" + lo + ", " + hi + "]"
}
