// Package trajsimp computes minimum-size simplifications of a 2D
// trajectory (polyline) under an exact rational error bound.
//
// A simplification keeps a subset of the original points (by index) and
// replaces each dropped run with a straight segment. A kept edge (i, j)
// is legal only when every intermediate point k (i < k < j) has squared
// distance to the segment pts[i]-pts[j] less than or equal to a rational
// threshold. All distance computations use exact rational arithmetic
// (math/big.Rat), so boundary cases (distance^2 exactly equal to the
// threshold) are decided exactly, never by floating-point rounding.
//
// The first and last points of the trajectory are always kept, together
// with any caller-specified mandatory indices. Among all legal kept-index
// sequences of minimum length, the lexicographically smallest one (as an
// increasing sequence of original indices) is returned. Duplicate
// coordinates are handled naturally: a zero-length segment falls back to
// point-to-point distance.
package trajsimp

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Point is a 2D point with exact rational coordinates.
type Point struct {
	X, Y *big.Rat
}

// IntPoint builds a Point from integer coordinates.
func IntPoint(x, y int64) Point {
	return Point{X: big.NewRat(x, 1), Y: big.NewRat(y, 1)}
}

// IntPoints converts integer coordinate pairs to a slice of Point.
func IntPoints(coords [][2]int64) []Point {
	pts := make([]Point, len(coords))
	for i, c := range coords {
		pts[i] = IntPoint(c[0], c[1])
	}
	return pts
}

func ratMul(a, b *big.Rat) *big.Rat {
	return new(big.Rat).Mul(a, b)
}

func dist2(a, b Point) *big.Rat {
	dx := new(big.Rat).Sub(a.X, b.X)
	dy := new(big.Rat).Sub(a.Y, b.Y)
	return new(big.Rat).Add(ratMul(dx, dx), ratMul(dy, dy))
}

// SegDist2 returns the exact squared distance from p to the segment a-b.
// If a and b coincide, it is the squared distance from p to a.
func SegDist2(a, b, p Point) *big.Rat {
	abx := new(big.Rat).Sub(b.X, a.X)
	aby := new(big.Rat).Sub(b.Y, a.Y)
	len2 := new(big.Rat).Add(ratMul(abx, abx), ratMul(aby, aby))
	if len2.Sign() == 0 {
		return dist2(a, p)
	}
	apx := new(big.Rat).Sub(p.X, a.X)
	apy := new(big.Rat).Sub(p.Y, a.Y)
	dot := new(big.Rat).Add(ratMul(apx, abx), ratMul(apy, aby))
	if dot.Sign() <= 0 {
		return dist2(a, p)
	}
	if dot.Cmp(len2) >= 0 {
		return dist2(b, p)
	}
	// Perpendicular foot lies strictly inside the segment:
	// d^2 = |ap|^2 - dot^2 / |ab|^2  (exact, non-negative).
	ap2 := new(big.Rat).Add(ratMul(apx, apx), ratMul(apy, apy))
	corr := ratMul(dot, dot)
	corr.Quo(corr, len2)
	return ap2.Sub(ap2, corr)
}

// EdgeValid reports whether replacing the run pts[i..j] by the single
// segment pts[i]-pts[j] respects the squared-distance threshold for every
// intermediate point. i < j is required.
func EdgeValid(pts []Point, i, j int, threshold *big.Rat) bool {
	for k := i + 1; k < j; k++ {
		if SegDist2(pts[i], pts[j], pts[k]).Cmp(threshold) > 0 {
			return false
		}
	}
	return true
}

// SequenceValid reports whether the increasing kept-index sequence seq is
// a legal simplification of pts under the threshold: every consecutive
// kept pair must form a valid edge.
func SequenceValid(pts []Point, threshold *big.Rat, seq []int) bool {
	for s := 0; s+1 < len(seq); s++ {
		if !EdgeValid(pts, seq[s], seq[s+1], threshold) {
			return false
		}
	}
	return true
}

