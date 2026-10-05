package frsimp

import "math"

// MatchPair is one pair of a monotone coupling: Orig is an index into the
// original polyline, Simp is a position within the simplified index sequence
// (i.e. the retained vertex Indices[Simp]).
type MatchPair struct {
	Orig int
	Simp int
}

// DiscreteFrechet computes the discrete Frechet distance between polylines a
// and b under metric, together with a monotone coupling witnessing it.
//
// The returned coupling is a sequence of index pairs (i, j) into a and b that
// starts at (0, 0), ends at (len(a)-1, len(b)-1), advances by steps in
// {(1,0), (0,1), (1,1)}, and whose maximum point distance equals the returned
// distance. It is nil if either polyline is empty.
func DiscreteFrechet(a, b []Point, metric Metric) (float64, []MatchPair) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	if metric == nil {
		metric = Euclidean
	}
	n, m := len(a), len(b)
	ca := make([][]float64, n)
	for i := range ca {
		ca[i] = make([]float64, m)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < m; j++ {
			d := metric(a[i], b[j])
			best := math.Inf(1)
			if i > 0 && ca[i-1][j] < best {
				best = ca[i-1][j]
			}
			if j > 0 && ca[i][j-1] < best {
				best = ca[i][j-1]
			}
			if i > 0 && j > 0 && ca[i-1][j-1] < best {
				best = ca[i-1][j-1]
			}
			if i == 0 && j == 0 {
				best = 0
			}
			ca[i][j] = math.Max(d, best)
		}
	}
	// Backtrack a witness coupling, preferring diagonal steps on ties.
	var rev []MatchPair
	i, j := n-1, m-1
	for {
		rev = append(rev, MatchPair{Orig: i, Simp: j})
		if i == 0 && j == 0 {
			break
		}
		bi, bj := -1, -1
		best := math.Inf(1)
		consider := func(pi, pj int) {
			if pi >= 0 && pj >= 0 && ca[pi][pj] < best {
				best = ca[pi][pj]
				bi, bj = pi, pj
			}
		}
		consider(i-1, j-1)
		consider(i-1, j)
		consider(i, j-1)
		i, j = bi, bj
	}
	matching := make([]MatchPair, len(rev))
	for t, p := range rev {
		matching[len(rev)-1-t] = p
	}
	return ca[n-1][m-1], matching
}
