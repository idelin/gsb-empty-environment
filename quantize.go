// Package quant provides exact solvers for small image quantization
// problems: single-image weighted gray-level quantization with integer
// representatives, and multi-frame quantization under a shared palette
// with per-frame migration budgets and error caps.
package quant

import (
	"math/big"
	"sort"
)

// Group is a contiguous run of distinct gray levels mapped to a single
// integer representative.
type Group struct {
	Min    int      // smallest gray level in the group
	Max    int      // largest gray level in the group
	Rep    int      // integer representative; ties resolve to the smaller value
	Weight int64    // total weight of the group
	Levels []int    // distinct gray levels in the group, ascending
	SSE    *big.Int // exact weighted squared error of the group
}

// Result is the outcome of Quantize.
type Result struct {
	Groups []Group
	Rep    map[int]int // gray level -> representative
	SSE    *big.Int    // exact total weighted squared error
}

// Quantize selects k integer representatives for the given gray levels so
// that the total weighted squared error is minimized.
//
// levels holds integer gray values and weights their multiplicities; the
// slices are parallel. Duplicate gray values are merged and zero weights
// are allowed (zero-weight levels still get mapped but contribute no
// error). At most twelve distinct levels are expected, which keeps the
// exact dynamic program and the canonical tie-breaking enumeration cheap.
//
// The optimal clusters of a 1-D squared-error problem are contiguous, so
// the output is a partition of the sorted distinct levels into k (or fewer,
// when k exceeds the number of distinct levels) contiguous groups. Every
// representative is an integer; when two integers give the same group cost
// the smaller one is chosen. When several partitions are equally optimal
// the lexicographically smallest tuple of group start indices is returned.
func Quantize(levels []int, weights []int64, k int) Result {
	if len(levels) != len(weights) {
		panic("quant: levels and weights length mismatch")
	}
	if k < 1 {
		panic("quant: k must be >= 1")
	}
	if len(levels) == 0 {
		return Result{Rep: map[int]int{}, SSE: new(big.Int)}
	}
	for _, w := range weights {
		if w < 0 {
			panic("quant: weights must be non-negative")
		}
	}

	// Merge duplicate colors.
	agg := make(map[int]int64, len(levels))
	for i, g := range levels {
		agg[g] += weights[i]
	}
	xs := make([]int, 0, len(agg))
	for g := range agg {
		xs = append(xs, g)
	}
	sort.Ints(xs)
	m := len(xs)
	w := make([]int64, m)
	for i, g := range xs {
		w[i] = agg[g]
	}
	if k > m {
		k = m
	}

	// Prefix sums of w, w*g and w*g*g as exact big integers.
	p0 := make([]*big.Int, m+1)
	p1 := make([]*big.Int, m+1)
	p2 := make([]*big.Int, m+1)
	p0[0] = new(big.Int)
	p1[0] = new(big.Int)
	p2[0] = new(big.Int)
	for i := 0; i < m; i++ {
		wi := big.NewInt(w[i])
		gi := big.NewInt(int64(xs[i]))
		p0[i+1] = new(big.Int).Add(p0[i], wi)
		p1[i+1] = new(big.Int).Add(p1[i], new(big.Int).Mul(wi, gi))
		p2[i+1] = new(big.Int).Add(p2[i], new(big.Int).Mul(new(big.Int).Mul(wi, gi), gi))
	}
	sum := func(p []*big.Int, a, b int) *big.Int { // [a, b] inclusive
		return new(big.Int).Sub(p[b+1], p[a])
	}

	// groupCost returns the exact minimum SSE over integer representatives
	// for the contiguous group xs[a..b], together with the representative
	// (smaller integer wins ties; empty-weight groups take xs[a]).
	groupCost := func(a, b int) (*big.Int, int) {
		s0 := sum(p0, a, b)
		if s0.Sign() == 0 {
			return new(big.Int), xs[a]
		}
		s1 := sum(p1, a, b)
		s2 := sum(p2, a, b)
		// Candidates around the real centroid s1/s0: floor and ceil.
		q, r := new(big.Int).QuoRem(s1, s0, new(big.Int))
		if r.Sign() < 0 { // floor division (s0 > 0)
			q.Sub(q, big.NewInt(1))
		}
		sse := func(c *big.Int) *big.Int {
			// s2 - 2*c*s1 + c*c*s0
			t := new(big.Int).Set(s2)
			t.Sub(t, new(big.Int).Mul(new(big.Int).Mul(big.NewInt(2), c), s1))
			t.Add(t, new(big.Int).Mul(new(big.Int).Mul(c, c), s0))
			return t
		}
		c0 := new(big.Int).Set(q)
		c1 := new(big.Int).Add(q, big.NewInt(1))
		e0, e1 := sse(c0), sse(c1)
		if e1.Cmp(e0) < 0 {
			return e1, int(c1.Int64())
		}
		return e0, int(c0.Int64())
	}

	// Dynamic programming for the optimal cost.
	// dp[j][i] = min cost of partitioning xs[0..i) into j groups;
	// nil marks an unreachable state.
	dp := make([][]*big.Int, k+1)
	for j := 0; j <= k; j++ {
		dp[j] = make([]*big.Int, m+1)
	}
	dp[0][0] = new(big.Int)
	costCache := make(map[[2]int]*big.Int)
	gc := func(a, b int) *big.Int {
		key := [2]int{a, b}
		if c, ok := costCache[key]; ok {
			return c
		}
		c, _ := groupCost(a, b)
		costCache[key] = c
		return c
	}
	for j := 1; j <= k; j++ {
		for i := j; i <= m; i++ {
			var best *big.Int
			for p := j - 1; p < i; p++ {
				if dp[j-1][p] == nil {
					continue
				}
				cand := new(big.Int).Add(dp[j-1][p], gc(p, i-1))
				if best == nil || cand.Cmp(best) < 0 {
					best = cand
				}
			}
			dp[j][i] = best
		}
	}
	best := dp[k][m]

	// Among all partitions attaining the optimal cost, select the
	// lexicographically smallest tuple of group start indices. With at
	// most twelve levels this enumeration is tiny.
	starts := make([]int, k)
	var bestStarts []int
	var rec func(j, lo int)
	rec = func(j, lo int) {
		if j == k {
			total := new(big.Int)
			for g := 0; g < k; g++ {
				total.Add(total, gc(starts[g], starts[g+1]-1))
			}
			if total.Cmp(best) == 0 {
				cand := append([]int(nil), starts[:k+1]...)
				if bestStarts == nil || lessInts(cand[:k], bestStarts[:k]) {
					bestStarts = cand
				}
			}
			return
		}
		// Need at least (k - j) levels for the remaining groups.
		for s := lo; s <= m-(k-j); s++ {
			starts[j] = s
			rec(j+1, s+1)
		}
	}
	starts = make([]int, k+1)
	starts[k] = m // sentinel end of the last group
	// The first group always starts at index 0.
	rec(1, 1)

	res := Result{Rep: make(map[int]int, m), SSE: best}
	for g := 0; g < k; g++ {
		a, b := bestStarts[g], bestStarts[g+1]-1
		c, rep := groupCost(a, b)
		grp := Group{
			Min:    xs[a],
			Max:    xs[b],
			Rep:    rep,
			Weight: sum(p0, a, b).Int64(),
			Levels: append([]int(nil), xs[a:b+1]...),
			SSE:    c,
		}
		res.Groups = append(res.Groups, grp)
		for _, x := range grp.Levels {
			res.Rep[x] = rep
		}
	}
	return res
}

func lessInts(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
