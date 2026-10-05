package main

import "math/bits"

// enumResult holds the optimum over all 2^n candidate subsets.
type enumResult struct {
	total    uint64
	feasible uint64
	hasBest  bool
	bestCost int64
	bestCnt  int
	bestPts  []int64
}

// enumerate checks every subset of the candidate set and returns the optimum
// under the same objective order as the solver: cost, count, lexicographic.
// Coverage is tracked incrementally with interval bitsets (Gray-code style
// lowbit DP over masks).
func enumerate(xs, cost []int64, ivs []Interval) enumResult {
	n := len(xs)
	m := len(ivs)
	w := (m + 63) / 64
	if w == 0 {
		w = 1
	}
	full := make([]uint64, w)
	for i := 0; i < m; i++ {
		full[i/64] |= 1 << uint(i%64)
	}
	cbits := make([][]uint64, n)
	for j, x := range xs {
		b := make([]uint64, w)
		for i, iv := range ivs {
			if iv.L <= x && x <= iv.R {
				b[i/64] |= 1 << uint(i%64)
			}
		}
		cbits[j] = b
	}
	total := uint64(1) << uint(n)
	w64 := uint64(w)
	cov := make([]uint64, total*w64)
	costSum := make([]int64, total)
	cnt := make([]int32, total)
	res := enumResult{total: total}
	var bestMask uint64
	for mask := uint64(0); mask < total; mask++ {
		row := cov[mask*w64 : mask*w64+w64]
		if mask > 0 {
			lb := mask & -mask
			bit := bits.TrailingZeros64(lb)
			prev := mask ^ lb
			prow := cov[prev*w64 : prev*w64+w64]
			for t := 0; t < w; t++ {
				row[t] = prow[t] | cbits[bit][t]
			}
			costSum[mask] = costSum[prev] + cost[bit]
			cnt[mask] = cnt[prev] + 1
		}
		ok := true
		for t := 0; t < w; t++ {
			if row[t] != full[t] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		res.feasible++
		c := costSum[mask]
		k := int(cnt[mask])
		if !res.hasBest || c < res.bestCost || (c == res.bestCost && k < res.bestCnt) ||
			(c == res.bestCost && k == res.bestCnt && maskLexLess(mask, bestMask, n)) {
			res.hasBest = true
			res.bestCost = c
			res.bestCnt = k
			bestMask = mask
		}
	}
	if res.hasBest {
		for j := 0; j < n; j++ {
			if bestMask>>uint(j)&1 == 1 {
				res.bestPts = append(res.bestPts, xs[j])
			}
		}
	}
	return res
}

// maskLexLess compares two subsets by the lexicographic order of their sorted
// coordinate lists (xs is sorted ascending, so bit index order matches).
func maskLexLess(a, b uint64, n int) bool {
	for j := 0; j < n; j++ {
		ai := a >> uint(j) & 1
		bi := b >> uint(j) & 1
		if ai != bi {
			return ai == 1
		}
	}
	return false
}
