package calib

import (
	"math/big"
)

// EventInterval computes the exact global-time interval of an event at local
// time tau, given the channel constraints and its solved region.
//
// For a fixed integer offset b the optimal drift a sits at an endpoint of the
// feasible drift interval, so the optimal value f(b) of a*tau+b is piecewise
// linear in b. Its breakpoints are the pairwise intersections of the
// constraint boundary lines (expressed in b); hence the optimum over the
// integer offsets is attained at an integer adjacent to a breakpoint or to an
// end of the feasible offset range. Unbounded sides are detected exactly via
// the eventual slope of f. The result is exact, not a relaxation.
//
// The caller must ensure reg.IntFeasible. A nil bound in the result means the
// event time is unbounded on that side.
func EventInterval(cs []Constraint, aBounds Interval, reg Region, tau *big.Rat) Interval {
	var out Interval
	if !unboundedBelow(cs, aBounds, reg, tau) {
		var lo *big.Rat
		set := false
		for _, b := range candidateOffsets(cs, aBounds, reg) {
			v, ok := eventBoundAtOffset(cs, aBounds, b, tau, true)
			if !ok {
				continue
			}
			if v == nil { // drift unbounded for this offset
				lo, set = nil, true
				break
			}
			if !set || (lo != nil && v.Cmp(lo) < 0) {
				lo, set = v, true
			}
		}
		out.Lo = lo
	}
	if !unboundedAbove(cs, aBounds, reg, tau) {
		var hi *big.Rat
		set := false
		for _, b := range candidateOffsets(cs, aBounds, reg) {
			v, ok := eventBoundAtOffset(cs, aBounds, b, tau, false)
			if !ok {
				continue
			}
			if v == nil {
				hi, set = nil, true
				break
			}
			if !set || (hi != nil && v.Cmp(hi) > 0) {
				hi, set = v, true
			}
		}
		out.Hi = hi
	}
	return out
}

// aBoundLines collects the lines bounding the drift a as functions of the
// offset b: lower[i](b) <= a <= upper[i](b) for all i.
func aBoundLines(cs []Constraint, aBounds Interval) (lower, upper []line) {
	for _, c := range cs {
		if c.T.Sign() == 0 {
			continue // pure offset constraint, no drift bound
		}
		inv := new(big.Rat).Inv(c.T) // 1/t
		s := new(big.Rat).Neg(inv)   // slope -1/t
		loLine := line{slope: s, inter: new(big.Rat).Mul(c.Lo, inv)}
		hiLine := line{slope: s, inter: new(big.Rat).Mul(c.Hi, inv)}
		if c.T.Sign() > 0 {
			lower = append(lower, loLine)
			upper = append(upper, hiLine)
		} else {
			lower = append(lower, hiLine)
			upper = append(upper, loLine)
		}
	}
	if aBounds.Lo != nil {
		lower = append(lower, line{slope: RatInt(0), inter: aBounds.Lo})
	}
	if aBounds.Hi != nil {
		upper = append(upper, line{slope: RatInt(0), inter: aBounds.Hi})
	}
	return lower, upper
}

// eventualSlope returns the slope of the max (for lower lines) or min (for
// upper lines) of the given lines as b -> dir*infinity (dir = -1 or +1).
func eventualSlope(lines []line, dir int, isLower bool) *big.Rat {
	best := lines[0].slope
	for _, l := range lines[1:] {
		// For b -> +inf the max of lines follows the largest slope, the min
		// follows the smallest; for b -> -inf the roles swap.
		pickLarger := (dir > 0) == isLower
		if pickLarger && l.slope.Cmp(best) > 0 {
			best = l.slope
		}
		if !pickLarger && l.slope.Cmp(best) < 0 {
			best = l.slope
		}
	}
	return best
}

// unboundedBelow reports whether the event global time a*tau+b can be
// arbitrarily negative over the feasible (a, integer b) set.
func unboundedBelow(cs []Constraint, aBounds Interval, reg Region, tau *big.Rat) bool {
	lower, upper := aBoundLines(cs, aBounds)
	switch tau.Sign() {
	case 0: // f(b) = b
		return reg.BIntLo == nil
	case 1: // f(b) = alpha(b)*tau + b, alpha = max of lower lines
		if len(lower) == 0 {
			return true
		}
		return rayUnbounded(reg, tau, lower, true, true)
	default: // f(b) = beta(b)*tau + b, beta = min of upper lines
		if len(upper) == 0 {
			return true
		}
		return rayUnbounded(reg, tau, upper, false, true)
	}
}

// unboundedAbove reports whether the event global time can be arbitrarily
// positive over the feasible set.
func unboundedAbove(cs []Constraint, aBounds Interval, reg Region, tau *big.Rat) bool {
	lower, upper := aBoundLines(cs, aBounds)
	switch tau.Sign() {
	case 0:
		return reg.BIntHi == nil
	case 1: // f(b) = beta(b)*tau + b
		if len(upper) == 0 {
			return true
		}
		return rayUnbounded(reg, tau, upper, false, false)
	default: // f(b) = alpha(b)*tau + b
		if len(lower) == 0 {
			return true
		}
		return rayUnbounded(reg, tau, lower, true, false)
	}
}

