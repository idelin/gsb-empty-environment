package calib

import (
	"math/big"
)

// Constraint is a single calibration point: at local time T the true global
// time lies in [Lo, Hi]. It imposes Lo <= a*T + b <= Hi on the channel
// parameters (drift rate a, offset b).
type Constraint struct {
	ID string
	T  *big.Rat
	Lo *big.Rat
	Hi *big.Rat
}

// Region describes the exact feasible set of (drift a, offset b) for one
// channel. Drift a is rational; offset b must additionally be an integer.
type Region struct {
	Feasible    bool     // feasible over the rationals
	A           Interval // feasible drift-rate interval
	B           Interval // feasible offset interval (real relaxation)
	IntFeasible bool     // feasible with the integer-offset requirement
	BIntLo      *big.Int // smallest feasible integer offset (nil = -inf)
	BIntHi      *big.Int // largest feasible integer offset (nil = +inf)
}

type line struct {
	slope *big.Rat
	inter *big.Rat
}

func (l line) at(x *big.Rat) *big.Rat { return addRat(mulRat(l.slope, x), l.inter) }

// Solve computes the exact feasible region for the given calibration
// constraints plus optional a-priori drift bounds.
func Solve(cs []Constraint, aBounds Interval) Region {
	reg := Region{}
	aLo, aHi, ok := driftInterval(cs, aBounds)
	if !ok {
		return reg
	}
	reg.Feasible = true
	reg.A = Interval{Lo: aLo, Hi: aHi}

	if len(cs) == 0 {
		reg.B = Interval{} // (-inf, +inf)
	} else {
		uLines := make([]line, len(cs))
		lLines := make([]line, len(cs))
		for i, c := range cs {
			neg := new(big.Rat).Neg(c.T)
			uLines[i] = line{slope: neg, inter: c.Hi}
			lLines[i] = line{slope: neg, inter: c.Lo}
		}
		reg.B = Interval{
			Lo: minOfMaxLines(aLo, aHi, lLines),
			Hi: maxOfMinLines(aLo, aHi, uLines),
		}
	}

	reg.BIntLo, reg.BIntHi, reg.IntFeasible = integerOffsets(reg.B)
	return reg
}

// integerOffsets derives the feasible integer-offset range from the real
// offset interval. The projection of the convex feasible set onto the b axis
// is exactly [B.Lo, B.Hi], so every integer inside it is attainable.
func integerOffsets(b Interval) (lo, hi *big.Int, ok bool) {
	if b.Lo != nil {
		lo = CeilInt(b.Lo)
	}
	if b.Hi != nil {
		hi = FloorInt(b.Hi)
	}
	if lo != nil && hi != nil && lo.Cmp(hi) > 0 {
		return lo, hi, false
	}
	return lo, hi, true
}

// driftInterval intersects all pairwise constraints
// a*(t_j - t_i) <= hi_j - lo_i into a single interval for a.
func driftInterval(cs []Constraint, aBounds Interval) (lo, hi *big.Rat, ok bool) {
	lo, hi = aBounds.Lo, aBounds.Hi
	for i := 0; i < len(cs); i++ {
		for j := 0; j < len(cs); j++ {
			d := subRat(cs[j].T, cs[i].T)
			rhs := subRat(cs[j].Hi, cs[i].Lo)
			switch d.Sign() {
			case 1:
				hi = minBound(hi, new(big.Rat).Quo(rhs, d))
			case -1:
				lo = maxBound(lo, new(big.Rat).Quo(rhs, d))
			default:
				if cs[i].Lo.Cmp(cs[j].Hi) > 0 {
					return nil, nil, false
				}
			}
		}
	}
	if lo != nil && hi != nil && lo.Cmp(hi) > 0 {
		return nil, nil, false
	}
	return lo, hi, true
}

// candidateA collects all a-values where a piecewise-linear bound over
// [aLo, aHi] can attain an extremum: interval endpoints and pairwise line
// intersections.
func candidateA(aLo, aHi *big.Rat, lines []line) []*big.Rat {
	inA := func(x *big.Rat) bool {
		if x == nil {
			return false
		}
		if aLo != nil && x.Cmp(aLo) < 0 {
			return false
		}
		if aHi != nil && x.Cmp(aHi) > 0 {
			return false
		}
		return true
	}
	var out []*big.Rat
	if inA(aLo) {
		out = append(out, aLo)
	}
	if inA(aHi) {
		out = append(out, aHi)
	}
	if inA(RatInt(0)) {
		out = append(out, RatInt(0))
	}
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			d := subRat(lines[i].slope, lines[j].slope)
			if d.Sign() == 0 {
				continue
			}
			x := new(big.Rat).Quo(subRat(lines[j].inter, lines[i].inter), d)
			if inA(x) {
				out = append(out, x)
			}
		}
	}
	return out
}

// maxOfMinLines computes max over a in [aLo,aHi] of (min of the given lines).
// Returns nil if the maximum is +inf.
func maxOfMinLines(aLo, aHi *big.Rat, lines []line) *big.Rat {
	if aHi == nil && minSlope(lines).Sign() > 0 {
		return nil
	}
	if aLo == nil && maxSlope(lines).Sign() < 0 {
		return nil
	}
	var best *big.Rat
	for _, x := range candidateA(aLo, aHi, lines) {
		v := evalMin(lines, x)
		if best == nil || v.Cmp(best) > 0 {
			best = v
		}
	}
	return best
}

// minOfMaxLines computes min over a in [aLo,aHi] of (max of the given lines).
// Returns nil if the minimum is -inf.
func minOfMaxLines(aLo, aHi *big.Rat, lines []line) *big.Rat {
	if aHi == nil && maxSlope(lines).Sign() < 0 {
		return nil
	}
	if aLo == nil && minSlope(lines).Sign() > 0 {
		return nil
	}
	var best *big.Rat
	for _, x := range candidateA(aLo, aHi, lines) {
		v := evalMax(lines, x)
		if best == nil || v.Cmp(best) < 0 {
			best = v
		}
	}
	return best
}

func evalMin(lines []line, x *big.Rat) *big.Rat {
	var best *big.Rat
	for _, l := range lines {
		v := l.at(x)
		if best == nil || v.Cmp(best) < 0 {
			best = v
		}
	}
	return best
}

func evalMax(lines []line, x *big.Rat) *big.Rat {
	var best *big.Rat
	for _, l := range lines {
		v := l.at(x)
		if best == nil || v.Cmp(best) > 0 {
			best = v
		}
	}
	return best
}

func minSlope(lines []line) *big.Rat {
	s := lines[0].slope
	for _, l := range lines[1:] {
		if l.slope.Cmp(s) < 0 {
			s = l.slope
		}
	}
	return s
}

func maxSlope(lines []line) *big.Rat {
	s := lines[0].slope
	for _, l := range lines[1:] {
		if l.slope.Cmp(s) > 0 {
			s = l.slope
		}
	}
	return s
}