// normalizeMandatory validates the mandatory indices, then adds the two
// endpoints and returns the sorted, de-duplicated set.
func normalizeMandatory(n int, mandatory []int) ([]int, error) {
	set := make(map[int]bool, len(mandatory)+2)
	for _, idx := range mandatory {
		if idx < 0 || idx >= n {
			return nil, fmt.Errorf("trajsimp: mandatory index %d out of range [0,%d)", idx, n)
		}
		set[idx] = true
	}
	set[0] = true
	set[n-1] = true
	out := make([]int, 0, len(set))
	for idx := range set {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out, nil
}

// MinKeepIndices returns the lexicographically smallest minimum-length
// sequence of kept indices for the trajectory pts under the rational
// squared-distance threshold. The sequence always starts at 0, ends at
// n-1, and contains every mandatory index. It returns nil for an empty
// trajectory. An error is returned for a nil/negative threshold or an
// out-of-range mandatory index.
func MinKeepIndices(pts []Point, threshold *big.Rat, mandatory []int) ([]int, error) {
	n := len(pts)
	if n == 0 {
		return nil, nil
	}
	if threshold == nil {
		return nil, errors.New("trajsimp: nil threshold")
	}
	if threshold.Sign() < 0 {
		return nil, errors.New("trajsimp: negative threshold")
	}
	must, err := normalizeMandatory(n, mandatory)
	if err != nil {
		return nil, err
	}
	if len(must) == 1 { // n == 1: the single point is both endpoints.
		return must, nil
	}

	// Memoized edge-validity table; -1 unknown, 0 invalid, 1 valid.
	valid := make([][]int8, n)
	for i := range valid {
		valid[i] = make([]int8, n)
		for j := range valid[i] {
			valid[i][j] = -1
		}
	}
	edgeOK := func(i, j int) bool {
		if valid[i][j] < 0 {
			if EdgeValid(pts, i, j, threshold) {
				valid[i][j] = 1
			} else {
				valid[i][j] = 0
			}
		}
		return valid[i][j] == 1
	}

	// The kept sequence must pass through every mandatory index in order,
	// and edges only go forward, so the problem decomposes into
	// independent sub-problems between consecutive mandatory indices.
	// Both objectives (minimum hops, then lexicographic order) compose
	// across the concatenation.
	var result []int
	for s := 0; s+1 < len(must); s++ {
		a, b := must[s], must[s+1]
		seg := shortestLexPath(a, b, edgeOK)
		if seg == nil {
			// Cannot happen: the unit edge (k, k+1) is always valid.
			return nil, fmt.Errorf("trajsimp: no valid path from %d to %d", a, b)
		}
		if result == nil {
			result = append(result, seg...)
		} else {
			result = append(result, seg[1:]...)
		}
	}
	return result, nil
}

// shortestLexPath finds a minimum-hop path from a to b (a < b) in the DAG
// where edge i->j exists iff edgeOK(i, j). Among minimum-hop paths it
// returns the lexicographically smallest vertex sequence.
func shortestLexPath(a, b int, edgeOK func(i, j int) bool) []int {
	const inf = int(^uint(0) >> 1)
	// reach[j] = minimum hops from j to b, computed backwards.
	reach := make([]int, b+1)
	for j := a; j < b; j++ {
		reach[j] = inf
	}
	reach[b] = 0
	for j := b - 1; j >= a; j-- {
		for k := j + 1; k <= b; k++ {
			if reach[k] != inf && edgeOK(j, k) && reach[k]+1 < reach[j] {
				reach[j] = reach[k] + 1
			}
		}
	}
	if reach[a] == inf {
		return nil
	}
	// Greedy forward: at each step take the smallest next index that
	// still lies on some minimum-hop path. This yields the
	// lexicographically smallest sequence among shortest paths.
	path := []int{a}
	cur := a
	for cur != b {
		for nxt := cur + 1; nxt <= b; nxt++ {
			if reach[nxt] == reach[cur]-1 && edgeOK(cur, nxt) {
				path = append(path, nxt)
				cur = nxt
				break
			}
		}
	}
	return path
}
