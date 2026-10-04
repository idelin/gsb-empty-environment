package domain

import "math/big"

// bounds is a rigorous rational enclosure: lo <= f(x) <= hi for all x in
// the interval; an open flag means the endpoint value itself is not attained.
type bounds struct {
	lo, hi         *big.Rat
	loOpen, hiOpen bool
}

// evalInterval evaluates e over iv with exact rational interval arithmetic.
// ok=false means no finite rational enclosure could be derived (e.g. a
// denominator straddling zero, or an even root of a possibly negative base).
func evalInterval(e *Expr, iv Interval) (bounds, bool) {
	switch e.Kind {
	case KConst:
		return bounds{new(big.Rat).Set(e.C), new(big.Rat).Set(e.C), false, false}, true
	case KVar:
		return bounds{iv.Lo.Val, iv.Hi.Val, iv.Lo.Open, iv.Hi.Open}, true
	case KNeg:
		b, ok := evalInterval(e.A, iv)
		if !ok {
			return bounds{}, false
		}
		return bounds{new(big.Rat).Neg(b.hi), new(big.Rat).Neg(b.lo), b.hiOpen, b.loOpen}, true
	case KAdd, KSub:
		a, ok1 := evalInterval(e.A, iv)
		b, ok2 := evalInterval(e.B, iv)
		if !ok1 || !ok2 {
			return bounds{}, false
		}
		if e.Kind == KSub {
			b = bounds{new(big.Rat).Neg(b.hi), new(big.Rat).Neg(b.lo), b.hiOpen, b.loOpen}
		}
		return bounds{
			new(big.Rat).Add(a.lo, b.lo), new(big.Rat).Add(a.hi, b.hi),
			a.loOpen || b.loOpen, a.hiOpen || b.hiOpen,
		}, true
	case KMul:
		a, ok1 := evalInterval(e.A, iv)
		b, ok2 := evalInterval(e.B, iv)
		if !ok1 || !ok2 {
			return bounds{}, false
		}
		if exprEqual(e.A, e.B) {
			// Same sub-expression multiplied by itself: avoid the
			// interval-arithmetic dependency blow-up, it is a square.
			return squareBounds(a), true
		}
		type corner struct {
			v    *big.Rat
			open bool
		}
		corners := []corner{
			{new(big.Rat).Mul(a.lo, b.lo), a.loOpen || b.loOpen},
			{new(big.Rat).Mul(a.lo, b.hi), a.loOpen || b.hiOpen},
			{new(big.Rat).Mul(a.hi, b.lo), a.hiOpen || b.loOpen},
			{new(big.Rat).Mul(a.hi, b.hi), a.hiOpen || b.hiOpen},
		}
		lo, hi := corners[0], corners[0]
		for _, c := range corners[1:] {
			if c.v.Cmp(lo.v) < 0 {
				lo = c
			} else if c.v.Cmp(lo.v) == 0 {
				lo.open = lo.open && c.open
			}
			if c.v.Cmp(hi.v) > 0 {
				hi = c
			} else if c.v.Cmp(hi.v) == 0 {
				hi.open = hi.open && c.open
			}
		}
		return bounds{lo.v, hi.v, lo.open, hi.open}, true
	case KDiv:
		num, ok1 := evalInterval(e.A, iv)
		den, ok2 := evalInterval(e.B, iv)
		if !ok1 || !ok2 {
			return bounds{}, false
		}
		// The denominator must be bounded strictly away from zero,
		// otherwise the quotient is unbounded or undefined.
		if !(den.lo.Sign() > 0 || den.hi.Sign() < 0) {
			return bounds{}, false
		}
		return divBounds(num, den), true
	case KPow:
		base, ok := evalInterval(e.A, iv)
		if !ok {
			return bounds{}, false
		}
		p := e.Exp.Num().Int64()
		q := e.Exp.Denom().Int64()
		return powBounds(base, p, q)
	}
	return bounds{}, false
}

