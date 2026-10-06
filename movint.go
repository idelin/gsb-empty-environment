// Package movint computes, exactly, the closed time intervals during which
// pairs of linearly moving 1-D closed intervals intersect.
//
// Each interval has two endpoints whose positions are affine functions of
// time with integer velocities: p(t) = P + V*t. Endpoints may cross; the
// interval at time t is always [min(p0,p1), max(p0,p1)]. Time is restricted
// to a rational domain [0, T]. All arithmetic is exact (math/big.Rat), and
// instantaneous contacts (touching at a single time) are preserved as
// degenerate closed intervals [t, t].
//
// Method: intervals A and B intersect at time t iff
//
//	hiA(t) >= loB(t)  and  hiB(t) >= loA(t).
//
// hiA - loB is a convex piecewise-linear function (max of linears minus min
// of linears) whose only breakpoints are the endpoint-crossing times of A
// and B. Evaluating it at the sorted breakpoints and solving one linear
// inequality per segment yields the exact closed set {hiA >= loB}; likewise
// for {hiB >= loA}. Intersecting the two closed sets gives the answer: at
// most three disjoint closed intervals inside [0, T].
package movint

import (
	"math/big"
	"sort"
)

// MovingInterval is a closed 1-D interval whose two endpoints move linearly.
// Endpoint positions at time t are P0+V0*t and P1+V1*t; the interval spans
// [min, max] of the two, so endpoint order may flip over time.
type MovingInterval struct {
	P0, V0 int64 // endpoint 0: position P0 + V0*t
	P1, V1 int64 // endpoint 1: position P1 + V1*t
}

// TimeInterval is a closed time interval [Lo, Hi] with Lo <= Hi.
// Lo == Hi represents an instantaneous contact at a single time.
type TimeInterval struct {
	Lo, Hi *big.Rat
}

// endpoint returns the position P + V*t as an exact rational.
func endpoint(p, v int64, t *big.Rat) *big.Rat {
	r := new(big.Rat).SetInt64(v)
	r.Mul(r, t)
	r.Add(r, big.NewRat(p, 1))
	return r
}

