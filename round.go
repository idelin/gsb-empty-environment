package intervalround

import "math/big"

// Round returns a decimal enclosure of m with the given number of decimal
// fraction digits: every finite lower endpoint is rounded down and every
// finite upper endpoint is rounded up to a multiple of 10^-digits.
//
// An endpoint that is already an exact decimal keeps its openness; an
// endpoint moved outward becomes closed, which is always safe because it
// then lies strictly outside the exact set. Infinite endpoints are
// unchanged. The result therefore always contains m, and rounding never
// turns a non-empty set into an empty one.
func (m Multi) Round(digits uint) Multi {
	if m.IsEmpty() {
		return m
	}
	out := make([]Interval, 0, len(m.Segs))
	for _, s := range m.Segs {
		out = append(out, Interval{
			Lo: roundLo(s.Lo, digits),
			Hi: roundHi(s.Hi, digits),
		})
	}
	// Outward rounding can make adjacent segments touch or overlap;
	// re-normalize to keep the canonical disjoint form.
	return Multi{Segs: normalize(out)}
}

func roundLo(b Bound, digits uint) Bound {
	if b.Inf != 0 {
		return b
	}
	r := floorDec(b.Rat, digits)
	if r.Cmp(b.Rat) == 0 {
		return Bound{Rat: r, Open: b.Open}
	}
	return Bound{Rat: r}
}

func roundHi(b Bound, digits uint) Bound {
	if b.Inf != 0 {
		return b
	}
	r := ceilDec(b.Rat, digits)
	if r.Cmp(b.Rat) == 0 {
		return Bound{Rat: r, Open: b.Open}
	}
	return Bound{Rat: r}
}

// pow10 returns 10^digits.
func pow10(digits uint) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(digits)), nil)
}

// floorDec returns the largest multiple of 10^-digits that is <= r.
func floorDec(r *big.Rat, digits uint) *big.Rat {
	p := pow10(digits)
	num := new(big.Int).Mul(r.Num(), p)
	q := new(big.Int)
	// DivMod is Euclidean: for a positive denominator q is the floor.
	q.DivMod(num, r.Denom(), new(big.Int))
	return new(big.Rat).SetFrac(q, p)
}

// ceilDec returns the smallest multiple of 10^-digits that is >= r.
func ceilDec(r *big.Rat, digits uint) *big.Rat {
	neg := new(big.Rat).Neg(r)
	return new(big.Rat).Neg(floorDec(neg, digits))
}