func divBounds(a, b bounds) bounds {
	inv := bounds{new(big.Rat).Inv(b.hi), new(big.Rat).Inv(b.lo), b.hiOpen, b.loOpen}
	if b.lo.Sign() < 0 { // both bounds negative: Inv swaps order already handled by signs
		inv = bounds{new(big.Rat).Inv(b.hi), new(big.Rat).Inv(b.lo), b.hiOpen, b.loOpen}
	}
	// a * (1/b); reuse mul logic via corners
	type corner struct {
		v    *big.Rat
		open bool
	}
	corners := []corner{
		{new(big.Rat).Mul(a.lo, inv.lo), a.loOpen || inv.loOpen},
		{new(big.Rat).Mul(a.lo, inv.hi), a.loOpen || inv.hiOpen},
		{new(big.Rat).Mul(a.hi, inv.lo), a.hiOpen || inv.loOpen},
		{new(big.Rat).Mul(a.hi, inv.hi), a.hiOpen || inv.hiOpen},
	}
	lo, hi := corners[0], corners[0]
	for _, c := range corners[1:] {
		if c.v.Cmp(lo.v) < 0 {
			lo = c
		} else if c.v.Cmp(lo.v) == 0 {
			lo.open = lo.open && c.open
		}
		if c.v.Cmp(hi.v) > 0 {
			hi = c
		} else if c.v.Cmp(hi.v) == 0 {
			hi.open = hi.open && c.open
		}
	}
	return bounds{lo.v, hi.v, lo.open, hi.open}
}

// powBounds encloses x^(p/q) for x in b. All decisions are exact.
func powBounds(b bounds, p, q int64) (bounds, bool) {
	switch {
	case p == 0:
		one := big.NewRat(1, 1)
		return bounds{one, new(big.Rat).Set(one), false, false}, true
	case p < 0:
		// x^(p/q) = 1 / x^((-p)/q); needs the positive power bounded away from 0.
		g, ok := powBounds(b, -p, q)
		if !ok || g.lo.Sign() == 0 {
			return bounds{}, false
		}
		return bounds{new(big.Rat).Inv(g.hi), new(big.Rat).Inv(g.lo), g.hiOpen, g.loOpen}, true
	}
	// p > 0 from here.
	if q == 1 {
		return intPowBounds(b, p), true
	}
	if q%2 == 0 {
		// Even denominator: the real branch requires a non-negative base.
		if b.lo.Sign() < 0 {
			return bounds{}, false
		}
		lo := ratPowBound(b.lo, p, q, false)
		hi := ratPowBound(b.hi, p, q, true)
		return bounds{lo, hi, b.loOpen, b.hiOpen}, true
	}
	// Odd denominator: defined for all reals, f(-x) = (-1)^p f(x).
	if p%2 == 1 { // strictly increasing
		return bounds{signedPowBound(b.lo, p, q, false), signedPowBound(b.hi, p, q, true), b.loOpen, b.hiOpen}, true
	}
	// p even: symmetric like x^2.
	switch {
	case b.lo.Sign() >= 0:
		return bounds{ratPowBound(b.lo, p, q, false), ratPowBound(b.hi, p, q, true), b.loOpen, b.hiOpen}, true
	case b.hi.Sign() <= 0:
		return bounds{ratPowBound(new(big.Rat).Neg(b.hi), p, q, false), ratPowBound(new(big.Rat).Neg(b.lo), p, q, true), b.hiOpen, b.loOpen}, true
	default:
		loAbs := new(big.Rat).Neg(b.lo)
		c1 := ratPowBound(loAbs, p, q, true)
		c2 := ratPowBound(b.hi, p, q, true)
		hi, hiOpen := c1, b.loOpen
		if c2.Cmp(c1) > 0 {
			hi, hiOpen = c2, b.hiOpen
		} else if c2.Cmp(c1) == 0 {
			hiOpen = b.loOpen && b.hiOpen
		}
		return bounds{big.NewRat(0, 1), hi, false, hiOpen}, true
	}
}

