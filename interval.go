package certitrig

import (
	"math/big"
	"strconv"
)

// Interval is a closed rational interval [Lo, Hi].
// All operations are conservative: the result always encloses the
// exact mathematical result applied to any points in the operands.
type Interval struct {
	Lo *big.Rat
	Hi *big.Rat
}

// IV builds an interval from two rationals (copied).
func IV(lo, hi *big.Rat) Interval {
	return Interval{new(big.Rat).Set(lo), new(big.Rat).Set(hi)}
}

// IVPoint builds a degenerate point interval.
func IVPoint(x *big.Rat) Interval { return IV(x, x) }

// IVSymmetric builds [-b, b].
func IVSymmetric(b *big.Rat) Interval {
	return Interval{new(big.Rat).Neg(b), new(big.Rat).Set(b)}
}

func ratAbs(x *big.Rat) *big.Rat { return new(big.Rat).Abs(x) }

func ratMax(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// Add returns a + b.
func (a Interval) Add(b Interval) Interval {
	return Interval{new(big.Rat).Add(a.Lo, b.Lo), new(big.Rat).Add(a.Hi, b.Hi)}
}

// Sub returns a - b.
func (a Interval) Sub(b Interval) Interval {
	return Interval{new(big.Rat).Sub(a.Lo, b.Hi), new(big.Rat).Sub(a.Hi, b.Lo)}
}

// Neg returns -a.
func (a Interval) Neg() Interval {
	return Interval{new(big.Rat).Neg(a.Hi), new(big.Rat).Neg(a.Lo)}
}

// Mul returns a * b.
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
	return Interval{new(big.Rat).Set(lo), new(big.Rat).Set(hi)}
}

// Scale multiplies by an exact rational scalar.
func (a Interval) Scale(s *big.Rat) Interval {
	if s.Sign() >= 0 {
		return Interval{new(big.Rat).Mul(a.Lo, s), new(big.Rat).Mul(a.Hi, s)}
	}
	return Interval{new(big.Rat).Mul(a.Hi, s), new(big.Rat).Mul(a.Lo, s)}
}

// Square returns a^2 (tighter than a*a when a straddles 0).
func (a Interval) Square() Interval {
	if a.Lo.Sign() <= 0 && a.Hi.Sign() >= 0 {
		m := ratMax(ratAbs(a.Lo), ratAbs(a.Hi))
		return Interval{new(big.Rat), new(big.Rat).Mul(m, m)}
	}
	lo2 := new(big.Rat).Mul(a.Lo, a.Lo)
	hi2 := new(big.Rat).Mul(a.Hi, a.Hi)
	if lo2.Cmp(hi2) <= 0 {
		return Interval{lo2, hi2}
	}
	return Interval{hi2, lo2}
}

// Intersect returns the intersection of two intervals that are both
// guaranteed to contain the same true value; the result is therefore
// also a valid enclosure of that value.
func (a Interval) Intersect(b Interval) Interval {
	return Interval{ratMax(a.Lo, b.Lo), ratMin(a.Hi, b.Hi)}
}

func ratMin(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// Width returns Hi - Lo.
func (a Interval) Width() *big.Rat { return new(big.Rat).Sub(a.Hi, a.Lo) }

// Mid returns the rational midpoint.
func (a Interval) Mid() *big.Rat {
	m := new(big.Rat).Add(a.Lo, a.Hi)
	return m.Quo(m, big.NewRat(2, 1))
}

// MaxAbs returns max(|Lo|, |Hi|), a rational bound on |x| for x in a.
func (a Interval) MaxAbs() *big.Rat {
	return ratMax(ratAbs(a.Lo), ratAbs(a.Hi))
}

// Contains reports whether x lies in the interval.
func (a Interval) Contains(x *big.Rat) bool {
	return a.Lo.Cmp(x) <= 0 && x.Cmp(a.Hi) <= 0
}

func (a Interval) String() string {
	lo, _ := a.Lo.Float64()
	hi, _ := a.Hi.Float64()
	w, _ := a.Width().Float64()
	f := func(x float64) string { return strconv.FormatFloat(x, 'g', 17, 64) }
	return "[" + f(lo) + ", " + f(hi) + "] (width " + f(w) + ")"
}
