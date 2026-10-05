package intervalround

import "math/big"

// maxLo returns the larger of two lower bounds; on a tie the endpoint is
// included only if both include it.
func maxLo(a, b Bound) Bound {
	switch compare(a, b) {
	case 1:
		return a
	case -1:
		return b
	}
	return Bound{Inf: a.Inf, Rat: a.Rat, Open: a.Open || b.Open}
}

// minHi returns the smaller of two upper bounds; on a tie the endpoint is
// included only if both include it.
func minHi(a, b Bound) Bound {
	switch compare(a, b) {
	case -1:
		return a
	case 1:
		return b
	}
	return Bound{Inf: a.Inf, Rat: a.Rat, Open: a.Open || b.Open}
}

// Intersect returns the exact intersection of two sets.
func Intersect(a, b Multi) Multi {
	var out []Interval
	for _, x := range a.Segs {
		for _, y := range b.Segs {
			iv := Interval{Lo: maxLo(x.Lo, y.Lo), Hi: minHi(x.Hi, y.Hi)}
			if !iv.empty() {
				out = append(out, iv)
			}
		}
	}
	return Multi{Segs: normalize(out)}
}

// Union returns the union of two sets. If the number of disjoint segments
// would exceed maxSegs, adjacent segments with the smallest gap are merged
// into their convex hull; merging only ever enlarges the set, so the result
// still contains every point of the exact union.
func Union(a, b Multi, maxSegs int) Multi {
	all := make([]Interval, 0, len(a.Segs)+len(b.Segs))
	all = append(all, a.Segs...)
	all = append(all, b.Segs...)
	return Multi{Segs: limitSegs(normalize(all), maxSegs)}
}

// limitSegs conservatively merges adjacent segments until at most max
// remain. max is clamped to at least 1 so a non-empty set never becomes
// empty.
func limitSegs(segs []Interval, max int) []Interval {
	if max < 1 {
		max = 1
	}
	for len(segs) > max {
		best := 0
		bestGap := gap(segs[0], segs[1])
		for i := 1; i+1 < len(segs); i++ {
			if g := gap(segs[i], segs[i+1]); g.Cmp(bestGap) < 0 {
				best, bestGap = i, g
			}
		}
		segs[best].Hi = segs[best+1].Hi
		segs = append(segs[:best+1], segs[best+2:]...)
	}
	return segs
}

// gap is the rational distance between two adjacent segments. Interior
// adjacency guarantees both endpoints are finite.
func gap(a, b Interval) *big.Rat {
	return new(big.Rat).Sub(b.Lo.Rat, a.Hi.Rat)
}

// Div returns the exact set {x/y : x in a, y in b, y != 0}. Divisors
// straddling zero are split at zero, so the result may consist of several
// disjoint segments (possibly unbounded). If the segment count would
// exceed maxSegs, adjacent segments are conservatively hull-merged.
func Div(a, b Multi, maxSegs int) Multi {
	var out []Interval
	for _, x := range a.Segs {
		for _, y := range b.Segs {
			for _, part := range splitZero(y) {
				r := mul(x, recip(part))
				if !r.empty() {
					out = append(out, r)
				}
			}
		}
	}
	return Multi{Segs: limitSegs(normalize(out), maxSegs)}
}

// splitZero splits a divisor segment at zero; zero itself is always
// excluded because division by zero is undefined.
func splitZero(y Interval) []Interval {
	zero := Bound{Rat: new(big.Rat), Open: true}
	var parts []Interval
	if compare(y.Lo, zero) < 0 {
		parts = append(parts, Interval{Lo: y.Lo, Hi: zero})
	}
	if compare(zero, y.Hi) < 0 {
		parts = append(parts, Interval{Lo: zero, Hi: y.Hi})
	}
	return parts
}

// recip returns 1/y for a segment y that does not cross zero (its zero
// endpoint, if any, is open).
func recip(y Interval) Interval {
	positive := y.Hi.Inf > 0 || (y.Hi.Inf == 0 && y.Hi.Rat.Sign() > 0)
	side := 1
	if !positive {
		side = -1
	}
	return Interval{Lo: recipBound(y.Hi, side), Hi: recipBound(y.Lo, side)}
}

// recipBound maps a bound b to 1/b. side gives the sign of the interval
// interior, used when b is an open zero endpoint.
func recipBound(b Bound, side int) Bound {
	if b.Inf != 0 {
		return Bound{Rat: new(big.Rat), Open: true} // 1/±inf -> 0, excluded
	}
	if b.Rat.Sign() == 0 {
		return Bound{Inf: side, Open: true} // open 0 endpoint -> ±inf
	}
	return Bound{Rat: new(big.Rat).Inv(b.Rat), Open: b.Open}
}

// mul returns the exact product of two segments. The product of two
// connected sets is connected, and a bilinear form attains its extrema at
// rectangle corners, so evaluating the four corner products suffices.
func mul(x, y Interval) Interval {
	cands := [4]Bound{
		mulBound(x.Lo, y.Lo),
		mulBound(x.Lo, y.Hi),
		mulBound(x.Hi, y.Lo),
		mulBound(x.Hi, y.Hi),
	}
	lo, hi := cands[0], cands[0]
	for _, c := range cands[1:] {
		if compare(c, lo) < 0 {
			lo = c
		}
		if compare(c, hi) > 0 {
			hi = c
		}
	}
	// An extremum is included if any corner attaining it is included.
	loOpen, hiOpen := true, true
	for _, c := range cands {
		if compare(c, lo) == 0 && !c.Open {
			loOpen = false
		}
		if compare(c, hi) == 0 && !c.Open {
			hiOpen = false
		}
	}
	lo.Open, hi.Open = loOpen, hiOpen
	return Interval{Lo: lo, Hi: hi}
}

// mulBound multiplies two endpoint bounds.
func mulBound(a, b Bound) Bound {
	if a.Inf == 0 && b.Inf == 0 {
		return Bound{Rat: new(big.Rat).Mul(a.Rat, b.Rat), Open: a.Open || b.Open}
	}
	if a.Inf != 0 && b.Inf != 0 {
		return Bound{Inf: a.Inf * b.Inf, Open: true}
	}
	inf, fin := a, b
	if inf.Inf == 0 {
		inf, fin = b, a
	}
	switch fin.Rat.Sign() {
	case 0:
		// 0 * inf: the value 0 is attained iff the zero endpoint is.
		return Bound{Rat: new(big.Rat), Open: fin.Open}
	case 1:
		return Bound{Inf: inf.Inf, Open: true}
	}
	return Bound{Inf: -inf.Inf, Open: true}
}
