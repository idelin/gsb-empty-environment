// Package onion computes the convex-hull layer decomposition (onion
// decomposition) of a finite set of integer points.
//
// The outermost layer is the boundary of the convex hull of the whole set;
// removing it and repeating yields the inner layers. Every point that lies
// on a hull edge (not only the extreme vertices) belongs to that layer, and
// duplicate coordinates always share a layer while keeping their distinct
// input indices. All orientation and area computations use exact big-integer
// arithmetic, so results never flip because of overflow, even for
// coordinates near the int64 limits.
package onion

import (
	"math/big"
	"sort"
)

// Point is a point with integer coordinates.
type Point struct {
	X, Y int64
}

// Layers decomposes pts into convex-hull layers.
//
// It returns layerOf, where layerOf[i] is the 0-based layer of pts[i]
// (0 is the outermost layer), and rings, where rings[k] lists the input
// indices of layer k as a canonical boundary ring:
//
//   - Non-degenerate layers (spanning a positive area) are ordered
//     counterclockwise and start at the smallest point by (X, Y, index).
//   - Degenerate layers (a single point, or all remaining points collinear)
//     are sorted ascending by (X, Y, index).
//
// Points lying on a collinear hull edge belong to the same layer as the
// edge endpoints, and duplicate coordinates are always assigned to the same
// layer. Every input index appears in exactly one ring.
func Layers(pts []Point) (layerOf []int, rings [][]int) {
	n := len(pts)
	layerOf = make([]int, n)
	remaining := make([]int, n)
	for i := range remaining {
		remaining[i] = i
	}
	inRing := make([]bool, n)
	for layer := 0; len(remaining) > 0; layer++ {
		ring := boundaryRing(pts, remaining)
		for _, id := range ring {
			layerOf[id] = layer
			inRing[id] = true
		}
		rings = append(rings, ring)
		next := remaining[:0]
		for _, id := range remaining {
			if !inRing[id] {
				next = append(next, id)
			}
		}
		remaining = next
	}
	return layerOf, rings
}

// Orient returns the sign of the cross product (b-a) x (c-a): +1 if c lies
// strictly to the left of the directed line a->b, -1 if strictly to the
// right, and 0 if the three points are collinear. The result is computed
// with exact big-integer arithmetic and cannot overflow.
func Orient(a, b, c Point) int {
	d1x := sub64(b.X, a.X)
	d1y := sub64(b.Y, a.Y)
	d2x := sub64(c.X, a.X)
	d2y := sub64(c.Y, a.Y)
	t1 := new(big.Int).Mul(d1x, d2y)
	t2 := new(big.Int).Mul(d1y, d2x)
	return t1.Sub(t1, t2).Sign()
}

// Area2 returns the doubled signed area of the polygon formed by
// pts[ring[0]], pts[ring[1]], ... in order. It is positive for
// counterclockwise rings, negative for clockwise rings, and zero for
// degenerate (collinear) rings. The result is exact.
func Area2(pts []Point, ring []int) *big.Int {
	sum := new(big.Int)
	m := len(ring)
	for i := 0; i < m; i++ {
		a := pts[ring[i]]
		b := pts[ring[(i+1)%m]]
		t := new(big.Int).Mul(big.NewInt(a.X), big.NewInt(b.Y))
		t.Sub(t, new(big.Int).Mul(big.NewInt(a.Y), big.NewInt(b.X)))
		sum.Add(sum, t)
	}
	return sum
}

// sub64 returns a-b as a big integer, avoiding int64 subtraction overflow.
func sub64(a, b int64) *big.Int {
	return new(big.Int).Sub(big.NewInt(a), big.NewInt(b))
}

// lessID reports whether id a orders before id b by (X, Y, index).
func lessID(pts []Point, a, b int) bool {
	pa, pb := pts[a], pts[b]
	if pa.X != pb.X {
		return pa.X < pb.X
	}
	if pa.Y != pb.Y {
		return pa.Y < pb.Y
	}
	return a < b
}

