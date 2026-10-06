// Package intervalmotion computes the exact closed time intervals during
// which pairs of moving one-dimensional closed intervals intersect.
//
// Each interval has two endpoints that move linearly in time with rational
// (typically integer) velocities. At any rational time t in [0, T], an
// interval is [min(e1, e2), max(e1, e2)] of its two endpoint positions, so
// endpoints are allowed to cross. All arithmetic is exact (math/big.Rat);
// instantaneous contacts are preserved as degenerate intervals [t, t].
//
// Method: the relative order of the four endpoints of a pair can only change
// when two endpoint lines cross. Between consecutive crossing times the
// intersection condition reduces to two linear inequalities with constant
// truth values, so the feasible set is a union of closed intervals found by
// evaluating one sample per open segment plus every critical time.
package intervalmotion

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// MovingInterval is a closed interval whose two endpoints move linearly:
// endpoint k is at position Pk + Vk*t at time t.
type MovingInterval struct {
	P1, V1, P2, V2 *big.Rat
}

// NewMovingInterval builds a MovingInterval from integer positions and
// velocities.
func NewMovingInterval(p1, v1, p2, v2 int64) MovingInterval {
	return MovingInterval{
		P1: big.NewRat(p1, 1), V1: big.NewRat(v1, 1),
		P2: big.NewRat(p2, 1), V2: big.NewRat(v2, 1),
	}
}

// NewMovingIntervalRat builds a MovingInterval from rational positions and
// velocities. The arguments are copied.
func NewMovingIntervalRat(p1, v1, p2, v2 *big.Rat) MovingInterval {
	return MovingInterval{
		P1: new(big.Rat).Set(p1), V1: new(big.Rat).Set(v1),
		P2: new(big.Rat).Set(p2), V2: new(big.Rat).Set(v2),
	}
}

// BoundsAt returns the (lo, hi) bounds of the interval at time t.
func (m MovingInterval) BoundsAt(t *big.Rat) (lo, hi *big.Rat) {
	e1 := new(big.Rat).Add(new(big.Rat).Mul(m.V1, t), m.P1)
	e2 := new(big.Rat).Add(new(big.Rat).Mul(m.V2, t), m.P2)
	if e1.Cmp(e2) <= 0 {
		return e1, e2
	}
	return e2, e1
}

// IntersectsAt reports whether m and o intersect (as closed intervals) at
// time t.
func (m MovingInterval) IntersectsAt(o MovingInterval, t *big.Rat) bool {
	alo, ahi := m.BoundsAt(t)
	blo, bhi := o.BoundsAt(t)
	return alo.Cmp(bhi) <= 0 && blo.Cmp(ahi) <= 0
}

// TimeInterval is a closed interval [Lo, Hi] of rational times. Lo == Hi
// represents an instantaneous contact.
type TimeInterval struct {
	Lo, Hi *big.Rat
}

func (ti TimeInterval) String() string {
	return "[" + ti.Lo.RatString() + ", " + ti.Hi.RatString() + "]"
}

// IntersectionTimes returns the sorted, disjoint, merged closed time
// intervals within [0, T] during which a and b intersect. It returns nil if
// they never intersect or T is negative.
func IntersectionTimes(a, b MovingInterval, T *big.Rat) []TimeInterval {
	zero := big.NewRat(0, 1)
	if T.Sign() < 0 {
		return nil
	}

	// Critical times: 0, T, and every t in (0, T) where two endpoint lines
	// cross. Between consecutive critical times the endpoint order is fixed,
	// so lo/hi of each interval are single lines and the two intersection
	// inequalities are strictly true or strictly false throughout the open
	// segment (a root inside would itself be a crossing).
	lines := [][2]*big.Rat{
		{a.P1, a.V1}, {a.P2, a.V2}, {b.P1, b.V1}, {b.P2, b.V2},
	}
	crit := []*big.Rat{zero, new(big.Rat).Set(T)}
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			dv := new(big.Rat).Sub(lines[i][1], lines[j][1])
			if dv.Sign() == 0 {
				continue
			}
			dp := new(big.Rat).Sub(lines[i][0], lines[j][0])
			t := new(big.Rat).Neg(new(big.Rat).Quo(dp, dv))
			if t.Cmp(zero) > 0 && t.Cmp(T) < 0 {
				crit = append(crit, t)
			}
		}
	}
	sort.Slice(crit, func(i, j int) bool { return crit[i].Cmp(crit[j]) < 0 })
	uniq := crit[:0]
	for _, t := range crit {
		if len(uniq) == 0 || t.Cmp(uniq[len(uniq)-1]) != 0 {
			uniq = append(uniq, t)
		}
	}

	var result []TimeInterval
	addPiece := func(lo, hi *big.Rat) {
		if n := len(result); n > 0 && lo.Cmp(result[n-1].Hi) <= 0 {
			// Feasible pieces are contiguous in time; touching closed
			// pieces merge into one closed interval.
			result[n-1].Hi = hi
			return
		}
		result = append(result, TimeInterval{lo, hi})
	}

	for i, t := range uniq {
		if a.IntersectsAt(b, t) {
			addPiece(t, t)
		}
		if i+1 < len(uniq) {
			mid := new(big.Rat).Add(uniq[i], uniq[i+1])
			mid.Quo(mid, big.NewRat(2, 1))
			if a.IntersectsAt(b, mid) {
				addPiece(uniq[i], uniq[i+1])
			}
		}
	}
	return result
}

