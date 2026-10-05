// Package convlayers computes the convex-hull layer decomposition
// (onion decomposition) of integer point sets.
//
// The boundary of the convex hull of the remaining points is peeled off
// repeatedly. Every point lying on a hull edge, including collinear edge
// points, belongs to the peeled layer. Points with duplicate coordinates
// share one layer, but all of their IDs are preserved in the layer ring.
// Orientation and area computations use exact big-integer arithmetic, so
// results never flip because of overflow.
package convlayers

import (
	"math/big"
	"sort"
)

// Point is an integer point in the plane.
type Point struct {
	X, Y int64
}

// Orient returns the sign of the cross product (b-a)x(c-a), computed with
// exact arithmetic: +1 when c lies strictly left of the directed line
// a->b, -1 when it lies strictly right, and 0 when the three points are
// collinear.
func Orient(a, b, c Point) int {
	abx := new(big.Int).Sub(big.NewInt(b.X), big.NewInt(a.X))
	aby := new(big.Int).Sub(big.NewInt(b.Y), big.NewInt(a.Y))
	acx := new(big.Int).Sub(big.NewInt(c.X), big.NewInt(a.X))
	acy := new(big.Int).Sub(big.NewInt(c.Y), big.NewInt(a.Y))
	cross := new(big.Int).Mul(abx, acy)
	cross.Sub(cross, new(big.Int).Mul(aby, acx))
	return cross.Sign()
}

// Area2 returns twice the signed area of the polygon given by ring, in
// exact arithmetic. The value is positive when the ring is oriented
// counterclockwise.
func Area2(ring []Point) *big.Int {
	sum := new(big.Int)
	for i := range ring {
		j := (i + 1) % len(ring)
		term := new(big.Int).Mul(big.NewInt(ring[i].X), big.NewInt(ring[j].Y))
		term.Sub(term, new(big.Int).Mul(big.NewInt(ring[j].X), big.NewInt(ring[i].Y)))
		sum.Add(sum, term)
	}
	return sum
}

// Layers decomposes pts into convex-hull layers. It returns layerOf, the
// 0-based layer index of every input point, and rings, the canonical
// boundary ring of each layer expressed as input point indices.
//
// All points collinear on a hull edge belong to the same layer. Duplicate
// coordinates share a layer; all of their IDs appear in the ring,
// adjacent and in ascending index order. Each non-degenerate ring is
// counterclockwise and starts at the lexicographically smallest
// coordinate (by X, then Y). A degenerate layer (a single point, two
// points, or all remaining points collinear) lists its coordinates once,
// in lexicographic order.
func Layers(pts []Point) (layerOf []int, rings [][]int) {
	layerOf = make([]int, len(pts))
	for i := range layerOf {
		layerOf[i] = -1
	}
	idsByCoord := make(map[Point][]int, len(pts))
	for i, p := range pts {
		idsByCoord[p] = append(idsByCoord[p], i)
	}
	remaining := make([]Point, 0, len(idsByCoord))
	for p := range idsByCoord {
		remaining = append(remaining, p)
	}
	sortPoints(remaining)

	for layer := 0; len(remaining) > 0; layer++ {
		var ringCoords []Point
		if len(remaining) <= 2 || allCollinear(remaining) {
			ringCoords = remaining // lexicographic order along the line
		} else {
			ringCoords = hullBoundary(remaining)
		}
		onBoundary := make(map[Point]bool, len(ringCoords))
		var ring []int
		for _, p := range ringCoords {
			onBoundary[p] = true
			ring = append(ring, idsByCoord[p]...)
		}
		for _, id := range ring {
			layerOf[id] = layer
		}
		rings = append(rings, ring)
		next := remaining[:0]
		for _, p := range remaining {
			if !onBoundary[p] {
				next = append(next, p)
			}
		}
		remaining = next
	}
	return layerOf, rings
}

func sortPoints(s []Point) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].X != s[j].X {
			return s[i].X < s[j].X
		}
		return s[i].Y < s[j].Y
	})
}

func allCollinear(s []Point) bool {
	for i := 2; i < len(s); i++ {
		if Orient(s[0], s[1], s[i]) != 0 {
			return false
		}
	}
	return true
}

// hullBoundary returns the convex-hull boundary of s, including points
// collinear on hull edges, as a counterclockwise ring that starts at the
// lexicographically smallest point. s must be sorted, deduplicated, and
// not all collinear.
func hullBoundary(s []Point) []Point {
	lower := make([]Point, 0, len(s))
	for _, p := range s {
		for len(lower) >= 2 && Orient(lower[len(lower)-2], lower[len(lower)-1], p) < 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}
	upper := make([]Point, 0, len(s))
	for i := len(s) - 1; i >= 0; i-- {
		p := s[i]
		for len(upper) >= 2 && Orient(upper[len(upper)-2], upper[len(upper)-1], p) < 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}
	return append(lower, upper[1:len(upper)-1]...)
}
