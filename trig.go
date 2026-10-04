package certitrig

import (
	"math/big"
)

// nearestInt rounds a rational to the nearest integer (ties away
// from zero). Purely exact arithmetic; used only to *choose* the
// reduction quadrant, never affecting correctness of the enclosure.
func nearestInt(r *big.Rat) *big.Int {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	if twice.Cmp(den) >= 0 {
		if r.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return q
}

// sinCosSeries evaluates the Taylor series for sin and cos over the
// interval t, adding terms until both enclosures (including the
// certified Lagrange remainder) have width at most target, or the
// budget runs out. It always returns valid enclosures; the bool
// reports whether the target width was reached.
//
// Remainder bounds (derivatives of sin/cos are bounded by 1):
//
//	sin: after terms up to x^(2n+1)/(2n+1)!, |R| <= M^(2n+3)/(2n+3)!
//	cos: after terms up to x^(2n)/(2n)!,     |R| <= M^(2n+2)/(2n+2)!
//
// with M = max|t| over the interval.
func sinCosSeries(t Interval, target *big.Rat, budget *Budget,
	sinBest, cosBest Interval) (Interval, Interval, bool) {

	x2 := t.Square()
	m := t.MaxAbs()

	// Running interval sums; term intervals track x^k/k! with sign.
	sinTerm := t // n=0 term of sin: x
	sinSum := sinTerm
	cosTerm := IVPoint(big.NewRat(1, 1)) // n=0 term of cos: 1
	cosSum := cosTerm

	// Exact rational remainder bounds.
	sinRemPow := new(big.Rat).Set(m) // M^(2n+1) tracked incrementally
	cosRemPow := big.NewRat(1, 1)    // M^(2n)
	m2 := new(big.Rat).Mul(m, m)
	sinFact := big.NewRat(1, 1) // (2n+1)!
	cosFact := big.NewRat(1, 1) // (2n)!

	remBound := func(pow, fact *big.Rat) *big.Rat {
		return new(big.Rat).Quo(pow, fact)
	}

	check := func(n int) (Interval, Interval, bool) {
		// After including sin terms 0..n: |R_sin| <= M^(2n+3)/(2n+3)!
		sr := remBound(new(big.Rat).Mul(sinRemPow, m2),
			new(big.Rat).Mul(sinFact, big.NewRat(int64((2*n+2)*(2*n+3)), 1)))
		// After including cos terms 0..n: |R_cos| <= M^(2n+2)/(2n+2)!
		cr := remBound(new(big.Rat).Mul(cosRemPow, m2),
			new(big.Rat).Mul(cosFact, big.NewRat(int64((2*n+1)*(2*n+2)), 1)))
		s := sinSum.Add(IVSymmetric(sr))
		c := cosSum.Add(IVSymmetric(cr))
		s = sinBest.Intersect(s)
		c = cosBest.Intersect(c)
		done := target != nil && target.Sign() > 0 &&
			s.Width().Cmp(target) <= 0 && c.Width().Cmp(target) <= 0
		return s, c, done
	}

	s, c, done := check(0)
	if done {
		return s, c, true
	}
	for n := 1; ; n++ {
		if !budget.Tick() {
			return s, c, false
		}
		// sin term n: multiply previous by -x^2/((2n)(2n+1))
		sinTerm = sinTerm.Mul(x2).Scale(big.NewRat(-1, int64((2*n)*(2*n+1))))
		sinSum = sinSum.Add(sinTerm)
		// cos term n: multiply previous by -x^2/((2n-1)(2n))
		cosTerm = cosTerm.Mul(x2).Scale(big.NewRat(-1, int64((2*n-1)*(2*n))))
		cosSum = cosSum.Add(cosTerm)
		// advance exact remainder powers and factorials
		sinRemPow.Mul(sinRemPow, m2)
		cosRemPow.Mul(cosRemPow, m2)
		sinFact.Mul(sinFact, big.NewRat(int64((2*n)*(2*n+1)), 1))
		cosFact.Mul(cosFact, big.NewRat(int64((2*n-1)*(2*n)), 1))
		s, c, done = check(n)
		if done {
			return s, c, true
		}
		if budget != nil && budget.Left <= 0 {
			return s, c, false
		}
	}
}

// SinCos returns certified rational enclosures of sin(x) and cos(x)
// for a rational angle x (radians). If eps is positive, refinement
// stops once both interval widths are at most eps; the budget caps
// total work, and on exhaustion the current valid enclosures are
// returned (initially [-1, 1], always a legal enclosure).
//
// Range reduction uses only certified rational intervals for pi/2
// (Machin formula with remainder bounds); no floating-point
// constants are used anywhere in the computation.
func SinCos(x, eps *big.Rat, budget *Budget) (sinI, cosI Interval) {
	one := big.NewRat(1, 1)
	sinBest := IVSymmetric(one) // |sin| <= 1 always
	cosBest := IVSymmetric(one) // |cos| <= 1 always

	target := eps
	if target == nil {
		target = new(big.Rat)
	}
	if budget == nil && target.Sign() <= 0 {
		// No stopping criterion otherwise: default to 2^-64.
		target = new(big.Rat).SetFrac(big.NewInt(1),
			new(big.Int).Lsh(big.NewInt(1), 64))
	}

	// Coarse certified pi/2 for a first quadrant estimate. Any
	// quadrant choice yields a valid enclosure; a good one keeps the
	// reduced argument small and the series short.
	coarseDelta := new(big.Rat).SetFrac(big.NewInt(1), big.NewInt(1<<20))
	coarseBudget := &Budget{Left: 64}
	ph := PiHalfInterval(coarseDelta, coarseBudget)

	// Precision needed for pi/2: the reduction residual width is
	// |q| * width(piHalf), and it propagates ~1:1 into the results
	// (derivatives of sin/cos are bounded by 1).
	piDelta := func(q *big.Int) *big.Rat {
		denom := new(big.Int).Abs(q)
		denom.Mul(denom, big.NewInt(16))
		denom.Add(denom, big.NewInt(64))
		d := new(big.Rat).Quo(target, new(big.Rat).SetInt(denom))
		if target.Sign() <= 0 {
			d = new(big.Rat).SetFrac(big.NewInt(1),
				new(big.Int).Lsh(big.NewInt(1), 64))
		}
		return d
	}

	// Size the pi/2 precision from the coarse quadrant estimate,
	// compute a fine certified pi/2, then re-select the quadrant
	// against it: the coarse estimate can be off by many quadrants
	// for huge x, which would leave an enormous residual argument.
	q := nearestInt(new(big.Rat).Quo(x, ph.Mid()))
	delta := piDelta(q)
	if budget == nil || budget.Left > 0 {
		ph = PiHalfInterval(delta, budget)
		q = nearestInt(new(big.Rat).Quo(x, ph.Mid()))
		delta = piDelta(q)
	}
	qRat := new(big.Rat).SetInt(q)
	quad := new(big.Int).Mod(q, big.NewInt(4)).Int64()

	for {
		if budget != nil && budget.Left <= 0 {
			return sinBest, cosBest
		}
		ph = PiHalfInterval(delta, budget)
		// t = x - q*(pi/2), certified interval residual.
		t := IVPoint(x).Sub(ph.Scale(qRat))

		s, c, _ := sinCosSeries(t, target, budget, sinBest, cosBest)

		// Map the reduced-argument enclosures through the quadrant.
		var rs, rc Interval
		switch quad {
		case 0:
			rs, rc = s, c
		case 1:
			rs, rc = c, s.Neg()
		case 2:
			rs, rc = s.Neg(), c.Neg()
		default: // 3
			rs, rc = c.Neg(), s
		}
		sinBest = sinBest.Intersect(rs)
		cosBest = cosBest.Intersect(rc)

		if target.Sign() > 0 &&
			sinBest.Width().Cmp(target) <= 0 &&
			cosBest.Width().Cmp(target) <= 0 {
			return sinBest, cosBest
		}
		if budget != nil && budget.Left <= 0 {
			return sinBest, cosBest
		}
		// Refine the certified pi/2 and retry with a narrower residual.
		delta.Quo(delta, big.NewRat(4, 1))
	}
}

// Sin returns a certified enclosure of sin(x).
func Sin(x, eps *big.Rat, budget *Budget) Interval {
	s, _ := SinCos(x, eps, budget)
	return s
}

// Cos returns a certified enclosure of cos(x).
func Cos(x, eps *big.Rat, budget *Budget) Interval {
	_, c := SinCos(x, eps, budget)
	return c
}
