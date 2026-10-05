package polysimp

import (
	"errors"
	"fmt"
)

// Options controls Simplify.
type Options struct {
	// Budget caps the number of shortcut error evaluations (each evaluation
	// is an exact discrete Fréchet computation between a subchain and a
	// two-point polyline). Zero means unlimited. When the budget is
	// exhausted, Simplify still returns a feasible simplification: a valid
	// upper bound on the optimal number of retained points.
	Budget int
}

// Result describes a simplification of an input polyline.
type Result struct {
	// Indices are the retained positions in the input polyline, in
	// increasing order, always including 0 and len(input)-1.
	Indices []int
	// Points are the retained points (the simplified polyline).
	Points []Point
	// Error is the exact discrete Fréchet distance between the input
	// polyline and Points. It never exceeds the requested epsilon.
	Error float64
	// Matching is the evidence: a monotone matching between the input
	// polyline and Points whose maximum matched pair distance equals
	// Error. Validate it with CheckMatching.
	Matching []MatchPair
	// Optimal reports whether Indices provably minimizes the number of
	// retained points (ties broken by lexicographically smallest indices).
	// It is false when the evaluation budget was exhausted; the result is
	// then only a feasible upper bound.
	Optimal bool
	// BudgetExhausted reports whether the evaluation budget ran out.
	BudgetExhausted bool
}

type edge struct{ a, b int }

// Simplify returns a subsequence of p (endpoints preserved) whose discrete
// Fréchet distance to p is at most eps. Among all such subsequences it
// keeps the fewest points; ties are broken by lexicographically smallest
// index sequence. If opt.Budget is exhausted before optimality is proven,
// a feasible (but possibly suboptimal) simplification is returned with
// Optimal == false.
//
// The error criterion is the discrete Fréchet distance, evaluated exactly.
// Perpendicular distance to a shortcut segment is never used as a proxy:
// a shortcut (a, b) is accepted only if the discrete Fréchet distance
// between p[a..b] and the two-point polyline (p[a], p[b]) is at most eps.
func Simplify(p []Point, eps float64, opt Options) (Result, error) {
	n := len(p)
	if n == 0 {
		return Result{}, errors.New("polysimp: empty polyline")
	}
	if eps < 0 {
		return Result{}, errors.New("polysimp: negative epsilon")
	}
	if n <= 2 {
		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
		}
		pts := append([]Point(nil), p...)
		d, matching := DiscreteFrechet(p, pts)
		return Result{Indices: indices, Points: pts, Error: d, Matching: matching, Optimal: true}, nil
	}

	memo := make(map[edge]float64)
	spent := 0
	exhausted := false
	// edgeErr returns the exact discrete Fréchet error of shortcut (a,b),
	// or ok == false when the budget forbids evaluating it.
	edgeErr := func(a, b int) (float64, bool) {
		if b <= a+1 {
			return 0, true // consecutive points: zero error, free
		}
		if v, ok := memo[edge{a, b}]; ok {
			return v, true
		}
		if opt.Budget > 0 && spent >= opt.Budget {
			exhausted = true
			return 0, false
		}
		spent++
		d, _ := DiscreteFrechet(p[a:b+1], []Point{p[a], p[b]})
		memo[edge{a, b}] = d
		return d, true
	}

	// dp[i] = min number of retained points covering p[0..i], keeping i.
	dp := make([]int, n)
	dp[0] = 1
	for i := 1; i < n; i++ {
		dp[i] = -1
		for j := 0; j < i; j++ {
			if dp[j] < 0 {
				continue
			}
			d, ok := edgeErr(j, i)
			if !ok {
				continue
			}
			if d <= eps && (dp[i] < 0 || dp[j]+1 < dp[i]) {
				dp[i] = dp[j] + 1
			}
		}
	}
	// suff[i] = min number of shortcut edges from i to n-1.
	suff := make([]int, n)
	suff[n-1] = 0
	for i := n - 2; i >= 0; i-- {
		suff[i] = -1
		for j := i + 1; j < n; j++ {
			if suff[j] < 0 {
				continue
			}
			d, ok := edgeErr(i, j)
			if !ok {
				continue
			}
			if d <= eps && (suff[i] < 0 || suff[j]+1 < suff[i]) {
				suff[i] = suff[j] + 1
			}
		}
	}

	var indices []int
	if dp[n-1] >= 0 {
		// Greedily pick the lexicographically smallest index sequence among
		// those consistent with the (possibly partial) DP tables.
		indices = []int{0}
		cur := 0
		for cur != n-1 {
			next := -1
			for j := cur + 1; j < n; j++ {
				d, ok := edgeErr(cur, j)
				if !ok || d > eps {
					continue
				}
				if dp[j] == dp[cur]+1 && dp[j]+suff[j] == dp[n-1] {
					next = j
					break
				}
			}
			if next < 0 {
				break // inconsistent tables (budget); fall through to greedy
			}
			indices = append(indices, next)
			cur = next
		}
		if cur != n-1 {
			indices = nil
		}
	}
	if indices == nil {
		// Feasible fallback: greedy farthest-reachable jumps, single steps
		// (always zero error) when the budget is gone. Yields a valid upper
		// bound on the optimal point count.
		indices = []int{0}
		cur := 0
		for cur != n-1 {
			next := cur + 1
			for j := n - 1; j > cur+1; j-- {
				d, ok := edgeErr(cur, j)
				if !ok {
					break
				}
				if d <= eps {
					next = j
					break
				}
			}
			indices = append(indices, next)
			cur = next
		}
	}

	pts := make([]Point, len(indices))
	for k, idx := range indices {
		pts[k] = p[idx]
	}
	// Evidence: exact discrete Fréchet distance and matching for the whole
	// input vs. the simplified polyline (not budgeted).
	d, matching := DiscreteFrechet(p, pts)
	return Result{
		Indices:         indices,
		Points:          pts,
		Error:           d,
		Matching:        matching,
		Optimal:         !exhausted,
		BudgetExhausted: exhausted,
	}, nil
}

// Verify checks that indices define a valid simplification of p: strictly
// increasing, in range, preserving both endpoints, and with discrete
// Fréchet distance to p at most eps. On success it returns the
// corresponding Result including the matching evidence.
func Verify(p []Point, indices []int, eps float64) (Result, error) {
	n := len(p)
	if n == 0 {
		return Result{}, errors.New("polysimp: empty polyline")
	}
	if len(indices) == 0 {
		return Result{}, errors.New("polysimp: empty index sequence")
	}
	if indices[0] != 0 || indices[len(indices)-1] != n-1 {
		return Result{}, errors.New("polysimp: endpoints not preserved")
	}
	prev := -1
	for _, idx := range indices {
		if idx <= prev || idx >= n {
			return Result{}, fmt.Errorf("polysimp: indices not strictly increasing/in range at %d", idx)
		}
		prev = idx
	}
	pts := make([]Point, len(indices))
	for k, idx := range indices {
		pts[k] = p[idx]
	}
	d, matching := DiscreteFrechet(p, pts)
	if d > eps {
		return Result{}, fmt.Errorf("polysimp: discrete Fréchet error %v exceeds eps %v", d, eps)
	}
	return Result{Indices: append([]int(nil), indices...), Points: pts, Error: d, Matching: matching, Optimal: false}, nil
}
