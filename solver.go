package main

import "math"

// Group is one contiguous segment of gray levels mapped to a single
// palette representative.
type Group struct {
	From, To int   // gray-level range [From, To] covered by this group
	Rep      int   // palette representative (integer gray value)
	Pixels   int64 // total pixels in the group
	MaxErr   int   // max |level - Rep| over non-empty levels in the group
	SSE      int64 // weighted squared error of the group
}

// Solution is the lexicographically optimal quantization:
// minimal max absolute error first, minimal weighted SSE second.
type Solution struct {
	MaxErr int
	SSE    int64
	Groups []Group
}

// Solve partitions gray levels 0..len(w)-1 with histogram weights w into k
// contiguous groups, each assigned an integer representative. It
// lexicographically minimizes (max absolute error, weighted SSE).
// Empty levels (w[i] == 0) carry no error and are absorbed by whichever
// group contains them. If k exceeds the number of non-empty levels it is
// clamped, since every non-empty level can then be its own group (error 0).
func Solve(w []int64, k int) Solution {
	n := len(w)
	m := 0
	for _, c := range w {
		if c > 0 {
			m++
		}
	}
	if m == 0 || n == 0 {
		return Solution{}
	}
	if k < 1 {
		k = 1
	}
	if k > m {
		k = m
	}

	// next[i] = first non-empty level >= i (n if none);
	// prev[i] = last non-empty level <= i (-1 if none).
	next := make([]int, n+1)
	next[n] = n
	for i := n - 1; i >= 0; i-- {
		if w[i] > 0 {
			next[i] = i
		} else {
			next[i] = next[i+1]
		}
	}
	prev := make([]int, n)
	last := -1
	for i := 0; i < n; i++ {
		if w[i] > 0 {
			last = i
		}
		prev[i] = last
	}

	// Prefix sums of weights, weighted levels, weighted squared levels.
	sw := make([]int64, n+1)
	s1 := make([]int64, n+1)
	s2 := make([]int64, n+1)
	for i := 0; i < n; i++ {
		sw[i+1] = sw[i] + w[i]
		s1[i+1] = s1[i] + w[i]*int64(i)
		s2[i+1] = s2[i] + w[i]*int64(i)*int64(i)
	}

	// bounds returns the first/last non-empty level inside segment [a,b].
	bounds := func(a, b int) (int, int, bool) {
		lo := next[a]
		if lo > b {
			return 0, 0, false
		}
		return lo, prev[b], true
	}

	// segMaxErr is the minimal achievable max abs error of segment [a,b]:
	// ceil((hi-lo)/2), attained by an integer representative midway.
	segMaxErr := func(a, b int) (int, bool) {
		lo, hi, ok := bounds(a, b)
		if !ok {
			return 0, false
		}
		return (hi - lo + 1) / 2, true
	}

	// segRep returns the SSE-minimizing integer representative of segment
	// [a,b] subject to max abs error <= limit, plus its weighted SSE.
	segRep := func(a, b, limit int) (int, int64, bool) {
		lo, hi, ok := bounds(a, b)
		if !ok {
			return 0, 0, false
		}
		rlo := hi - limit
		if rlo < 0 {
			rlo = 0
		}
		rhi := lo + limit
		if rhi > n-1 {
			rhi = n - 1
		}
		if rlo > rhi {
			// No integer representative can keep this segment's max
			// abs error within limit.
			return 0, 0, false
		}
		cnt := sw[b+1] - sw[a]
		sum1 := s1[b+1] - s1[a]
		sum2 := s2[b+1] - s2[a]
		sse := func(r int) int64 {
			return sum2 - 2*int64(r)*sum1 + int64(r)*int64(r)*cnt
		}
		// Unconstrained integer minimizer is round(mean); check the two
		// integers bracketing the exact mean, clamped to [rlo, rhi].
		q := int(sum1 / cnt)
		best, bestCost := -1, int64(math.MaxInt64)
		for _, r := range []int{q, q + 1} {
			if r < rlo {
				r = rlo
			}
			if r > rhi {
				r = rhi
			}
			if c := sse(r); c < bestCost {
				bestCost, best = c, r
			}
		}
		return best, bestCost, true
	}

	const INF = int(1 << 30)

	// Phase 1: exact DP for the minimal achievable max abs error M.
	f := make([][]int, k+1)
	for t := range f {
		f[t] = make([]int, n+1)
		for i := range f[t] {
			f[t][i] = INF
		}
	}
	f[0][0] = 0
	for t := 1; t <= k; t++ {
		for i := 1; i <= n; i++ {
			best := INF
			for j := 0; j < i; j++ {
				if f[t-1][j] == INF {
					continue
				}
				c, ok := segMaxErr(j, i-1)
				if !ok {
					continue
				}
				if f[t-1][j] > c {
					c = f[t-1][j]
				}
				if c < best {
					best = c
				}
			}
			f[t][i] = best
		}
	}
	M := f[k][n]

	// Phase 2: exact DP for minimal total SSE, every segment constrained
	// to max abs error <= M.
	g := make([][]int64, k+1)
	par := make([][]int, k+1)
	for t := range g {
		g[t] = make([]int64, n+1)
		par[t] = make([]int, n+1)
		for i := range g[t] {
			g[t][i] = int64(math.MaxInt64)
			par[t][i] = -1
		}
	}
	g[0][0] = 0
	for t := 1; t <= k; t++ {
		for i := 1; i <= n; i++ {
			for j := 0; j < i; j++ {
				if g[t-1][j] == int64(math.MaxInt64) {
					continue
				}
				_, c, ok := segRep(j, i-1, M)
				if !ok {
					continue
				}
				if v := g[t-1][j] + c; v < g[t][i] {
					g[t][i] = v
					par[t][i] = j
				}
			}
		}
	}

	// Backtrack the optimal partition and rebuild group stats.
	groups := make([]Group, 0, k)
	i := n
	for t := k; t >= 1; t-- {
		j := par[t][i]
		rep, sse, _ := segRep(j, i-1, M)
		lo, hi, _ := bounds(j, i-1)
		me := iabs(rep - lo)
		if d := iabs(hi - rep); d > me {
			me = d
		}
		groups = append(groups, Group{
			From:   j,
			To:     i - 1,
			Rep:    rep,
			Pixels: sw[i] - sw[j],
			MaxErr: me,
			SSE:    sse,
		})
		i = j
	}
	for a, b := 0, len(groups)-1; a < b; a, b = a+1, b-1 {
		groups[a], groups[b] = groups[b], groups[a]
	}

	return Solution{MaxErr: M, SSE: g[k][n], Groups: groups}
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
