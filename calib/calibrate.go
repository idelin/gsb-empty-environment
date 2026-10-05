package calib

import "math/big"

// Calib is the calibration result for one channel.
//
// The feasible parameter set is
//
//	beta in [OffsetLo, OffsetHi] (integers; nil bound = unbounded)
//	rho  in [L(beta), U(beta)]   (rationals)
//
// where L and U are non-increasing in beta, so the overall drift range is
// [DriftLo, DriftHi] = [L(OffsetHi), U(OffsetLo)].
type Calib struct {
	Feasible bool

	OffsetLo *big.Int // nil = unbounded below
	OffsetHi *big.Int // nil = unbounded above
	DriftLo  *big.Rat // nil = unbounded below
	DriftHi  *big.Rat // nil = unbounded above

	// Incompatible lists the IDs of a minimal infeasible subset of
	// calibration points (irreducible inconsistent set), non-empty only
	// when Feasible is false.
	Incompatible []string
}

// offsetBounds accumulates the rational bounds on the integer offset beta
// implied by the calibration points. The constraints are exact:
//
//   - a point with Ref == 0 forces Lo <= beta <= Hi;
//   - two points with equal Ref must have overlapping intervals;
//   - for points i, j with 0 < Ti < Tj, feasibility of some rho requires
//     (Lo_i - beta)/Ti <= (Hi_j - beta)/Tj  and  (Lo_j - beta)/Tj <= (Hi_i - beta)/Ti,
//     i.e.
//     beta >= (Tj*Lo_i - Ti*Hi_j) / (Tj - Ti)
//     beta <= (Tj*Hi_i - Ti*Lo_j) / (Tj - Ti).
//
// ok is false when the rational bounds are already contradictory.
func offsetBounds(points []Point) (lo, hi *big.Rat, ok bool) {
	for _, p := range points {
		if p.Lo.Cmp(p.Hi) > 0 {
			return nil, nil, false
		}
		if p.Ref.Sign() < 0 {
			// Negative reference times are not supported: the pairwise
			// derivation assumes non-negative timestamps.
			return nil, nil, false
		}
		if p.Ref.Sign() == 0 {
			lo = maxRat(lo, p.Lo)
			hi = minRat(hi, p.Hi)
		}
	}
	for i := 0; i < len(points); i++ {
		for j := i + 1; j < len(points); j++ {
			a, b := points[i], points[j]
			c := a.Ref.Cmp(b.Ref)
			if c == 0 {
				if a.Hi.Cmp(b.Lo) < 0 || b.Hi.Cmp(a.Lo) < 0 {
					return nil, nil, false
				}
				continue
			}
			if a.Ref.Sign() == 0 || b.Ref.Sign() == 0 {
				continue
			}
			if c > 0 {
				a, b = b, a
			}
			d := new(big.Rat).Sub(b.Ref, a.Ref)

			lb := new(big.Rat).Mul(b.Ref, a.Lo)
			lb.Sub(lb, new(big.Rat).Mul(a.Ref, b.Hi))
			lb.Quo(lb, d)
			lo = maxRat(lo, lb)

			ub := new(big.Rat).Mul(b.Ref, a.Hi)
			ub.Sub(ub, new(big.Rat).Mul(a.Ref, b.Lo))
			ub.Quo(ub, d)
			hi = minRat(hi, ub)
		}
	}
	if lo != nil && hi != nil && lo.Cmp(hi) > 0 {
		return nil, nil, false
	}
	return lo, hi, true
}

// integerOffsetRange tightens the rational bounds to integers.
// ok is false when no integer offset survives.
func integerOffsetRange(points []Point) (lo, hi *big.Int, ok bool) {
	rlo, rhi, ok := offsetBounds(points)
	if !ok {
		return nil, nil, false
	}
	if rlo != nil {
		lo = ratCeil(rlo)
	}
	if rhi != nil {
		hi = ratFloor(rhi)
	}
	if lo != nil && hi != nil && lo.Cmp(hi) > 0 {
		return nil, nil, false
	}
	return lo, hi, true
}

// driftBounds returns the rational drift interval [L(beta), U(beta)] for a
// fixed integer offset. A nil bound means unbounded (no positive-Ref point
// constrains that side).
func driftBounds(points []Point, beta *big.Int) (lo, hi *big.Rat) {
	b := new(big.Rat).SetInt(beta)
	for _, p := range points {
		if p.Ref.Sign() <= 0 {
			continue
		}
		l := new(big.Rat).Quo(new(big.Rat).Sub(p.Lo, b), p.Ref)
		u := new(big.Rat).Quo(new(big.Rat).Sub(p.Hi, b), p.Ref)
		lo = maxRat(lo, l)
		hi = minRat(hi, u)
	}
	return lo, hi
}

func feasible(points []Point) bool {
	_, _, ok := integerOffsetRange(points)
	return ok
}

// Calibrate computes the feasible parameter range for one channel, or a
// minimal incompatible calibration point set when infeasible.
func Calibrate(points []Point) Calib {
	lo, hi, ok := integerOffsetRange(points)
	if !ok {
		return Calib{Feasible: false, Incompatible: incompatibleIDs(points)}
	}
	c := Calib{Feasible: true, OffsetLo: lo, OffsetHi: hi}

	hasPositiveRef := false
	for _, p := range points {
		if p.Ref.Sign() > 0 {
			hasPositiveRef = true
			break
		}
	}
	if !hasPositiveRef {
		return c // drift fully unconstrained
	}
	// L(beta) and U(beta) are non-increasing in beta, so the overall drift
	// range is [L(OffsetHi), U(OffsetLo)]; an unbounded offset side makes
	// the corresponding drift side unbounded.
	if hi != nil {
		c.DriftLo, _ = driftBounds(points, hi)
	}
	if lo != nil {
		_, c.DriftHi = driftBounds(points, lo)
	}
	return c
}

// incompatibleIDs extracts a minimal infeasible subset by deletion:
// repeatedly drop any point whose removal keeps the set infeasible.
func incompatibleIDs(points []Point) []string {
	keep := append([]Point(nil), points...)
	i := 0
	for i < len(keep) {
		trial := make([]Point, 0, len(keep)-1)
		trial = append(trial, keep[:i]...)
		trial = append(trial, keep[i+1:]...)
		if !feasible(trial) {
			keep = trial
		} else {
			i++
		}
	}
	ids := make([]string, len(keep))
	for i, p := range keep {
		ids[i] = p.ID
	}
	return ids
}
