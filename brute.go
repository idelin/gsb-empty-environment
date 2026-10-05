package polysimp

import "errors"

// BruteForce enumerates every endpoint-preserving subsequence of p and
// returns the one with the fewest points (ties: lexicographically smallest
// indices) whose exact discrete Fréchet distance to p is at most eps.
//
// It exists to cross-check Simplify on small inputs: every candidate is
// judged by the true discrete Fréchet distance, never by a perpendicular
// distance heuristic. Inputs are limited to 24 points.
func BruteForce(p []Point, eps float64) (Result, error) {
	n := len(p)
	if n == 0 {
		return Result{}, errors.New("polysimp: empty polyline")
	}
	if eps < 0 {
		return Result{}, errors.New("polysimp: negative epsilon")
	}
	if n > 24 {
		return Result{}, errors.New("polysimp: too many points for enumeration (max 24)")
	}
	if n <= 2 {
		return Simplify(p, eps, Options{})
	}
	var best Result
	found := false
	interior := n - 2
	for mask := 0; mask < (1 << uint(interior)); mask++ {
		indices := make([]int, 0, n)
		indices = append(indices, 0)
		for k := 0; k < interior; k++ {
			if mask&(1<<uint(k)) != 0 {
				indices = append(indices, k+1)
			}
		}
		indices = append(indices, n-1)
		pts := make([]Point, len(indices))
		for i, idx := range indices {
			pts[i] = p[idx]
		}
		d, matching := DiscreteFrechet(p, pts)
		if d > eps {
			continue
		}
		if !found || len(indices) < len(best.Indices) ||
			(len(indices) == len(best.Indices) && lexLess(indices, best.Indices)) {
			best = Result{
				Indices:  indices,
				Points:   pts,
				Error:    d,
				Matching: matching,
				Optimal:  true,
			}
			found = true
		}
	}
	if !found {
		// Unreachable for eps >= 0: the full polyline has error 0.
		return Result{}, errors.New("polysimp: no feasible subsequence")
	}
	return best, nil
}

func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
