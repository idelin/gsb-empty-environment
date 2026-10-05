// Package motion computes the exact set of time intervals during which two
// piecewise constant-velocity trajectories stay within a given squared
// distance of each other.
//
// Each trajectory is a sequence of segments. A segment is valid on the
// half-open time range [Start, End): at a breakpoint the position may jump
// discontinuously to the next segment's Pos. Because segments are half-open,
// the position at a breakpoint instant is always determined by the segment
// that starts there, never by the one that ends there.
//
// Within every elementary overlap of one segment from each trajectory the
// squared relative distance is a quadratic f(s) = a*s^2 + b*s + c in the
// local time s. Intersect solves the exact quadratic inequality
// f(s) <= maxDistSq on every overlap (it does NOT merely sample segment
// endpoints) and merges the results across breakpoints.
package motion

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Vec is a position or velocity in any fixed dimension >= 1.
type Vec []float64

// Segment is one piece of a piecewise constant-velocity trajectory.
//
// On [Start, End) the position is Pos + Vel*(t-Start). At t == End this
// segment no longer applies; the next segment (if any) takes over, so the
// trajectory may jump at End.
type Segment struct {
	Start, End float64
	Pos, Vel   Vec
}

// Trajectory is an ordered, non-overlapping list of segments. Gaps between
// segments are allowed; the trajectory is undefined there.
type Trajectory struct {
	Segments []Segment
}

// Interval is one maximal connected time set on which the squared distance
// is <= the threshold.
//
// Start is always inclusive. End is inclusive iff EndClosed. A grazing
// contact that touches the threshold at a single instant is reported as a
// point interval with Start == End and EndClosed == true.
type Interval struct {
	Start, End float64
	EndClosed  bool
}

// Contains reports whether t belongs to the interval.
func (iv Interval) Contains(t float64) bool {
	if t < iv.Start || t > iv.End {
		return false
	}
	if t == iv.End && !iv.EndClosed {
		return false
	}
	return true
}

// Validate checks that the segments are well formed: positive duration,
// ordered, non-overlapping, consistent dimension, no NaN/Inf times.
func (tr Trajectory) Validate() error {
	dim := -1
	prevEnd := math.Inf(-1)
	for i, s := range tr.Segments {
		if math.IsNaN(s.Start) || math.IsNaN(s.End) ||
			math.IsInf(s.Start, 0) || math.IsInf(s.End, 0) {
			return fmt.Errorf("segment %d: non-finite time bounds", i)
		}
		if !(s.End > s.Start) {
			return fmt.Errorf("segment %d: End (%v) must be greater than Start (%v)", i, s.End, s.Start)
		}
		if len(s.Pos) == 0 {
			return fmt.Errorf("segment %d: empty position vector", i)
		}
		if len(s.Pos) != len(s.Vel) {
			return fmt.Errorf("segment %d: position/velocity dimension mismatch (%d vs %d)", i, len(s.Pos), len(s.Vel))
		}
		if dim < 0 {
			dim = len(s.Pos)
		} else if len(s.Pos) != dim {
			return fmt.Errorf("segment %d: inconsistent dimension %d, want %d", i, len(s.Pos), dim)
		}
		if s.Start < prevEnd {
			return fmt.Errorf("segment %d: overlaps previous segment", i)
		}
		prevEnd = s.End
	}
	return nil
}

// At returns the trajectory position at time t. The second result is false
// when t lies outside every segment (a coverage gap or beyond the ends).
func (tr Trajectory) At(t float64) (Vec, bool) {
	i := sort.Search(len(tr.Segments), func(i int) bool { return tr.Segments[i].End > t })
	if i >= len(tr.Segments) || t < tr.Segments[i].Start {
		return nil, false
	}
	s := tr.Segments[i]
	p := make(Vec, len(s.Pos))
	for d := range p {
		p[d] = s.Pos[d] + s.Vel[d]*(t-s.Start)
	}
	return p, true
}

// Intersect returns the sorted, merged, non-adjacent list of intervals on
// which |a(t) - b(t)|^2 <= maxDistSq. Times where either trajectory is
// undefined never belong to the result.
func Intersect(a, b Trajectory, maxDistSq float64) ([]Interval, error) {
	if err := a.Validate(); err != nil {
		return nil, fmt.Errorf("trajectory a: %v", err)
	}
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("trajectory b: %v", err)
	}
	if math.IsNaN(maxDistSq) || maxDistSq < 0 {
		return nil, errors.New("maxDistSq must be a non-negative number")
	}
	if len(a.Segments) == 0 || len(b.Segments) == 0 {
		return nil, nil
	}
	if len(a.Segments[0].Pos) != len(b.Segments[0].Pos) {
		return nil, fmt.Errorf("dimension mismatch: %d vs %d",
			len(a.Segments[0].Pos), len(b.Segments[0].Pos))
	}

	var out []Interval
	i, j := 0, 0
	sa, sb := a.Segments, b.Segments
	for i < len(sa) && j < len(sb) {
		lo := math.Max(sa[i].Start, sb[j].Start)
		hi := math.Min(sa[i].End, sb[j].End)
		if lo < hi {
			if iv, ok := solvePiece(sa[i], sb[j], lo, hi, maxDistSq); ok {
				out = pushInterval(out, iv)
			}
		}
		switch {
		case sa[i].End < sb[j].End:
			i++
		case sb[j].End < sa[i].End:
			j++
		default:
			i++
			j++
		}
	}
	return out, nil
}

// pushInterval appends iv to out, merging it with the previous interval when
// their union is contiguous. Every piece solution is closed at its start, so
// the union is contiguous exactly when iv.Start <= last.End: if iv.Start ==
// last.End the shared instant is covered by iv regardless of whether the
// previous interval was closed there.
func pushInterval(out []Interval, iv Interval) []Interval {
	if n := len(out); n > 0 {
		last := &out[n-1]
		if iv.Start <= last.End {
			if iv.End > last.End || (iv.End == last.End && iv.EndClosed) {
				last.End = iv.End
				last.EndClosed = iv.EndClosed
			}
			return out
		}
	}
	return append(out, iv)
}