// PairResult holds the intersection times for one pair of intervals.
type PairResult struct {
	I, J  int // indices into the first and second input slices
	Times []TimeInterval
}

// AllPairsIntersectionTimes computes IntersectionTimes for every pair
// (as[i], bs[j]).
func AllPairsIntersectionTimes(as, bs []MovingInterval, T *big.Rat) []PairResult {
	out := make([]PairResult, 0, len(as)*len(bs))
	for i := range as {
		for j := range bs {
			out = append(out, PairResult{
				I:     i,
				J:     j,
				Times: IntersectionTimes(as[i], bs[j], T),
			})
		}
	}
	return out
}

// VerifyIntersectionTimes cross-checks the output of IntersectionTimes by
// sampling exact rational points: every reported interval is probed at its
// endpoints and at samplesPerRegion interior points (all must intersect),
// and every gap around and between reported intervals is probed at
// samplesPerRegion points (none may intersect). It returns a descriptive
// error on the first violation.
//
// Because the feasible set is a union of closed intervals whose boundaries
// are endpoint-crossing times, dense enough sampling of the open regions is
// a sound complement to the piecewise-linear argument used by
// IntersectionTimes.
func VerifyIntersectionTimes(a, b MovingInterval, T *big.Rat, got []TimeInterval, samplesPerRegion int) error {
	if samplesPerRegion < 1 {
		return errors.New("intervalmotion: samplesPerRegion must be >= 1")
	}
	zero := big.NewRat(0, 1)

	// Structural checks: inside [0, T], sorted, and pairwise disjoint with
	// a strict gap (touching intervals should have been merged).
	prevHi := (*big.Rat)(nil)
	for k, ti := range got {
		if ti.Lo.Cmp(zero) < 0 || ti.Hi.Cmp(T) > 0 || ti.Lo.Cmp(ti.Hi) > 0 {
			return fmt.Errorf("interval %d %s is not a valid closed subinterval of [0, %s]", k, ti, T.RatString())
		}
		if prevHi != nil && prevHi.Cmp(ti.Lo) >= 0 {
			return fmt.Errorf("intervals %d and %d overlap or touch; expected merged disjoint output", k-1, k)
		}
		prevHi = ti.Hi
	}

	// Interior sampling: points strictly inside each reported interval must
	// be intersection times, as must the (closed) endpoints.
	for k, ti := range got {
		if !a.IntersectsAt(b, ti.Lo) || !a.IntersectsAt(b, ti.Hi) {
			return fmt.Errorf("interval %d %s has an endpoint where the intervals do not intersect", k, ti)
		}
		for s := 1; s <= samplesPerRegion; s++ {
			t := innerPoint(ti.Lo, ti.Hi, s, samplesPerRegion+1)
			if t.Cmp(ti.Lo) <= 0 || t.Cmp(ti.Hi) >= 0 {
				continue // degenerate interval: no interior
			}
			if !a.IntersectsAt(b, t) {
				return fmt.Errorf("interval %d %s contains non-intersection time %s", k, ti, t.RatString())
			}
		}
	}

	// Exterior sampling: points strictly inside each gap must not intersect.
	type gap struct{ lo, hi *big.Rat }
	var gaps []gap
	if len(got) == 0 {
		gaps = append(gaps, gap{zero, T})
	} else {
		gaps = append(gaps, gap{zero, got[0].Lo})
		for k := 1; k < len(got); k++ {
			gaps = append(gaps, gap{got[k-1].Hi, got[k].Lo})
		}
		gaps = append(gaps, gap{got[len(got)-1].Hi, T})
	}
	for k, g := range gaps {
		for s := 1; s <= samplesPerRegion; s++ {
			t := innerPoint(g.lo, g.hi, s, samplesPerRegion+1)
			if t.Cmp(g.lo) <= 0 || t.Cmp(g.hi) >= 0 {
				continue
			}
			if a.IntersectsAt(b, t) {
				return fmt.Errorf("gap %d (%s, %s) contains intersection time %s", k, g.lo.RatString(), g.hi.RatString(), t.RatString())
			}
		}
	}
	return nil
}

// innerPoint returns lo + (hi-lo)*num/den.
func innerPoint(lo, hi *big.Rat, num, den int) *big.Rat {
	t := new(big.Rat).Sub(hi, lo)
	t.Mul(t, big.NewRat(int64(num), int64(den)))
	return t.Add(t, lo)
}
