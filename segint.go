// Package segint computes the full intersection structure of a set of
// integer-coordinate line segments: every intersection point (with the set
// of segment IDs participating at that point) and every collinear overlap
// segment (with the exact set of segment IDs covering it).
//
// All computation uses exact rational arithmetic (math/big.Rat, which is
// always kept in normalized form), so reported coordinates are canonical
// rational numbers.
//
// Semantics:
//   - Endpoint touching counts as intersecting.
//   - A zero-length segment is treated as a point; it intersects any
//     segment containing that point.
//   - If several segments meet at one point, the point is reported once
//     with the full set of participating segment IDs.
//   - Collinear pairs overlapping in a positive-length interval produce
//     overlap segments. Overlaps on the same supporting line are merged
//     into maximal atomic pieces, each reported with the exact set of
//     segment IDs covering it. A collinear pair sharing only a single
//     point yields an intersection point, not an overlap.
package segint

import (
	"fmt"
	"math/big"
	"sort"
)

// Point is a 2D point with canonical rational coordinates.
type Point struct {
	X, Y *big.Rat
}

// NewPoint returns a Point with integer coordinates.
func NewPoint(x, y int64) Point {
	return Point{X: big.NewRat(x, 1), Y: big.NewRat(y, 1)}
}

func (p Point) String() string {
	return "(" + p.X.RatString() + "," + p.Y.RatString() + ")"
}

func pointEqual(a, b Point) bool {
	return a.X.Cmp(b.X) == 0 && a.Y.Cmp(b.Y) == 0
}

func pointKey(p Point) string {
	return p.X.String() + ";" + p.Y.String()
}

// Segment is a line segment between two integer-coordinate endpoints.
// ID identifies the segment in the reported results and must be unique
// within an input set.
type Segment struct {
	ID     int
	P1, P2 Point
}

// NewSegment builds a Segment from integer endpoint coordinates.
func NewSegment(id int, x1, y1, x2, y2 int64) Segment {
	return Segment{ID: id, P1: NewPoint(x1, y1), P2: NewPoint(x2, y2)}
}

// degenerate reports whether the segment has zero length.
func (s Segment) degenerate() bool { return pointEqual(s.P1, s.P2) }

// PointIntersection is a point where two or more segments meet.
type PointIntersection struct {
	Point      Point
	SegmentIDs []int // sorted, unique
}

// OverlapSegment is a maximal collinear overlap shared by two or more
// segments. Start/End are ordered canonically along the supporting line
// (by x, then y).
type OverlapSegment struct {
	Start, End Point
	SegmentIDs []int // sorted, unique; exactly the segments covering this piece
}

// Result is the full intersection structure of a segment set.
type Result struct {
	Points   []PointIntersection // sorted by (X, Y)
	Overlaps []OverlapSegment    // sorted by (Start.X, Start.Y, End.X, End.Y)
}

// Intersect computes the intersection structure of segs using a sweep
// line over x. It is equivalent to IntersectPairwise but only tests
// segment pairs whose x-ranges overlap.
func Intersect(segs []Segment) Result { return IntersectSweep(segs) }

// IntersectPairwise is the O(n^2) reference implementation: it tests
// every segment pair directly.
func IntersectPairwise(segs []Segment) Result {
	ac := newAccumulator()
	for i := 0; i < len(segs); i++ {
		for j := i + 1; j < len(segs); j++ {
			ac.addPair(segs[i], segs[j])
		}
	}
	return ac.result()
}

func sortPoints(pts []PointIntersection) {
	sort.Slice(pts, func(i, j int) bool {
		if c := pts[i].Point.X.Cmp(pts[j].Point.X); c != 0 {
			return c < 0
		}
		return pts[i].Point.Y.Cmp(pts[j].Point.Y) < 0
	})
}

func sortOverlaps(ovs []OverlapSegment) {
	sort.Slice(ovs, func(i, j int) bool {
		if c := ovs[i].Start.X.Cmp(ovs[j].Start.X); c != 0 {
			return c < 0
		}
		if c := ovs[i].Start.Y.Cmp(ovs[j].Start.Y); c != 0 {
			return c < 0
		}
		if c := ovs[i].End.X.Cmp(ovs[j].End.X); c != 0 {
			return c < 0
		}
		return ovs[i].End.Y.Cmp(ovs[j].End.Y) < 0
	})
}

func sortedIDs(set map[int]struct{}) []int {
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// String renders the result in a canonical, deterministic form.
func (r Result) String() string {
	s := "points:"
	for _, p := range r.Points {
		s += fmt.Sprintf(" %v%v", p.Point, p.SegmentIDs)
	}
	s += " overlaps:"
	for _, o := range r.Overlaps {
		s += fmt.Sprintf(" %v-%v%v", o.Start, o.End, o.SegmentIDs)
	}
	return s
}