// Lo returns the interval's left endpoint position at time t.
func (m MovingInterval) Lo(t *big.Rat) *big.Rat {
	a := endpoint(m.P0, m.V0, t)
	b := endpoint(m.P1, m.V1, t)
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// Hi returns the interval's right endpoint position at time t.
func (m MovingInterval) Hi(t *big.Rat) *big.Rat {
	a := endpoint(m.P0, m.V0, t)
	b := endpoint(m.P1, m.V1, t)
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// IntersectsAt reports whether m and o intersect (or touch) at time t.
func (m MovingInterval) IntersectsAt(o MovingInterval, t *big.Rat) bool {
	return m.Lo(t).Cmp(o.Hi(t)) <= 0 && o.Lo(t).Cmp(m.Hi(t)) <= 0
}

// crossing returns the time at which the lines p1+v1*t and p2+v2*t meet.
// ok is false when they are parallel (no single crossing time).
func crossing(p1, v1, p2, v2 int64) (t *big.Rat, ok bool) {
	if v1 == v2 {
		return nil, false
	}
	return big.NewRat(p2-p1, v1-v2), true
}

// IntersectionTimes returns the sorted, disjoint closed time intervals
// within [0, T] during which a and b intersect. Instantaneous contacts are
// included as degenerate intervals [t, t]. The result contains at most
// three intervals. T must be non-negative.
func IntersectionTimes(a, b MovingInterval, T *big.Rat) []TimeInterval {
	if T.Sign() < 0 {
		return nil
	}
	zero := new(big.Rat)

	// Breakpoints: 0, T, and the endpoint-crossing times of each interval
	// (the only points where lo/hi change their linear branch).
	breaks := []*big.Rat{new(big.Rat), new(big.Rat).Set(T)}
	if t, ok := crossing(a.P0, a.V0, a.P1, a.V1); ok && inOpen(t, zero, T) {
		breaks = append(breaks, t)
	}
	if t, ok := crossing(b.P0, b.V0, b.P1, b.V1); ok && inOpen(t, zero, T) {
		breaks = append(breaks, t)
	}
	sort.Slice(breaks, func(i, j int) bool { return breaks[i].Cmp(breaks[j]) < 0 })
	breaks = dedup(breaks)

	// f(t) = hiA(t) - loB(t) >= 0  and  g(t) = hiB(t) - loA(t) >= 0.
	fvals := make([]*big.Rat, len(breaks))
	gvals := make([]*big.Rat, len(breaks))
	for i, t := range breaks {
		fvals[i] = new(big.Rat).Sub(a.Hi(t), b.Lo(t))
		gvals[i] = new(big.Rat).Sub(b.Hi(t), a.Lo(t))
	}

	return intersectLists(nonnegSet(breaks, fvals), nonnegSet(breaks, gvals))
}

// PairResult holds the intersection time intervals for one (A, B) pair.
type PairResult struct {
	A, B  int            // indices into the input slices
	Times []TimeInterval // sorted disjoint closed intervals, possibly empty
}

// IntersectAll computes IntersectionTimes for every pair (as[i], bs[j]).
func IntersectAll(as, bs []MovingInterval, T *big.Rat) []PairResult {
	out := make([]PairResult, 0, len(as)*len(bs))
	for i, a := range as {
		for j, b := range bs {
			out = append(out, PairResult{A: i, B: j, Times: IntersectionTimes(a, b, T)})
		}
	}
	return out
}

// inOpen reports whether l < t < r.
func inOpen(t, l, r *big.Rat) bool {
	return t.Cmp(l) > 0 && t.Cmp(r) < 0
}

// dedup removes adjacent duplicates from a sorted slice of rationals.
func dedup(ts []*big.Rat) []*big.Rat {
	out := ts[:0]
	for _, t := range ts {
		if len(out) == 0 || out[len(out)-1].Cmp(t) != 0 {
			out = append(out, t)
		}
	}
	return out
}

// rootOf returns the exact root of the linear function passing through
// (l, fl) and (r, fr), where fl and fr have opposite signs (or one is zero).
func rootOf(l, fl, r, fr *big.Rat) *big.Rat {
	// root = l - fl * (r - l) / (fr - fl)
	num := new(big.Rat).Sub(r, l)
	num.Mul(num, fl)
	den := new(big.Rat).Sub(fr, fl)
	num.Quo(num, den)
	return new(big.Rat).Sub(l, num)
}

// nonnegSet returns the closed set {t in [ts[0], ts[last]] : v(t) >= 0},
// where v is the piecewise-linear function through (ts[i], vs[i]). Roots are
// computed exactly, so boundary touch points are included.
func nonnegSet(ts, vs []*big.Rat) []TimeInterval {
	if len(ts) == 1 { // degenerate domain [T, T] with T == 0
		if vs[0].Sign() >= 0 {
			return []TimeInterval{{Lo: ts[0], Hi: ts[0]}}
		}
		return nil
	}
	var out []TimeInterval
	for i := 0; i+1 < len(ts); i++ {
		l, r := ts[i], ts[i+1]
		sl, sr := vs[i].Sign(), vs[i+1].Sign()
		switch {
		case sl >= 0 && sr >= 0:
			out = append(out, TimeInterval{Lo: l, Hi: r})
		case sl < 0 && sr < 0:
			// no intersection on this segment
		case sl < 0 && sr >= 0:
			out = append(out, TimeInterval{Lo: rootOf(l, vs[i], r, vs[i+1]), Hi: r})
		default: // sl >= 0 && sr < 0
			out = append(out, TimeInterval{Lo: l, Hi: rootOf(l, vs[i], r, vs[i+1])})
		}
	}
	return mergeTouching(out)
}

// mergeTouching merges adjacent closed intervals that share an endpoint.
func mergeTouching(in []TimeInterval) []TimeInterval {
	var out []TimeInterval
	for _, iv := range in {
		if n := len(out); n > 0 && out[n-1].Hi.Cmp(iv.Lo) >= 0 {
			if out[n-1].Hi.Cmp(iv.Hi) < 0 {
				out[n-1].Hi = iv.Hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// intersectLists intersects two sorted lists of disjoint closed intervals.
// Touching endpoints are kept (degenerate [t, t] results).
func intersectLists(x, y []TimeInterval) []TimeInterval {
	var out []TimeInterval
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		lo := x[i].Lo
		if y[j].Lo.Cmp(lo) > 0 {
			lo = y[j].Lo
		}
		hi := x[i].Hi
		if y[j].Hi.Cmp(hi) < 0 {
			hi = y[j].Hi
		}
		if lo.Cmp(hi) <= 0 {
			out = append(out, TimeInterval{Lo: lo, Hi: hi})
		}
		if x[i].Hi.Cmp(y[j].Hi) < 0 {
			i++
		} else {
			j++
		}
	}
	return out
}