// sortIDs sorts ids in place by (X, Y, index).
func sortIDs(pts []Point, ids []int) {
	sort.Slice(ids, func(i, j int) bool {
		return lessID(pts, ids[i], ids[j])
	})
}

// boundaryRing returns the canonical ring of input indices lying on the
// boundary of the convex hull of pts restricted to ids.
func boundaryRing(pts []Point, ids []int) []int {
	sorted := make([]int, len(ids))
	copy(sorted, ids)
	sortIDs(pts, sorted)
	if len(sorted) <= 2 {
		return sorted
	}

	// Monotone chain over the sorted points, keeping only extreme vertices
	// (collinear points on edges are re-inserted afterwards).
	lower := make([]int, 0, len(sorted))
	for _, id := range sorted {
		for len(lower) >= 2 && Orient(pts[lower[len(lower)-2]], pts[lower[len(lower)-1]], pts[id]) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, id)
	}
	upper := make([]int, 0, len(sorted))
	for i := len(sorted) - 1; i >= 0; i-- {
		id := sorted[i]
		for len(upper) >= 2 && Orient(pts[upper[len(upper)-2]], pts[upper[len(upper)-1]], pts[id]) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, id)
	}
	hull := append(lower[:len(lower)-1], upper[:len(upper)-1]...)

	if len(hull) <= 2 {
		// All remaining points are collinear: the whole set is the boundary.
		return sorted
	}

	// Walk the hull edges counterclockwise, inserting every point that lies
	// on an edge (including duplicates of the edge's starting vertex).
	ring := make([]int, 0, len(ids))
	h := len(hull)
	for e := 0; e < h; e++ {
		a, b := hull[e], hull[(e+1)%h]
		pa, pb := pts[a], pts[b]
		ring = append(ring, a)
		var mids []int
		for _, id := range ids {
			if id == a || id == b {
				continue
			}
			p := pts[id]
			if p == pb {
				continue // duplicates of vertex b are emitted with edge b->...
			}
			if Orient(pa, pb, p) != 0 || !between(pa, pb, p) {
				continue
			}
			mids = append(mids, id)
		}
		sort.Slice(mids, func(i, j int) bool {
			return alongEdge(pa, pb, pts[mids[i]], pts[mids[j]], mids[i], mids[j])
		})
		ring = append(ring, mids...)
	}

	// Canonical form: start at the smallest point by (X, Y, index).
	minPos := 0
	for i, id := range ring {
		if lessID(pts, id, ring[minPos]) {
			minPos = i
		}
	}
	if minPos > 0 {
		rotated := make([]int, 0, len(ring))
		rotated = append(rotated, ring[minPos:]...)
		rotated = append(rotated, ring[:minPos]...)
		ring = rotated
	}
	return ring
}

// between reports whether p lies on the closed segment a-b, assuming a, b, p
// are collinear. Pure comparisons, so no overflow is possible.
func between(a, b, p Point) bool {
	if a.X != b.X {
		lo, hi := a.X, b.X
		if lo > hi {
			lo, hi = hi, lo
		}
		return p.X >= lo && p.X <= hi
	}
	lo, hi := a.Y, b.Y
	if lo > hi {
		lo, hi = hi, lo
	}
	return p.Y >= lo && p.Y <= hi
}

// alongEdge orders collinear points p and q on the segment a-b along the
// direction from a to b, breaking ties by (X, Y, index).
func alongEdge(a, b, p, q Point, ip, iq int) bool {
	if a.X != b.X && p.X != q.X {
		if a.X < b.X {
			return p.X < q.X
		}
		return p.X > q.X
	}
	if a.Y != b.Y && p.Y != q.Y {
		if a.Y < b.Y {
			return p.Y < q.Y
		}
		return p.Y > q.Y
	}
	if p.X != q.X {
		return p.X < q.X
	}
	if p.Y != q.Y {
		return p.Y < q.Y
	}
	return ip < iq
}