// rayUnbounded checks the two offset rays for unboundedness of
// f(b) = bound(b)*tau + b, where bound is the max (isLower) or min of lines.
// minimize selects whether we ask about f -> -inf (true) or f -> +inf.
func rayUnbounded(reg Region, tau *big.Rat, lines []line, isLower, minimize bool) bool {
	check := func(dir int) bool {
		s := addRat(mulRat(eventualSlope(lines, dir, isLower), tau), RatInt(1))
		// f ~ s*b as b -> dir*inf.
		if minimize {
			return dir*s.Sign() < 0
		}
		return dir*s.Sign() > 0
	}
	if reg.BIntLo == nil && check(-1) {
		return true
	}
	if reg.BIntHi == nil && check(1) {
		return true
	}
	return false
}

// candidateOffsets returns every integer offset at which the optimal event
// time can be attained: integers adjacent to the ends of the feasible offset
// range and to every breakpoint of the piecewise-linear value function.
func candidateOffsets(cs []Constraint, aBounds Interval, reg Region) []*big.Int {
	seen := make(map[string]bool)
	var out []*big.Int
	add := func(r *big.Rat) {
		if r == nil {
			return
		}
		for _, b := range []*big.Int{FloorInt(r), CeilInt(r)} {
			if !inOffsetRange(b, reg) {
				continue
			}
			k := b.String()
			if !seen[k] {
				seen[k] = true
				out = append(out, b)
			}
		}
	}
	add(reg.B.Lo)
	add(reg.B.Hi)
	for i := 0; i < len(cs); i++ {
		for j := 0; j < len(cs); j++ {
			if i == j {
				continue
			}
			d := subRat(cs[i].T, cs[j].T)
			if d.Sign() == 0 {
				continue
			}
			// Intersection of two constraint boundary lines, solved for b:
			// b* = (t_i*g_j - t_j*g_i) / (t_i - t_j), g ranging over the
			// lower/upper bounds of both points.
			for _, gi := range [2]*big.Rat{cs[i].Lo, cs[i].Hi} {
				for _, gj := range [2]*big.Rat{cs[j].Lo, cs[j].Hi} {
					num := subRat(mulRat(cs[i].T, gj), mulRat(cs[j].T, gi))
					add(new(big.Rat).Quo(num, d))
				}
			}
		}
		// Breakpoints where a constraint line meets an a-priori drift bound.
		if aBounds.Lo != nil {
			add(subRat(cs[i].Lo, mulRat(aBounds.Lo, cs[i].T)))
			add(subRat(cs[i].Hi, mulRat(aBounds.Lo, cs[i].T)))
		}
		if aBounds.Hi != nil {
			add(subRat(cs[i].Lo, mulRat(aBounds.Hi, cs[i].T)))
			add(subRat(cs[i].Hi, mulRat(aBounds.Hi, cs[i].T)))
		}
	}
	if len(out) == 0 {
		// No breakpoints and no finite range ends (e.g. a single constraint
		// line): the value function is constant on every feasible ray, so
		// any feasible integer offset is an optimal candidate.
		switch {
		case reg.BIntLo != nil:
			out = append(out, reg.BIntLo)
		case reg.BIntHi != nil:
			out = append(out, reg.BIntHi)
		default:
			out = append(out, big.NewInt(0))
		}
	}
	return out
}

func inOffsetRange(b *big.Int, reg Region) bool {
	if reg.BIntLo != nil && b.Cmp(reg.BIntLo) < 0 {
		return false
	}
	if reg.BIntHi != nil && b.Cmp(reg.BIntHi) > 0 {
		return false
	}
	return true
}

// eventBoundAtOffset computes the min (or max) of a*tau + b over all feasible
// drift rates a for one fixed integer offset b. The returned bool reports
// whether any drift rate is feasible for this offset; a nil value together
// with ok=true means the bound is unbounded.
func eventBoundAtOffset(cs []Constraint, aBounds Interval, b *big.Int, tau *big.Rat, minimize bool) (*big.Rat, bool) {
	bRat := new(big.Rat).SetInt(b)
	aLo, aHi := aBounds.Lo, aBounds.Hi
	for _, c := range cs {
		switch c.T.Sign() {
		case 1:
			aLo = maxBound(aLo, new(big.Rat).Quo(subRat(c.Lo, bRat), c.T))
			aHi = minBound(aHi, new(big.Rat).Quo(subRat(c.Hi, bRat), c.T))
		case -1:
			aLo = maxBound(aLo, new(big.Rat).Quo(subRat(c.Hi, bRat), c.T))
			aHi = minBound(aHi, new(big.Rat).Quo(subRat(c.Lo, bRat), c.T))
		default:
			if bRat.Cmp(c.Lo) < 0 || bRat.Cmp(c.Hi) > 0 {
				return nil, false
			}
		}
	}
	if aLo != nil && aHi != nil && aLo.Cmp(aHi) > 0 {
		return nil, false
	}
	if minimize {
		switch tau.Sign() {
		case 1:
			if aLo == nil {
				return nil, true
			}
			return addRat(mulRat(aLo, tau), bRat), true
		case -1:
			if aHi == nil {
				return nil, true
			}
			return addRat(mulRat(aHi, tau), bRat), true
		default:
			return new(big.Rat).Set(bRat), true
		}
	}
	switch tau.Sign() {
	case 1:
		if aHi == nil {
			return nil, true
		}
		return addRat(mulRat(aHi, tau), bRat), true
	case -1:
		if aLo == nil {
			return nil, true
		}
		return addRat(mulRat(aLo, tau), bRat), true
	default:
		return new(big.Rat).Set(bRat), true
	}
}
