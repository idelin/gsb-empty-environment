// Package closestpair finds the closest pair(s) of points in a set of
// ID-tagged two-dimensional integer points.
//
// Query returns the minimum squared Euclidean distance together with every
// pair of point IDs that attains it. Because duplicate coordinates can make
// the number of tied pairs quadratic in the input size, tied pairs are
// exposed through paginated accessors instead of a single mandatory slice.
//
// All distance arithmetic uses math/big integers: squared distances of
// int64 coordinates can exceed 128 bits, and no floating-point square roots
// are ever taken, so comparisons are exact.
package closestpair

import (
	"fmt"
	"math/big"
	"sort"
)

// Point is a two-dimensional integer point tagged with a unique ID.
type Point struct {
	ID   int64
	X, Y int64
}

// Pair identifies two points by ID. First is always less than Second.
type Pair struct {
	First  int64
	Second int64
}

// Result is the outcome of a Query: the minimum squared distance and all
// tied pairs, sorted by (First, Second).
type Result struct {
	dist2 *big.Int
	pairs []Pair
}

// Query finds the minimum squared distance over all pairs of points and
// every pair that attains it. It runs in O(n log n) time plus time linear
// in the number of reported pairs.
//
// It returns an error if fewer than two points are given or if any point
// ID occurs more than once.
func Query(points []Point) (*Result, error) {
	if len(points) < 2 {
		return nil, fmt.Errorf("closestpair: need at least 2 points, got %d", len(points))
	}
	seen := make(map[int64]struct{}, len(points))
	for _, p := range points {
		if _, ok := seen[p.ID]; ok {
			return nil, fmt.Errorf("closestpair: duplicate point ID %d", p.ID)
		}
		seen[p.ID] = struct{}{}
	}

	px := make([]Point, len(points))
	copy(px, points)
	sort.Slice(px, func(i, j int) bool {
		if px[i].X != px[j].X {
			return px[i].X < px[j].X
		}
		if px[i].Y != px[j].Y {
			return px[i].Y < px[j].Y
		}
		return px[i].ID < px[j].ID
	})

	s := &solver{tmp: make([]Point, len(px))}
	s.solve(px)

	sort.Slice(s.pairs, func(i, j int) bool {
		if s.pairs[i].First != s.pairs[j].First {
			return s.pairs[i].First < s.pairs[j].First
		}
		return s.pairs[i].Second < s.pairs[j].Second
	})
	return &Result{dist2: s.best, pairs: s.pairs}, nil
}

// MinDist2 returns the minimum squared Euclidean distance. The returned
// value is a copy and may be mutated freely by the caller.
func (r *Result) MinDist2() *big.Int {
	return new(big.Int).Set(r.dist2)
}

// NumPairs returns the total number of pairs attaining the minimum
// squared distance. It may be quadratic in the number of input points.
func (r *Result) NumPairs() int {
	return len(r.pairs)
}

// Pairs returns up to limit tied pairs starting at offset, in sorted
// (First, Second) order, so callers can page through a potentially
// quadratic number of results. A negative limit returns all remaining
// pairs. Offsets beyond the end yield an empty slice. The returned slice
// is a copy.
func (r *Result) Pairs(offset, limit int) []Pair {
	if offset < 0 {
		offset = 0
	}
	if offset > len(r.pairs) {
		offset = len(r.pairs)
	}
	end := len(r.pairs)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	out := make([]Pair, end-offset)
	copy(out, r.pairs[offset:end])
	return out
}

// solver carries the divide-and-conquer state.
type solver struct {
	best  *big.Int // minimum squared distance seen so far; nil before any pair
	pairs []Pair   // pairs attaining best
	tmp   []Point  // scratch buffer for merging y-sorted halves
}

// solve recursively computes the closest pairs within pts, which must be
// sorted by (X, Y, ID). On return pts is sorted by (Y, X, ID).
func (s *solver) solve(pts []Point) {
	n := len(pts)
	if n <= 3 {
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				s.consider(pts[i], pts[j])
			}
		}
		sort.Slice(pts, func(i, j int) bool {
			if pts[i].Y != pts[j].Y {
				return pts[i].Y < pts[j].Y
			}
			if pts[i].X != pts[j].X {
				return pts[i].X < pts[j].X
			}
			return pts[i].ID < pts[j].ID
		})
		return
	}

	mid := n / 2
	midX := pts[mid].X
	s.solve(pts[:mid])
	s.solve(pts[mid:])

	// Merge the two y-sorted halves back into pts, and simultaneously
	// build the strip of points within sqrt(best) of the dividing line,
	// tagging each strip point with the half it came from. A cross pair
	// can only tie or beat best if both of its coordinate differences
	// satisfy dx^2 <= best and dy^2 <= best.
	tmp := s.tmp[:n]
	type stripPoint struct {
		p    Point
		left bool
	}
	strip := make([]stripPoint, 0, n)
	inStrip := func(p Point) bool {
		return sqDiff(p.X, midX).Cmp(s.best) <= 0
	}
	i, j, k := 0, mid, 0
	for i < mid && j < n {
		if lessY(pts[i], pts[j]) {
			if inStrip(pts[i]) {
				strip = append(strip, stripPoint{pts[i], true})
			}
			tmp[k] = pts[i]
			i++
		} else {
			if inStrip(pts[j]) {
				strip = append(strip, stripPoint{pts[j], false})
			}
			tmp[k] = pts[j]
			j++
		}
		k++
	}
	for ; i < mid; i++ {
		if inStrip(pts[i]) {
			strip = append(strip, stripPoint{pts[i], true})
		}
		tmp[k] = pts[i]
		k++
	}
	for ; j < n; j++ {
		if inStrip(pts[j]) {
			strip = append(strip, stripPoint{pts[j], false})
		}
		tmp[k] = pts[j]
		k++
	}
	copy(pts, tmp)

	// Sweep the strip, considering only pairs that straddle the dividing
	// line: pairs within one half were already reported by the recursion,
	// and re-examining them here would report them again.
	for i = 0; i < len(strip); i++ {
		for j = i + 1; j < len(strip); j++ {
			if sqDiff(strip[j].p.Y, strip[i].p.Y).Cmp(s.best) > 0 {
				break
			}
			if strip[i].left != strip[j].left {
				s.consider(strip[i].p, strip[j].p)
			}
		}
	}
}

// consider records the pair (a, b) if it ties or improves the best
// squared distance found so far.
func (s *solver) consider(a, b Point) {
	d := dist2(a, b)
	switch {
	case s.best == nil || d.Cmp(s.best) < 0:
		s.best = d
		s.pairs = append(s.pairs[:0], makePair(a, b))
	case d.Cmp(s.best) == 0:
		s.pairs = append(s.pairs, makePair(a, b))
	}
}

func lessY(a, b Point) bool {
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	if a.X != b.X {
		return a.X < b.X
	}
	return a.ID < b.ID
}

func makePair(a, b Point) Pair {
	if a.ID < b.ID {
		return Pair{First: a.ID, Second: b.ID}
	}
	return Pair{First: b.ID, Second: a.ID}
}

// dist2 returns the exact squared Euclidean distance between a and b.
func dist2(a, b Point) *big.Int {
	dx := sqDiff(a.X, b.X)
	dy := sqDiff(a.Y, b.Y)
	return dx.Add(dx, dy)
}

// sqDiff returns (a - b)^2 as a big integer, safe for the full int64 range.
func sqDiff(a, b int64) *big.Int {
	d := new(big.Int).Sub(big.NewInt(a), big.NewInt(b))
	return d.Mul(d, d)
}
