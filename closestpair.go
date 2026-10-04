// Package closestpair finds the closest pair(s) in a set of integer
// 2D points identified by unique IDs.
//
// The minimum squared Euclidean distance is computed exactly with
// arbitrary-precision integer arithmetic, so coordinates anywhere in
// the int64 range are safe: squared differences never overflow and no
// floating-point square roots are ever compared.
//
// Because duplicate coordinates (or many co-circular points) can make
// the number of tied closest pairs grow quadratically, results are
// returned through a paginated API. Pairs are sorted by ID, so
// successive pages concatenate into a deterministic, gap-free list.
package closestpair

import (
	"errors"
	"math/big"
	"sort"
)

// Point is a 2D integer point with a unique ID.
type Point struct {
	ID   int64
	X, Y int64
}

// Pair is a pair of point IDs with A < B.
type Pair struct {
	A, B int64
}

// Result is one page of the closest-pair query.
type Result struct {
	// Dist2 is the minimum squared Euclidean distance (exact).
	Dist2 *big.Int
	// Pairs holds the requested page of tied pairs, sorted by (A, B).
	Pairs []Pair
	// Total is the total number of tied closest pairs across all pages.
	Total int
}

var (
	ErrTooFewPoints = errors.New("closestpair: at least two points are required")
	ErrDuplicateID  = errors.New("closestpair: point IDs must be unique")
	ErrBadRange     = errors.New("closestpair: offset and limit must be non-negative")
)

// bpoint is a Point with arbitrary-precision coordinates.
type bpoint struct {
	id   int64
	x, y *big.Int
}

// ClosestPairs returns the minimum squared distance over all pairs of
// points, together with a paginated list of every pair attaining it.
//
// offset and limit select the page: offset is the number of sorted
// pairs to skip and limit caps the page size (limit == 0 returns only
// the total count). Pages are contiguous and non-overlapping, so
// iterating offset += limit until an empty page is returned enumerates
// all tied pairs without omissions or duplicates.
func ClosestPairs(points []Point, offset, limit int) (*Result, error) {
	if offset < 0 || limit < 0 {
		return nil, ErrBadRange
	}
	if len(points) < 2 {
		return nil, ErrTooFewPoints
	}
	seen := make(map[int64]struct{}, len(points))
	pts := make([]bpoint, len(points))
	for i, p := range points {
		if _, dup := seen[p.ID]; dup {
			return nil, ErrDuplicateID
		}
		seen[p.ID] = struct{}{}
		pts[i] = bpoint{id: p.ID, x: big.NewInt(p.X), y: big.NewInt(p.Y)}
	}

	d := minDist2(pts)

	var pairs []Pair
	if d.Sign() == 0 {
		pairs = enumerateZero(pts)
	} else {
		pairs = enumeratePositive(pts, d)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})

	total := len(pairs)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := make([]Pair, end-offset)
	copy(page, pairs[offset:end])

	return &Result{Dist2: d, Pairs: page, Total: total}, nil
}

// dist2 returns the exact squared Euclidean distance between a and b.
func dist2(a, b bpoint) *big.Int {
	dx := new(big.Int).Sub(a.x, b.x)
	dy := new(big.Int).Sub(a.y, b.y)
	dx.Mul(dx, dx)
	dy.Mul(dy, dy)
	return dx.Add(dx, dy)
}

// newPair orders two IDs into a Pair with A < B.
func newPair(a, b int64) Pair {
	if a < b {
		return Pair{A: a, B: b}
	}
	return Pair{A: b, B: a}
}
