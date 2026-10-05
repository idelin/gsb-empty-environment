package trigcert

import "math/big"

var (
	ratZero = big.NewRat(0, 1)
	ratOne  = big.NewRat(1, 1)
)

// unitInterval is the trivially valid enclosure of sin and cos.
func unitInterval() Interval {
	return NewInterval(big.NewRat(-1, 1), big.NewRat(1, 1))
}

// Sin returns a certified enclosure of sin(x); see SinCos.
func Sin(x, eps *big.Rat, budget int) Interval {
	s, _ := SinCos(x, eps, budget)
	return s
}

// Cos returns a certified enclosure of cos(x); see SinCos.
func Cos(x, eps *big.Rat, budget int) Interval {
	_, c := SinCos(x, eps, budget)
	return c
}

// SinCos returns certified enclosures of sin(x) and cos(x) for a rational
// angle x (in radians). eps is the target tolerance: when the budget
// suffices, each returned interval has width at most eps. eps should be
// positive; a non-positive eps simply drives the refinement loops until
// the budget is exhausted, which still yields a valid enclosure.
//
// budget bounds the total number of series terms (pi refinement plus
// Taylor terms). Because every intermediate state of the computation is
// already a valid enclosure, exhausting the budget never breaks the
// guarantee: the current, possibly wider, enclosure is returned. With
// budget <= 0 the trivially valid enclosure [-1, 1] is returned.
func SinCos(x, eps *big.Rat, budget int) (Interval, Interval) {
	if x == nil {
		return unitInterval(), unitInterval()
	}
	if budget <= 0 {
		return unitInterval(), unitInterval()
	}
	if x.Sign() == 0 {
		return Point(ratZero), Point(ratOne)
	}
	if eps == nil {
		eps = ratOne
	}

	// Phase 1: coarse certified pi, only used to estimate the reduction
	// integer k. Any integer k yields a correct reduction, so a rough
	// estimate here cannot break the guarantee.
	coarseN := 8
	if 2*coarseN > budget {
		coarseN = budget / 2
		if coarseN < 1 {
			coarseN = 1
		}
	}
	budget -= 2 * coarseN
	piN := coarseN
	piI := PiInterval(piN)

	estimateK := func(pi Interval) *big.Int {
		twoPi := new(big.Rat).Mul(pi.Mid(), big.NewRat(2, 1))
		return nearestInt(new(big.Rat).Quo(x, twoPi))
	}
	k := estimateK(piI)

	// Phase 2: refine pi until
	//   width(pi) <= eps / (8(|k|+1))   (reduction width contributes <= eps/8)
	//   width(pi) <= 4 / (|x|+4)        (keeps k within 1 of the optimal)
	// or the budget runs out. Either way the reduction below stays valid.
	absK := new(big.Int).Abs(k)
	den := new(big.Rat).SetInt(new(big.Int).Add(absK, big.NewInt(1)))
	den.Mul(den, big.NewRat(8, 1))
	needW := new(big.Rat).Quo(eps, den)
	absX := new(big.Rat).Abs(x)
	needW2 := new(big.Rat).Quo(big.NewRat(4, 1), new(big.Rat).Add(absX, big.NewRat(4, 1)))
	if needW2.Cmp(needW) < 0 {
		needW = needW2
	}
	for piI.Width().Cmp(needW) > 0 && budget >= 2 {
		piN++
		budget -= 2
		piI = PiInterval(piN)
	}
	k = estimateK(piI)

	// Phase 3: range reduction in interval arithmetic with certified pi.
	// xred = x - 2k*pi is an interval guaranteed to contain x - 2k*pi_true.
	kpi := piI.ScaleInt(k).Scale(big.NewRat(2, 1))
	xred := Point(x).Sub(kpi)
	m := xred.Mid()
	hw := new(big.Rat).Quo(xred.Width(), big.NewRat(2, 1))

	// If the reduction width alone already covers the codomain, the
	// series cannot improve the enclosure; skip it.
	if hw.Cmp(big.NewRat(2, 1)) >= 0 {
		return unitInterval(), unitInterval()
	}

	// Phase 4: Taylor series at the rational midpoint m with Lagrange
	// remainder bounds. The remaining budget is split between sin and cos.
	target := new(big.Rat).Quo(eps, big.NewRat(8, 1))
	half := budget / 2
	sinI, usedS := sinSeries(m, target, half)
	cosI, usedC := cosSeries(m, target, budget-half)
	_ = usedS
	_ = usedC

	// Phase 5: propagate the reduction half-width (sin and cos are
	// 1-Lipschitz) and clip to the trivial bound [-1, 1].
	sinI = sinI.Expand(hw).Intersect(unitInterval())
	cosI = cosI.Expand(hw).Intersect(unitInterval())
	return sinI, cosI
}

// sinSeries encloses sin(m) using at most maxTerms Taylor terms, stopping
// early once the remainder bound drops to target or below. After t terms
// the partial sum equals the Taylor polynomial of degree 2t-1 (identically
// that of degree 2t), so the Lagrange remainder is bounded by
// |m|^(2t+1)/(2t+1)!, the magnitude of the first omitted term.
func sinSeries(m *big.Rat, target *big.Rat, maxTerms int) (Interval, int) {
	if maxTerms < 1 {
		maxTerms = 1
	}
	m2 := new(big.Rat).Mul(m, m)
	sum := new(big.Rat)
	term := new(big.Rat).Set(m) // degree-1 term
	used := 0
	for {
		sum.Add(sum, term)
		used++
		// First omitted term: -term * m^2 / ((2t)(2t+1)).
		next := new(big.Rat).Mul(term, m2)
		next.Neg(next)
		d := int64(2*used) * int64(2*used+1)
		next.Quo(next, new(big.Rat).SetInt64(d))
		rem := new(big.Rat).Abs(next)
		if rem.Cmp(target) <= 0 || used >= maxTerms {
			lo := new(big.Rat).Sub(sum, rem)
			hi := new(big.Rat).Add(sum, rem)
			return Interval{Lo: lo, Hi: hi}, used
		}
		term = next
	}
}

// cosSeries encloses cos(m); same argument as sinSeries with even powers.
// After t terms the remainder is bounded by |m|^(2t)/(2t)!.
func cosSeries(m *big.Rat, target *big.Rat, maxTerms int) (Interval, int) {
	if maxTerms < 1 {
		maxTerms = 1
	}
	m2 := new(big.Rat).Mul(m, m)
	sum := new(big.Rat)
	term := new(big.Rat).SetInt64(1) // degree-0 term
	used := 0
	for {
		sum.Add(sum, term)
		used++
		// First omitted term: -term * m^2 / ((2t-1)(2t)).
		next := new(big.Rat).Mul(term, m2)
		next.Neg(next)
		d := int64(2*used-1) * int64(2*used)
		next.Quo(next, new(big.Rat).SetInt64(d))
		rem := new(big.Rat).Abs(next)
		if rem.Cmp(target) <= 0 || used >= maxTerms {
			lo := new(big.Rat).Sub(sum, rem)
			hi := new(big.Rat).Add(sum, rem)
			return Interval{Lo: lo, Hi: hi}, used
		}
		term = next
	}
}

// nearestInt rounds a rational to the nearest integer, halves away from
// zero. Any integer is acceptable for range reduction; nearest keeps the
// reduced argument small.
func nearestInt(r *big.Rat) *big.Int {
	q := new(big.Int)
	rem := new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem) // truncates toward zero
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	if twice.Cmp(r.Denom()) >= 0 {
		if r.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return q
}
