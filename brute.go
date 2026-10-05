package frsimp

import (
	"errors"
	"fmt"
)

// maxBruteForceVertices caps the enumeration cost of BruteForce: 2^(n-2)
// candidate subsequences are examined.
const maxBruteForceVertices = 18

// BruteForce finds the optimal simplification by enumerating every
// subsequence that keeps the first and last point, scoring each with the
// genuine discrete Frechet dynamic program (DiscreteFrechet), and keeping
// the shortest, ties broken by lexicographic index order. It is intended for
// small polylines and serves as the ground truth for Verify.
//
// opts.Budget is ignored: the enumeration always runs to completion.
func BruteForce(points []Point, opts Options) (Result, error) {
	n := len(points)
	if n == 0 {
		return Result{}, errors.New("frsimp: empty polyline")
	}
	if opts.Epsilon < 0 {
		return Result{}, errors.New("frsimp: negative epsilon")
	}
	if n > maxBruteForceVertices {
		return Result{}, fmt.Errorf("frsimp: %d vertices too many for brute force (max %d)", n, maxBruteForceVertices)
	}
	metric := opts.Metric
	if metric == nil {
		metric = Euclidean
	}
	if n <= 2 {
		return Simplify(points, opts)
	}

	var best Result
	var bestIdx []int
	for mask := 0; mask < 1<<uint(n-2); mask++ {
		idx := make([]int, 0, n)
		idx = append(idx, 0)
		for b := 0; b < n-2; b++ {
			if mask&(1<<uint(b)) != 0 {
				idx = append(idx, b+1)
			}
		}
		idx = append(idx, n-1)
		if bestIdx != nil {
			if len(idx) > len(bestIdx) {
				continue
			}
			if len(idx) == len(bestIdx) && !lexLess(idx, bestIdx) {
				continue
			}
		}
		sub := make([]Point, len(idx))
		for t, i := range idx {
			sub[t] = points[i]
		}
		dist, matching := DiscreteFrechet(points, sub, metric)
		if dist <= opts.Epsilon {
			bestIdx = idx
			best = Result{
				Indices:       idx,
				Matching:      matching,
				Distance:      dist,
				DistanceExact: true,
				Optimal:       true,
			}
		}
	}
	// The full vertex set always has distance 0, so a feasible answer exists.
	return best, nil
}

// lexLess reports whether a is lexicographically smaller than b.
func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// Verify cross-checks Simplify against brute-force enumeration: the fast
// algorithm must produce exactly the optimal count and the lexicographically
// smallest index sequence. Only usable for small polylines. opts.Budget is
// ignored (both sides run unbounded).
func Verify(points []Point, opts Options) (Result, error) {
	opts.Budget = 0
	fast, err := Simplify(points, opts)
	if err != nil {
		return Result{}, err
	}
	slow, err := BruteForce(points, opts)
	if err != nil {
		return Result{}, err
	}
	if len(fast.Indices) != len(slow.Indices) {
		return fast, fmt.Errorf("frsimp: count mismatch: got %d points, optimal is %d",
			len(fast.Indices), len(slow.Indices))
	}
	for t := range fast.Indices {
		if fast.Indices[t] != slow.Indices[t] {
			return fast, fmt.Errorf("frsimp: index mismatch: got %v, optimal is %v",
				fast.Indices, slow.Indices)
		}
	}
	return fast, nil
}

// CheckMatching validates simplification evidence: indices must be a
// strictly increasing subsequence from 0 to len(points)-1, and matching must
// be a lawful monotone coupling between the polyline and the subsequence
// whose every pair distance is at most eps under metric (nil = Euclidean).
func CheckMatching(points []Point, indices []int, matching []MatchPair, eps float64, metric Metric) error {
	n := len(points)
	if n == 0 {
		return errors.New("frsimp: empty polyline")
	}
	if metric == nil {
		metric = Euclidean
	}
	if len(indices) == 0 || indices[0] != 0 || indices[len(indices)-1] != n-1 {
		return fmt.Errorf("frsimp: indices must start at 0 and end at %d", n-1)
	}
	for t := 1; t < len(indices); t++ {
		if indices[t] <= indices[t-1] || indices[t] >= n {
			return fmt.Errorf("frsimp: indices not a strictly increasing subsequence at position %d", t)
		}
	}
	m := len(indices)
	if len(matching) == 0 {
		return errors.New("frsimp: empty matching")
	}
	if matching[0] != (MatchPair{Orig: 0, Simp: 0}) {
		return errors.New("frsimp: matching must start at (0,0)")
	}
	if matching[len(matching)-1] != (MatchPair{Orig: n - 1, Simp: m - 1}) {
		return fmt.Errorf("frsimp: matching must end at (%d,%d)", n-1, m-1)
	}
	for t := 1; t < len(matching); t++ {
		di := matching[t].Orig - matching[t-1].Orig
		dj := matching[t].Simp - matching[t-1].Simp
		if di < 0 || dj < 0 || di > 1 || dj > 1 || di+dj == 0 {
			return fmt.Errorf("frsimp: matching not a monotone coupling at step %d", t)
		}
	}
	for _, p := range matching {
		if p.Orig < 0 || p.Orig >= n || p.Simp < 0 || p.Simp >= m {
			return fmt.Errorf("frsimp: matching pair (%d,%d) out of range", p.Orig, p.Simp)
		}
		if d := metric(points[p.Orig], points[indices[p.Simp]]); d > eps {
			return fmt.Errorf("frsimp: pair (%d,%d) distance %g exceeds epsilon %g",
				p.Orig, p.Simp, d, eps)
		}
	}
	return nil
}
