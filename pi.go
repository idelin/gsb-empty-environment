package certitrig

import "math/big"

// Budget is a work counter. Every series term consumes one unit.
// When it runs out, the current (always valid) enclosure is returned.
type Budget struct{ Left int64 }

// Tick consumes one unit of work; false means the budget is exhausted.
func (b *Budget) Tick() bool {
	if b == nil {
		return true // nil budget means unlimited
	}
	if b.Left <= 0 {
		return false
	}
	b.Left--
	return true
}

// arctanInv returns a certified rational interval for arctan(1/q),
// q >= 2, using the alternating Taylor series with at most nTerms
// terms and the standard alternating-series remainder bound
// |R| <= (1/q)^(2N+3) / (2N+3). All arithmetic is exact rational;
// the only widening is the certified remainder.
func arctanInv(q int64, nTerms int, budget *Budget) Interval {
	y := big.NewRat(1, q)
	y2 := new(big.Rat).Mul(y, y)
	// term holds y^(2n+1) / (2n+1) with sign applied.
	term := new(big.Rat).Set(y)
	sum := new(big.Rat).Set(term)
	n := 0
	for n < nTerms {
		if !budget.Tick() {
			break
		}
		// advance to next term: multiply by y^2, flip sign,
		// rescale from 1/(2n+1) to 1/(2n+3).
		term.Mul(term, y2)
		term.Mul(term, big.NewRat(int64(2*n+1), int64(2*n+3)))
		term.Neg(term)
		sum.Add(sum, term)
		n++
	}
	// Certified remainder: |R| <= y^(2n+3) / (2n+3) (alternating
	// series with terms decreasing in magnitude for |y| <= 1).
	r := new(big.Rat).Set(y)
	for i := 0; i < 2*n+2; i++ {
		r.Mul(r, y)
	}
	r.Quo(r, big.NewRat(int64(2*n+3), 1))
	return Interval{
		new(big.Rat).Sub(sum, r),
		new(big.Rat).Add(sum, r),
	}
}

// PiInterval returns a certified rational interval containing pi,
// computed via Machin's formula pi = 16*arctan(1/5) - 4*arctan(1/239).
// It increases the term count until the interval width is at most
// delta (if delta is positive) or the budget runs out; the returned
// interval is a valid enclosure of pi regardless.
func PiInterval(delta *big.Rat, budget *Budget) Interval {
	if (delta == nil || delta.Sign() <= 0) && budget == nil {
		// Avoid an unbounded refinement loop: default to 2^-64.
		delta = new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), 64))
	}
	best := arctanInv(5, 1, budget)
	best = best.Scale(big.NewRat(16, 1))
	a239 := arctanInv(239, 1, budget)
	best = best.Sub(a239.Scale(big.NewRat(4, 1)))
	for n := 2; ; n *= 2 {
		if delta != nil && delta.Sign() > 0 && best.Width().Cmp(delta) <= 0 {
			return best
		}
		if budget != nil && budget.Left <= 0 {
			return best
		}
		a5 := arctanInv(5, n, budget)
		a2 := arctanInv(239, n, budget)
		cand := a5.Scale(big.NewRat(16, 1)).Sub(a2.Scale(big.NewRat(4, 1)))
		best = best.Intersect(cand)
	}
}

// PiHalfInterval returns a certified interval for pi/2 with width at
// most delta (budget permitting).
func PiHalfInterval(delta *big.Rat, budget *Budget) Interval {
	d := delta
	if d == nil {
		d = new(big.Rat)
	}
	pi := PiInterval(new(big.Rat).Mul(d, big.NewRat(2, 1)), budget)
	return pi.Scale(big.NewRat(1, 2))
}
