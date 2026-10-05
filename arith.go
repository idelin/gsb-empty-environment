package interval

import "math/big"

// Neg returns -x.
func (x Interval) Neg() Interval {
	if x.empty {
		return x
	}
	return Make(negExt(x.hi), negExt(x.lo), x.hiOpen, x.loOpen)
}

// Add returns x + y.
func (x Interval) Add(y Interval) Interval {
	if x.empty || y.empty {
		return Empty()
	}
	return Make(
		addExt(x.lo, y.lo), addExt(x.hi, y.hi),
		x.loOpen || y.loOpen, x.hiOpen || y.hiOpen,
	)
}

// Sub returns x - y.
func (x Interval) Sub(y Interval) Interval { return x.Add(y.Neg()) }

// Mul returns x * y. The product of two connected intervals is always
// connected; its extremes are limits of endpoint products, so the
// indeterminate form 0*Inf is resolved by the remaining products.
func (x Interval) Mul(y Interval) Interval {
	if x.empty || y.empty {
		return Empty()
	}
	type bound struct {
		v    Ext
		open bool
	}
	xb := []bound{{x.lo, x.loOpen}, {x.hi, x.hiOpen}}
	yb := []bound{{y.lo, y.loOpen}, {y.hi, y.hiOpen}}
	var lo, hi *bound
	for _, a := range xb {
		for _, b := range yb {
			v, ok := mulExt(a.v, b.v)
			if !ok {
				continue
			}
			open := a.open || b.open
			if lo == nil || cmpExt(v, lo.v) < 0 {
				c := bound{v, open}
				lo = &c
			} else if cmpExt(v, lo.v) == 0 {
				lo.open = lo.open && open
			}
			if hi == nil || cmpExt(v, hi.v) > 0 {
				c := bound{v, open}
				hi = &c
			} else if cmpExt(v, hi.v) == 0 {
				hi.open = hi.open && open
			}
		}
	}
	if lo == nil {
		// Every product was 0*Inf: one operand is {0} and the other
		// has no finite nonzero endpoint, so the product is {0}.
		return Point(new(big.Rat))
	}
	return Make(lo.v, hi.v, lo.open, hi.open)
}

// Div returns x / y as a Set. Division by an interval containing zero
// splits y at zero and yields up to two segments. Division by {0} is
// undefined and yields the empty set.
func (x Interval) Div(y Interval) *Set {
	out := NewSet(0)
	if x.empty || y.empty {
		return out
	}
	if y.IsPoint() && y.lo.val.Sign() == 0 {
		return out
	}
	zero := Fin(new(big.Rat))
	negPart := y.Intersect(Make(NegInf(), zero, true, true))
	posPart := y.Intersect(Make(zero, PosInf(), true, true))
	if !negPart.empty {
		out.Add(x.Mul(recip(negPart)))
	}
	if !posPart.empty {
		out.Add(x.Mul(recip(posPart)))
	}
	return out
}

// recip returns 1/y for a nonempty y contained in (0, +inf] or in
// [-inf, 0). y may touch 0 at an open endpoint or be unbounded.
func recip(y Interval) Interval {
	posSide := y.lo.Sign() >= 0
	invBound := func(e Ext, open bool) (Ext, bool) {
		if e.inf != 0 {
			// 1/(+-inf) = 0, never attained.
			return Fin(new(big.Rat)), true
		}
		if e.val.Sign() == 0 {
			// Open endpoint at 0: the reciprocal is unbounded.
			if posSide {
				return PosInf(), true
			}
			return NegInf(), true
		}
		return Fin(new(big.Rat).Inv(e.val)), open
	}
	lo, loOpen := invBound(y.hi, y.hiOpen)
	hi, hiOpen := invBound(y.lo, y.loOpen)
	return Make(lo, hi, loOpen, hiOpen)
}
