// Package interval implements rigorous interval arithmetic over exact
// rational endpoints. Every operation returns a closed interval whose
// closure is guaranteed to contain the true mathematical result set.
package interval

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrZeroDivisor is returned when a divisor interval contains zero; the
// result is then unlocalizable (unbounded / disconnected).
var ErrZeroDivisor = errors.New("interval: divisor contains zero, result unlocalizable")

// ErrNegativeRadicand is returned when a square-root operand interval
// extends below zero.
var ErrNegativeRadicand = errors.New("interval: square root of negative lower bound")

// Interval is a closed interval [Lo, Hi] with exact rational endpoints.
type Interval struct {
	Lo *big.Rat
	Hi *big.Rat
}

// New builds the interval [lo, hi]. It panics if lo > hi.
func New(lo, hi *big.Rat) Interval {
	if lo.Cmp(hi) > 0 {
		panic(fmt.Sprintf("interval: empty interval [%s, %s]", lo, hi))
	}
	return Interval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}
}

// Of builds a degenerate interval from a single rational.
func Of(x *big.Rat) Interval { return New(x, x) }

// OfInt64 builds the interval [lo, hi] from integers.
func OfInt64(lo, hi int64) Interval {
	return New(big.NewRat(lo, 1), big.NewRat(hi, 1))
}

// Contains reports whether the exact rational x lies in i.
func (i Interval) Contains(x *big.Rat) bool {
	return i.Lo.Cmp(x) <= 0 && x.Cmp(i.Hi) <= 0
}

// ContainsZero reports whether 0 lies in i.
func (i Interval) ContainsZero() bool {
	return i.Lo.Sign() <= 0 && i.Hi.Sign() >= 0
}

func (i Interval) String() string {
	return "[" + ratString(i.Lo) + ", " + ratString(i.Hi) + "]"
}

func ratString(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return r.RatString()
}

// Equal reports exact structural equality of endpoints.
func (a Interval) Equal(b Interval) bool {
	return a.Lo.Cmp(b.Lo) == 0 && a.Hi.Cmp(b.Hi) == 0
}

// Width returns Hi - Lo.
func (a Interval) Width() *big.Rat {
	return new(big.Rat).Sub(a.Hi, a.Lo)
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

// Neg returns [-a.Hi, -a.Lo].
func (a Interval) Neg() Interval {
	return Interval{
		Lo: new(big.Rat).Neg(a.Hi),
		Hi: new(big.Rat).Neg(a.Lo),
	}
}

// Mul returns the exact product enclosure: the min/max of the four
// endpoint products. Endpoints are exact rationals, so no outward
// rounding is needed.
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

// Recip returns [1/a.Hi, 1/a.Lo]. It fails with ErrZeroDivisor when a
// contains zero, since the reciprocal is then unlocalizable.
func (a Interval) Recip() (Interval, error) {
	if a.ContainsZero() {
		return Interval{}, ErrZeroDivisor
	}
	return Interval{
		Lo: new(big.Rat).Inv(a.Hi),
		Hi: new(big.Rat).Inv(a.Lo),
	}, nil
}

// Div returns a * (1/b). It fails with ErrZeroDivisor when b contains
// zero.
func (a Interval) Div(b Interval) (Interval, error) {
	r, err := b.Recip()
	if err != nil {
		return Interval{}, err
	}
	return a.Mul(r), nil
}

// Sqrt returns an outward enclosure of {sqrt(x) : x in a}. Because the
// square root of a rational is generally irrational, the endpoints are
// rationals bracketing the true roots: Lo is rounded down, Hi is rounded
// up, so the closure strictly contains the exact result set.
func (a Interval) Sqrt() (Interval, error) {
	if a.Lo.Sign() < 0 {
		return Interval{}, ErrNegativeRadicand
	}
	return Interval{Lo: sqrtRatFloor(a.Lo), Hi: sqrtRatCeil(a.Hi)}, nil
}

// sqrtRatFloor returns the greatest rational with denominator q (where
// x = p/q in lowest terms) not exceeding sqrt(x):
// floor(sqrt(x)) = isqrt(p*q)/q.
func sqrtRatFloor(x *big.Rat) *big.Rat {
	n := new(big.Int).Mul(x.Num(), x.Denom())
	s := isqrt(n)
	return new(big.Rat).SetFrac(s, x.Denom())
}

// sqrtRatCeil returns the least rational with denominator q not below
// sqrt(x): floor(sqrt(x)) if x is a perfect rational square, else
// floor(sqrt(x)) + 1/q.
func sqrtRatCeil(x *big.Rat) *big.Rat {
	n := new(big.Int).Mul(x.Num(), x.Denom())
	s := isqrt(n)
	if new(big.Int).Mul(s, s).Cmp(n) != 0 {
		s.Add(s, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(s, x.Denom())
}

// isqrt returns floor(sqrt(n)) for n >= 0 via Newton's method.
func isqrt(n *big.Int) *big.Int {
	if n.Sign() == 0 {
		return new(big.Int)
	}
	// Initial guess 2^(ceil(bitlen/2)+1) is strictly above sqrt(n).
	x := new(big.Int).Lsh(big.NewInt(1), uint(n.BitLen()/2)+1)
	y := new(big.Int)
	for {
		y.Quo(n, x).Add(y, x).Rsh(y, 1)
		if y.Cmp(x) >= 0 {
			return x
		}
		x, y = y, x
	}
}
