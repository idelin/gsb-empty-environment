// Package motion computes the exact set of time intervals during which the
// squared distance between two piecewise constant-velocity trajectories
// does not exceed a given threshold.
//
// Each trajectory is a sequence of segments. A segment covers the half-open
// interval [T0, T1): inside it the position is P0 + V*(t-T0), and at T1 the
// position may jump discontinuously to the next segment's P0. Because
// segments are half-open, the value of a trajectory exactly at a breakpoint
// is defined by the segment that starts there.
//
// The coverage set is computed by solving, for every pair of overlapping
// segments, the exact quadratic inequality |pa(t)-pb(t)|^2 <= threshold on
// their overlap, and then merging the results across breakpoints with the
// correct open/closed semantics. It is never enough to sample segment
// endpoints: the squared distance is quadratic in t, so its minimum may lie
// strictly inside a segment pair.
package motion

import (
	"errors"
	"fmt"
	"math"
)

// Vec is a position or velocity vector of arbitrary (consistent) dimension.
type Vec []float64

// Segment is one piece of a trajectory. Over the half-open interval
// [T0, T1) the position is P0 + V*(t-T0).
type Segment struct {
	T0, T1 float64
	P0, V  Vec
}

// At returns the position at time t. Only meaningful for T0 <= t < T1.
func (s Segment) At(t float64) Vec {
	p := make(Vec, len(s.P0))
	dt := t - s.T0
	for k := range p {
		p[k] = s.P0[k] + s.V[k]*dt
	}
	return p
}

// Trajectory is a sequence of segments with disjoint, non-decreasing
// half-open time intervals. Gaps (times where the trajectory is undefined)
// are allowed.
type Trajectory []Segment

// Span is one maximal interval of the coverage set. An endpoint is
// inclusive exactly when its Closed flag is set. An endpoint that is a root
// of the pairwise squared-distance quadratic (rather than a trajectory
// breakpoint) carries a *QuadRoot for exact refinement and comparison.
// A zero-width span [t, t] with both ends closed represents an isolated
// instant of coverage (a grazing/tangency point).
type Span struct {
	Start, End             float64
	StartClosed, EndClosed bool
	StartRoot, EndRoot     *QuadRoot
}

// Contains reports whether time t is covered by spans, which must be the
// sorted, merged output of Coverage.
func Contains(spans []Span, t float64) bool {
	for _, s := range spans {
		if t < s.Start {
			return false
		}
		if t > s.End {
			continue
		}
		if t == s.Start && !s.StartClosed {
			return false
		}
		if t == s.End && !s.EndClosed {
			return false
		}
		return true
	}
	return false
}

// Validate checks that a trajectory is well formed: finite times, T0 < T1,
// non-overlapping ordered segments, and consistent vector dimensions.
func Validate(tr Trajectory) error {
	dim := -1
	var prevT1 float64
	for i, s := range tr {
		if math.IsNaN(s.T0) || math.IsNaN(s.T1) || math.IsInf(s.T0, 0) || math.IsInf(s.T1, 0) {
			return fmt.Errorf("segment %d: non-finite time", i)
		}
		if !(s.T0 < s.T1) {
			return fmt.Errorf("segment %d: empty interval [%v, %v)", i, s.T0, s.T1)
		}
		if i > 0 && s.T0 < prevT1 {
			return fmt.Errorf("segment %d: overlaps previous segment", i)
		}
		prevT1 = s.T1
		if len(s.P0) == 0 || len(s.P0) != len(s.V) {
			return fmt.Errorf("segment %d: dimension mismatch between P0 and V", i)
		}
		if dim < 0 {
			dim = len(s.P0)
		} else if len(s.P0) != dim {
			return fmt.Errorf("segment %d: inconsistent dimension", i)
		}
		for _, x := range s.P0 {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return fmt.Errorf("segment %d: non-finite position", i)
			}
		}
		for _, x := range s.V {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return fmt.Errorf("segment %d: non-finite velocity", i)
			}
		}
	}
	return nil
}

// Coverage returns the sorted, merged set of maximal time intervals on which
// both trajectories are defined and |a(t)-b(t)|^2 <= maxDistSq.
//
// Endpoints that fall on trajectory breakpoints obey half-open semantics:
// the instant of a breakpoint belongs to the segment starting there, so a
// span ending at a breakpoint is open unless the following pair also
// satisfies the condition at that exact instant.
func Coverage(a, b Trajectory, maxDistSq float64) ([]Span, error) {
	if err := Validate(a); err != nil {
		return nil, fmt.Errorf("trajectory a: %v", err)
	}
	if err := Validate(b); err != nil {
		return nil, fmt.Errorf("trajectory b: %v", err)
	}
	if len(a) > 0 && len(b) > 0 && len(a[0].P0) != len(b[0].P0) {
		return nil, errors.New("trajectory dimensions differ")
	}
	if math.IsNaN(maxDistSq) || maxDistSq < 0 {
		return nil, errors.New("threshold must be a non-negative number")
	}

	var spans []Span
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		sa, sb := a[i], b[j]
		lo := math.Max(sa.T0, sb.T0)
		hi := math.Min(sa.T1, sb.T1)
		if lo < hi {
			au, bu, c0 := pairCoeffs(sa, sb, lo, maxDistSq)
			for _, s := range solvePair(au, bu, c0, lo, hi) {
				spans = appendSpan(spans, s)
			}
		}
		switch {
		case sa.T1 < sb.T1:
			i++
		case sb.T1 < sa.T1:
			j++
		default:
			i++
			j++
		}
	}
	return spans, nil
}

// pairCoeffs returns the coefficients of f(u) = au*u^2 + bu*u + c0, the
// squared distance minus threshold, in local time u = t - lo over the
// overlap [lo, hi) of segments sa and sb.
func pairCoeffs(sa, sb Segment, lo, maxDistSq float64) (au, bu, c0 float64) {
	dim := len(sa.P0)
	da := lo - sa.T0
	db := lo - sb.T0
	var dp, dv float64
	for k := 0; k < dim; k++ {
		d := (sa.P0[k] + sa.V[k]*da) - (sb.P0[k] + sb.V[k]*db)
		w := sa.V[k] - sb.V[k]
		dp += d * d
		dv += d * w
		au += w * w
	}
	bu = 2 * dv
	c0 = dp - maxDistSq
	return au, bu, c0
}

// appendSpan appends s to spans, merging it with the previous span when they
// share an endpoint instant and at least one of them includes that instant
// (so their union is a single connected interval).
func appendSpan(spans []Span, s Span) []Span {
	if len(spans) > 0 {
		last := &spans[len(spans)-1]
		if last.End == s.Start && (last.EndClosed || s.StartClosed) {
			last.End = s.End
			last.EndClosed = s.EndClosed
			last.EndRoot = s.EndRoot
			return spans
		}
	}
	return append(spans, s)
}
