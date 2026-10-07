// Package convexhull computes 2D convex hulls using exact rational
// arithmetic. All predicates are evaluated with math/big.Rat, so results
// never depend on floating-point tolerances.
//
// The hull is returned as counterclockwise extreme points (vertices).
// Points lying strictly inside an edge are not listed as vertices; when
// every input point is collinear, only the two endpoints are kept.
// Each hull edge additionally carries the ids of every input point that
// lies on the edge segment (duplicates included).
package convexhull

import (
	"math/big"
	"sort"
)

// Point is a 2D point with exact rational coordinates. The coordinates
// must not be mutated after the point is handed to this package.
type Point struct {
	X, Y *big.Rat
}

// Pt builds a Point from integer coordinates.
func Pt(x, y int64) Point {
	return Point{big.NewRat(x, 1), big.NewRat(y, 1)}
}

// PtFrac builds a Point from fractional coordinates xn/xd, yn/yd.
func PtFrac(xn, xd, yn, yd int64) Point {
	return Point{big.NewRat(xn, xd), big.NewRat(yn, yd)}
}

// Edge is one directed edge of the hull, from vertex From to vertex To
// (input ids, counterclockwise). Points lists the ids of every input
// point lying on the closed segment, in ascending id order; it always
// includes From and To. A point coinciding with a vertex belongs to both
// edges adjacent to that vertex.
type Edge struct {
	From, To int
	Points   []int
}

// Hull is the convex hull of a point set.
//
// Vertices holds input ids of the extreme points in counterclockwise
// order, starting from the lexicographically smallest (x, then y)
// vertex. When several input points share a vertex's coordinates, the
// smallest input id is used. Degenerate cases: all points identical
// yields a single vertex and no edges; all points collinear yields
// exactly two vertices and a single edge from the first to the second.
type Hull struct {
	Vertices []int
	Edges    []Edge
}

// orientSign returns the sign of the z-component of (b-a) x (c-a):
// positive when c is strictly left of the directed line a->b.
func orientSign(a, b, c Point) int {
	abx := new(big.Rat).Sub(b.X, a.X)
	aby := new(big.Rat).Sub(b.Y, a.Y)
	acx := new(big.Rat).Sub(c.X, a.X)
	acy := new(big.Rat).Sub(c.Y, a.Y)
	cross := new(big.Rat).Mul(abx, acy)
	cross.Sub(cross, new(big.Rat).Mul(aby, acx))
	return cross.Sign()
}

// onSegment reports whether p lies on the closed segment ab, given that
// p is already known to be collinear with a and b.
func onSegment(a, b, p Point) bool {
	if p.X.Cmp(a.X) < 0 && p.X.Cmp(b.X) < 0 {
		return false
	}
	if p.X.Cmp(a.X) > 0 && p.X.Cmp(b.X) > 0 {
		return false
	}
	if p.Y.Cmp(a.Y) < 0 && p.Y.Cmp(b.Y) < 0 {
		return false
	}
	if p.Y.Cmp(a.Y) > 0 && p.Y.Cmp(b.Y) > 0 {
		return false
	}
	return true
}

// coordKey canonically identifies a coordinate pair; big.Rat normalizes
// on construction, so its string form is a canonical key.
type coordKey struct{ x, y string }

func keyOf(p Point) coordKey { return coordKey{p.X.String(), p.Y.String()} }

// ConvexHull computes the convex hull of pts. Duplicate points are
// allowed; every input id still appears in the Points list of each edge
// it lies on.
func ConvexHull(pts []Point) Hull {
	if len(pts) == 0 {
		return Hull{}
	}

	// Group duplicate coordinates; representative id is the smallest.
	rep := make(map[coordKey]int)
	for i, p := range pts {
		k := keyOf(p)
		if j, ok := rep[k]; !ok || i < j {
			rep[k] = i
		}
	}
	uniq := make([]Point, 0, len(rep))
	for _, id := range rep {
		uniq = append(uniq, pts[id])
	}
	sort.Slice(uniq, func(i, j int) bool {
		if c := uniq[i].X.Cmp(uniq[j].X); c != 0 {
			return c < 0
		}
		return uniq[i].Y.Cmp(uniq[j].Y) < 0
	})
	// Rebuild ids to match the sorted order.
	sortedID := make([]int, len(uniq))
	for i, p := range uniq {
		sortedID[i] = rep[keyOf(p)]
	}

	n := len(uniq)
	if n == 1 {
		return Hull{Vertices: []int{sortedID[0]}}
	}

	// Andrew's monotone chain. Popping on cross <= 0 drops collinear
	// intermediate points, keeping extreme endpoints only.
	lower := make([]int, 0, n)
	for i := 0; i < n; i++ {
		for len(lower) >= 2 &&
			orientSign(uniq[lower[len(lower)-2]], uniq[lower[len(lower)-1]], uniq[i]) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, i)
	}
	upper := make([]int, 0, n)
	for i := n - 1; i >= 0; i-- {
		for len(upper) >= 2 &&
			orientSign(uniq[upper[len(upper)-2]], uniq[upper[len(upper)-1]], uniq[i]) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, i)
	}
	chain := append(lower[:len(lower)-1], upper[:len(upper)-1]...)

	vertices := make([]int, len(chain))
	for i, idx := range chain {
		vertices[i] = sortedID[idx]
	}

	h := Hull{Vertices: vertices}
	if len(vertices) == 2 {
		// Fully collinear input: a single edge between the endpoints.
		h.Edges = []Edge{h.buildEdge(pts, vertices[0], vertices[1])}
		return h
	}
	for i, from := range vertices {
		to := vertices[(i+1)%len(vertices)]
		h.Edges = append(h.Edges, h.buildEdge(pts, from, to))
	}
	return h
}

// buildEdge collects every input id whose point lies on segment from-to.
func (h Hull) buildEdge(pts []Point, from, to int) Edge {
	e := Edge{From: from, To: to}
	a, b := pts[from], pts[to]
	for i, p := range pts {
		if orientSign(a, b, p) == 0 && onSegment(a, b, p) {
			e.Points = append(e.Points, i)
		}
	}
	return e
}
