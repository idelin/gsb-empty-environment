package timemap

import (
	"errors"
	"fmt"
	"sort"
)

// Point is a breakpoint of the piecewise-linear time mapping: original time X
// maps to compressed time Y.
type Point struct {
	X Rat
	Y Rat
}

// Mapping is a strictly increasing piecewise-linear map shared by every
// compressed segment and every synchronized track. Strict monotonicity in both
// X and Y guarantees that event order and simultaneity are preserved and that
// the map is invertible over its whole range.
type Mapping struct {
	pts []Point
}

// NewMapping validates breakpoints and builds a Mapping. X and Y must both be
// strictly increasing; at least two points are required.
func NewMapping(pts []Point) (*Mapping, error) {
	if len(pts) < 2 {
		return nil, errors.New("mapping needs at least two breakpoints")
	}
	for i, p := range pts {
		if !p.X.valid() || !p.Y.valid() {
			return nil, fmt.Errorf("breakpoint %d has nil coordinate", i)
		}
		if i == 0 {
			continue
		}
		if pts[i-1].X.Cmp(p.X) >= 0 {
			return nil, fmt.Errorf("breakpoint %d: X not strictly increasing (%s >= %s)", i, pts[i-1].X, p.X)
		}
		if pts[i-1].Y.Cmp(p.Y) >= 0 {
			return nil, fmt.Errorf("breakpoint %d: Y not strictly increasing (%s >= %s)", i, pts[i-1].Y, p.Y)
		}
	}
	cp := make([]Point, len(pts))
	copy(cp, pts)
	return &Mapping{pts: cp}, nil
}

// Points returns a copy of the breakpoints.
func (m *Mapping) Points() []Point {
	cp := make([]Point, len(m.pts))
	copy(cp, m.pts)
	return cp
}

// Domain returns the original-time interval covered by the breakpoints.
// Outside the domain the map is extrapolated with the boundary slope.
func (m *Mapping) Domain() (lo, hi Rat) {
	return m.pts[0].X, m.pts[len(m.pts)-1].X
}

// Range returns the compressed-time interval covered by the breakpoints.
func (m *Mapping) Range() (lo, hi Rat) {
	return m.pts[0].Y, m.pts[len(m.pts)-1].Y
}

// segment locates the piece used to evaluate x: the greatest i such that
// pts[i].X <= x, clamped to [0, len-2] so extrapolation reuses boundary slopes.
func (m *Mapping) segment(x Rat) int {
	n := len(m.pts)
	i := sort.Search(n-1, func(i int) bool { return m.pts[i+1].X.Cmp(x) > 0 })
	if i > n-2 {
		i = n - 2
	}
	return i
}

// inverseSegment mirrors segment for the Y axis.
func (m *Mapping) inverseSegment(y Rat) int {
	n := len(m.pts)
	i := sort.Search(n-1, func(i int) bool { return m.pts[i+1].Y.Cmp(y) > 0 })
	if i > n-2 {
		i = n - 2
	}
	return i
}

func slope(a, b Point) Rat {
	return b.Y.Sub(a.Y).Div(b.X.Sub(a.X))
}

// Slope returns the compression slope of the piece containing original time x.
// A slope of 1 means the piece is uncompressed; smaller values mean stronger
// compression.
func (m *Mapping) Slope(x Rat) Rat {
	i := m.segment(x)
	return slope(m.pts[i], m.pts[i+1])
}

// Forward maps an original time to compressed time.
func (m *Mapping) Forward(x Rat) Rat {
	i := m.segment(x)
	a, b := m.pts[i], m.pts[i+1]
	return a.Y.Add(x.Sub(a.X).Mul(slope(a, b)))
}

// Inverse maps a compressed time back to the original time. Because the map is
// strictly increasing, the inverse is a single exact point.
func (m *Mapping) Inverse(y Rat) Rat {
	i := m.inverseSegment(y)
	a, b := m.pts[i], m.pts[i+1]
	return a.X.Add(y.Sub(a.Y).Div(slope(a, b)))
}

// Interval is a half-open time interval [Start, End).
type Interval struct {
	ID    string
	Kind  string
	Start Rat
	End   Rat
}

// Contains reports whether t is inside [Start, End).
func (iv Interval) Contains(t Rat) bool {
	return iv.Start.Cmp(t) <= 0 && t.Cmp(iv.End) < 0
}

// Duration returns End-Start.
func (iv Interval) Duration() Rat { return iv.End.Sub(iv.Start) }

// Overlaps reports whether two intervals share a non-empty interior.
func (iv Interval) Overlaps(o Interval) bool {
	return iv.Start.Cmp(o.End) < 0 && o.Start.Cmp(iv.End) < 0
}
