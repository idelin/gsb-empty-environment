// Package segsweep computes the full set of intersections of a set of
// integer-coordinate line segments: every intersection point (with the
// set of participating segment IDs) and every collinear overlap portion.
//
// All output coordinates are canonical rational numbers (math/big.Rat
// stores reduced fractions), so results are exact.
//
// Conventions:
//   - Endpoint touching counts as intersecting.
//   - A point where k segments meet is reported once, with all k IDs.
//   - Collinear segments that share a positive-length portion produce an
//     Overlap (maximal portions with a constant participant set);
//     collinear segments that only touch at a single point produce an
//     IntersectionPoint instead.
//   - Zero-length segments are supported and behave as points.
package segsweep

import "math/big"

// Point is an exact rational point.
type Point struct {
	X, Y *big.Rat
}

// IntPoint builds a Point with integer coordinates.
func IntPoint(x, y int64) Point { return Point{big.NewRat(x, 1), big.NewRat(y, 1)} }

// Cmp orders points lexicographically by (X, Y).
func (p Point) Cmp(q Point) int {
	if c := p.X.Cmp(q.X); c != 0 {
		return c
	}
	return p.Y.Cmp(q.Y)
}

func (p Point) String() string { return "(" + p.X.RatString() + "," + p.Y.RatString() + ")" }

func pointKey(p Point) string { return p.X.String() + ";" + p.Y.String() }

// IntSegment is an input segment with integer endpoints. IDs must be
// unique across the input set.
type IntSegment struct {
	ID             int
	X1, Y1, X2, Y2 int64
}

// IntersectionPoint is a location where two or more segments meet,
// together with the sorted set of participating segment IDs.
type IntersectionPoint struct {
	P   Point
	IDs []int
}

// Overlap is a maximal positive-length collinear portion shared by two
// or more segments, with the sorted set of participating segment IDs.
type Overlap struct {
	A, B Point
	IDs  []int
}

// Result is the full intersection report for a set of segments.
type Result struct {
	Points   []IntersectionPoint
	Overlaps []Overlap
}
