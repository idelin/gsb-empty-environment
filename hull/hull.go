// Package hull computes the convex hull of a 2D point cloud using exact
// rational arithmetic. Points may have rational coordinates and the input
// may contain duplicates. No floating-point tolerance is used anywhere:
// every predicate is an exact sign test on big.Rat values.
//
// The hull is returned as counterclockwise extreme points only: points
// lying in the interior of a hull edge are not listed as vertices, and a
// fully collinear cloud yields exactly its two endpoints. Every hull edge
// carries the ids of all input points collinear with (and between) its
// endpoints.
package hull

import (
	"math/big"
	"sort"
)

// Point is a 2D point with exact rational coordinates.
type Point struct {
	X, Y *big.Rat
}

// Pt builds a Point from integer coordinates.
func Pt(x, y int64) Point {
	return Point{big.NewRat(x, 1), big.NewRat(y, 1)}
}

// Rat builds a Point from rational strings such as "3/2" or "-1.25".
func Rat(x, y string) Point {
	rx, okx := new(big.Rat).SetString(x)
	ry, oky := new(big.Rat).SetString(y)
	if !okx || !oky {
		panic("hull: invalid rational coordinate")
	}
	return Point{rx, ry}
}

// Float builds a Point from float64 coordinates. The conversion is exact:
// the rational value equals the binary value of the float64.
func Float(x, y float64) Point {
	return Point{new(big.Rat).SetFloat64(x), new(big.Rat).SetFloat64(y)}
}

// Edge is one directed hull edge. From and To are input ids of adjacent
// hull vertices (counterclockwise). Collinear lists the ids of every input
// point lying on the closed segment From-To, endpoints included, sorted
// ascending.
type Edge struct {
	From, To  int
	Collinear []int
}

// Hull is the convex hull of a point cloud.
type Hull struct {
	// Vertices are input ids of the extreme points, counterclockwise.
	// A fully collinear cloud yields exactly two vertices (the endpoints);
	// a cloud of identical points yields one.
	Vertices []int
	// Edges are the cyclic hull edges, one per consecutive vertex pair.
	// A two-vertex hull has two edges (one per direction).
	Edges []Edge
}

// orient returns the exact signed area*2 of triangle (a,b,c): positive if
// c is strictly left of a->b, zero if collinear, negative if right.
func orient(a, b, c Point) *big.Rat {
	abx := new(big.Rat).Sub(b.X, a.X)
	aby := new(big.Rat).Sub(b.Y, a.Y)
	acx := new(big.Rat).Sub(c.X, a.X)
	acy := new(big.Rat).Sub(c.Y, a.Y)
	t1 := new(big.Rat).Mul(abx, acy)
	t2 := new(big.Rat).Mul(aby, acx)
	return t1.Sub(t1, t2)
}

// sign returns -1, 0 or +1.
func sign(r *big.Rat) int { return r.Sign() }

func key(p Point) string { return p.X.RatString() + "," + p.Y.RatString() }

// onSegment reports whether p lies on the closed segment a-b. Exact.
func onSegment(a, b, p Point) bool {
	if sign(orient(a, b, p)) != 0 {
		return false
	}
	loX, hiX := a.X, b.X
	if loX.Cmp(hiX) > 0 {
		loX, hiX = hiX, loX
	}
	loY, hiY := a.Y, b.Y
	if loY.Cmp(hiY) > 0 {
		loY, hiY = hiY, loY
	}
	return p.X.Cmp(loX) >= 0 && p.X.Cmp(hiX) <= 0 &&
		p.Y.Cmp(loY) >= 0 && p.Y.Cmp(hiY) <= 0
}

// unique returns the distinct points sorted lexicographically by (X, Y),
// together with, for each, the smallest input id among its duplicates.
func unique(pts []Point) ([]Point, []int) {
	first := make(map[string]int, len(pts))
	for i, p := range pts {
		k := key(p)
		if _, ok := first[k]; !ok {
			first[k] = i
		}
	}
	uq := make([]Point, 0, len(first))
	for k := range first {
		uq = append(uq, pts[first[k]])
	}
	sort.Slice(uq, func(i, j int) bool {
		if c := uq[i].X.Cmp(uq[j].X); c != 0 {
			return c < 0
		}
		return uq[i].Y.Cmp(uq[j].Y) < 0
	})
	ids := make([]int, len(uq))
	for i, p := range uq {
		ids[i] = first[key(p)]
	}
	return uq, ids
}

// ConvexHull computes the convex hull of pts. Duplicate points are
// tolerated; every input id on a hull edge is reported in that edge's
// Collinear field. Runs Andrew's monotone chain with exact predicates.
func ConvexHull(pts []Point) Hull {
	if len(pts) == 0 {
		return Hull{}
	}
	uq, ids := unique(pts)
	if len(uq) == 1 {
		return Hull{Vertices: []int{ids[0]}}
	}

	// Monotone chain. Popping on orient <= 0 drops non-strict left turns,
	// so collinear points along an edge never become vertices.
	lower := make([]int, 0, len(uq))
	for i := range uq {
		for len(lower) >= 2 &&
			sign(orient(uq[lower[len(lower)-2]], uq[lower[len(lower)-1]], uq[i])) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, i)
	}
	upper := make([]int, 0, len(uq))
	for i := len(uq) - 1; i >= 0; i-- {
		for len(upper) >= 2 &&
			sign(orient(uq[upper[len(upper)-2]], uq[upper[len(upper)-1]], uq[i])) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, i)
	}
	chain := append(lower[:len(lower)-1], upper[:len(upper)-1]...)

	verts := make([]int, len(chain))
	for i, ui := range chain {
		verts[i] = ids[ui]
	}

	h := Hull{Vertices: verts}
	n := len(verts)
	for i := 0; i < n; i++ {
		from, to := verts[i], verts[(i+1)%n]
		e := Edge{From: from, To: to}
		for id, p := range pts {
			if onSegment(pts[from], pts[to], p) {
				e.Collinear = append(e.Collinear, id)
			}
		}
		h.Edges = append(h.Edges, e)
	}
	return h
}