// intPowBounds encloses x^p for integer p > 0, exactly.
func intPowBounds(b bounds, p int64) bounds {
	loP := ratPowInt(b.lo, p)
	hiP := ratPowInt(b.hi, p)
	if p%2 == 1 || b.lo.Sign() >= 0 {
		return bounds{loP, hiP, b.loOpen, b.hiOpen}
	}
	if b.hi.Sign() <= 0 {
		return bounds{hiP, loP, b.hiOpen, b.loOpen}
	}
	// Straddles zero: minimum 0 (attained), maximum at an endpoint.
	hi, hiOpen := loP, b.loOpen
	if hiP.Cmp(hi) > 0 {
		hi, hiOpen = hiP, b.hiOpen
	} else if hiP.Cmp(hi) == 0 {
		hiOpen = b.loOpen && b.hiOpen
	}
	return bounds{big.NewRat(0, 1), hi, false, hiOpen}
}

// ratPowInt returns x^p exactly for p >= 0.
func ratPowInt(x *big.Rat, p int64) *big.Rat {
	n := new(big.Int).Exp(x.Num(), big.NewInt(p), nil)
	d := new(big.Int).Exp(x.Denom(), big.NewInt(p), nil)
	return new(big.Rat).SetFrac(n, d)
}

// ratPowBound returns a rigorous rational bound of x^(p/q) for x >= 0,
// p > 0, q >= 1: a lower bound when upper=false, an upper bound otherwise.
// It uses exact big.Rat bisection — no floating point anywhere.
func ratPowBound(x *big.Rat, p, q int64, upper bool) *big.Rat {
	if x.Sign() == 0 {
		return big.NewRat(0, 1)
	}
	target := ratPowInt(x, p) // compare r^q against x^p
	lo := big.NewRat(0, 1)
	hi := big.NewRat(1, 1)
	two := big.NewRat(2, 1)
	for ratPowInt(hi, q).Cmp(target) < 0 {
		hi.Mul(hi, two)
	}
	// Invariant: lo^q <= target <= hi^q.
	for i := 0; i < 128; i++ {
		mid := new(big.Rat).Add(lo, hi)
		mid.Quo(mid, two)
		if ratPowInt(mid, q).Cmp(target) <= 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	if upper {
		return hi
	}
	return lo
}

// signedPowBound bounds x^(p/q) for odd q and p > 0, any real x.
func signedPowBound(x *big.Rat, p, q int64, upper bool) *big.Rat {
	if x.Sign() >= 0 {
		return ratPowBound(x, p, q, upper)
	}
	abs := new(big.Rat).Neg(x)
	if p%2 == 0 {
		return ratPowBound(abs, p, q, upper)
	}
	// Odd numerator: x^(p/q) = -(|x|^(p/q)).
	return new(big.Rat).Neg(ratPowBound(abs, p, q, !upper))
}

// squareBounds encloses v^2 given an enclosure of v.
func squareBounds(b bounds) bounds {
	lo2 := new(big.Rat).Mul(b.lo, b.lo)
	hi2 := new(big.Rat).Mul(b.hi, b.hi)
	switch {
	case b.lo.Sign() >= 0:
		return bounds{lo2, hi2, b.loOpen, b.hiOpen}
	case b.hi.Sign() <= 0:
		return bounds{hi2, lo2, b.hiOpen, b.loOpen}
	default:
		hi, hiOpen := lo2, b.loOpen
		if hi2.Cmp(hi) > 0 {
			hi, hiOpen = hi2, b.hiOpen
		} else if hi2.Cmp(hi) == 0 {
			hiOpen = b.loOpen && b.hiOpen
		}
		return bounds{big.NewRat(0, 1), hi, false, hiOpen}
	}
}

// exprEqual reports structural equality of two expression trees.
func exprEqual(a, b *Expr) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KConst:
		return a.C.Cmp(b.C) == 0
	case KVar:
		return a.Name == b.Name
	case KNeg:
		return exprEqual(a.A, b.A)
	case KPow:
		return a.Exp.Cmp(b.Exp) == 0 && exprEqual(a.A, b.A)
	}
	return exprEqual(a.A, b.A) && exprEqual(a.B, b.B)
}
