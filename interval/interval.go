// Package interval implements rigorous interval arithmetic with exact
// rational endpoints. Every operation returns a closed interval whose
// closure is guaranteed to contain the true mathematical result.
package interval

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrIndeterminateDomain is returned when an operation's domain cannot be
// determined for the given interval, e.g. division by an interval
// containing zero, reciprocal of an interval containing zero, or square
// root of an interval containing negative values.
var ErrIndeterminateDomain = errors.New("interval: indeterminate domain")

// Interval is a closed interval [Lo, Hi] with exact rational endpoints.
type Interval struct {
	Lo *big.Rat
	Hi *big.Rat
}

// Rat is a convenience helper to build an exact rational endpoint.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

// New constructs [lo, hi]; it fails when lo > hi.
func New(lo, hi *big.Rat) (Interval, error) {
	if lo.Cmp(hi) > 0 {
		return Interval{}, fmt.Errorf("interval: lower bound %s exceeds upper bound %s", lo, hi)
	}
	return Interval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}, nil
}

// Must is New but panics on an invalid interval.
func Must(lo, hi *big.Rat) Interval {
	iv, err := New(lo, hi)
	if err != nil {
		panic(err)
	}
	return iv
}

func (i Interval) String() string {
	return "[" + i.Lo.RatString() + ", " + i.Hi.RatString() + "]"
}

// ContainsZero reports whether 0 lies in the closed interval.
func (i Interval) ContainsZero() bool {
	return i.Lo.Sign() <= 0 && i.Hi.Sign() >= 0
}

// Contains reports whether x lies in the closed interval.
func (i Interval) Contains(x *big.Rat) bool {
	return i.Lo.Cmp(x) <= 0 && i.Hi.Cmp(x) >= 0
}

// Add returns [a.Lo+b.Lo, a.Hi+b.Hi].
func (a Interval) Add(b Interval) Interval {
	return Interval{
		Lo: new(big.Rat).Add(a.Lo, b.Lo),
		Hi: new(big.Rat).Add(a.Hi, b.Hi),
	}
}

// Sub returns [a.Lo-b.Hi, a.Hi-b.Lo].
func (a Interval) Sub(b Interval) Interval {
	return Interval{
		Lo: new(big.Rat).Sub(a.Lo, b.Hi),
		Hi: new(big.Rat).Sub(a.Hi, b.Lo),
	}
}

// Neg returns [-Hi, -Lo].
func (a Interval) Neg() Interval {
	return Interval{
		Lo: new(big.Rat).Neg(a.Hi),
		Hi: new(big.Rat).Neg(a.Lo),
	}
}

// Mul returns the exact product interval, the hull of the four
// endpoint products.
func (a Interval) Mul(b Interval) Interval {
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

// Reciprocal returns [1/Hi, 1/Lo]. It fails with
// ErrIndeterminateDomain when the interval contains zero.
func (a Interval) Reciprocal() (Interval, error) {
	if a.ContainsZero() {
		return Interval{}, ErrIndeterminateDomain
	}
	return Interval{
		Lo: new(big.Rat).Inv(a.Hi),
		Hi: new(big.Rat).Inv(a.Lo),
	}, nil
}

// Div returns a * (1/b). It fails with ErrIndeterminateDomain when the
// divisor interval contains zero.
func (a Interval) Div(b Interval) (Interval, error) {
	rec, err := b.Reciprocal()
	if err != nil {
		return Interval{}, err
	}
	return a.Mul(rec), nil
}

// Sqrt returns an outward-rounded rational enclosure of
// [sqrt(Lo), sqrt(Hi)]. It fails with ErrIndeterminateDomain when the
// interval contains negative values. Because square roots of rationals
// are generally irrational, endpoints are rounded outward: Lo down and
// Hi up, so the true result always lies within the closed result.
func (a Interval) Sqrt() (Interval, error) {
	if a.Lo.Sign() < 0 {
		return Interval{}, ErrIndeterminateDomain
	}
	lo, _ := sqrtRatFloor(a.Lo)
	_, hi := sqrtRatFloor(a.Hi)
	return Interval{Lo: lo, Hi: hi}, nil
}

// sqrtRatFloor returns floor and ceil rational bounds for sqrt(x),
// x >= 0. For x = p/q, sqrt(x) = sqrt(p*q)/q; integer square root of
// n = p*q gives s <= sqrt(n) <= s+1, hence s/q <= sqrt(x) <= (s+1)/q,
// with equality on the right only when n is a perfect square.
func sqrtRatFloor(x *big.Rat) (floor, ceil *big.Rat) {
	if x.Sign() == 0 {
		zero := new(big.Rat)
		return zero, new(big.Rat)
	}
	n := new(big.Int).Mul(x.Num(), x.Denom())
	s := new(big.Int).Sqrt(n) // floor(sqrt(n))
	q := x.Denom()
	floor = new(big.Rat).SetFrac(s, q)
	ceil = new(big.Rat).Set(floor)
	if new(big.Int).Mul(s, s).Cmp(n) != 0 {
		ceil = new(big.Rat).SetFrac(new(big.Int).Add(s, big.NewInt(1)), q)
	}
	return floor, ceil
}

// Equal reports exact structural equality of both endpoints.
func (a Interval) Equal(b Interval) bool {
	return a.Lo.Cmp(b.Lo) == 0 && a.Hi.Cmp(b.Hi) == 0
}

// Width returns Hi - Lo.
func (a Interval) Width() *big.Rat {
	return new(big.Rat).Sub(a.Hi, a.Lo)
}
