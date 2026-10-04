package main

import "fmt"

// BruteForce solves the feedback vertex set problem by enumerating every
// subset of vertices. It is the ground-truth checker for the exact solver
// and is only feasible for small n (<= 24).
func BruteForce(g *Graph) (cost int64, set []int, err error) {
	if g.N > 24 {
		return 0, nil, fmt.Errorf("brute force limited to n<=24, got %d", g.N)
	}
	found := false
	var bestCost int64
	var bestCount int64
	var bestSet []int
	total := uint64(1) << uint(g.N)
	deleted := make([]bool, g.N)
	for mask := uint64(0); mask < total; mask++ {
		var c int64
		var cnt int64
		for v := 0; v < g.N; v++ {
			del := mask&(uint64(1)<<uint(v)) != 0
			deleted[v] = del
			if del {
				c += g.Cost[v]
				cnt++
			}
		}
		if found {
			if c > bestCost || (c == bestCost && cnt > bestCount) {
				continue
			}
		}
		if !g.IsAcyclicAfter(deleted) {
			continue
		}
		cur := make([]int, 0, cnt)
		for v := 0; v < g.N; v++ {
			if deleted[v] {
				cur = append(cur, v)
			}
		}
		if !found || tupleLess(c, cnt, cur, bestCost, bestCount, bestSet) {
			found = true
			bestCost = c
			bestCount = cnt
			bestSet = cur
		}
	}
	if !found {
		return 0, nil, fmt.Errorf("no feasible set (impossible: deleting all always works)")
	}
	return bestCost, bestSet, nil
}

// tupleLess compares (cost, count, sorted IDs) lexicographically.
func tupleLess(c1, k1 int64, s1 []int, c2, k2 int64, s2 []int) bool {
	if c1 != c2 {
		return c1 < c2
	}
	if k1 != k2 {
		return k1 < k2
	}
	for i := 0; i < len(s1) && i < len(s2); i++ {
		if s1[i] != s2[i] {
			return s1[i] < s2[i]
		}
	}
	return len(s1) < len(s2)
}
