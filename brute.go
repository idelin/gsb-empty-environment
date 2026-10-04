package main

import "sort"

// cutCapacity independently computes the capacity of a cut (given as an
// ascending list of 1-based vertices) directly from the original edges:
// the sum of capacities of arcs leaving the set.
func cutCapacity(g *Graph, cut []int) int64 {
	in := make([]bool, g.N)
	for _, v := range cut {
		in[v-1] = true
	}
	var total int64
	for _, e := range g.Edges {
		if in[e.U] && !in[e.V] {
			total += e.C
		}
	}
	return total
}

// bruteForceMinCuts enumerates all 2^(n-2) subsets containing s and not t,
// computes each cut capacity from scratch, and returns the minimum capacity
// together with every cut attaining it, sorted lexicographically. Used as
// an independent cross-check on small graphs.
func bruteForceMinCuts(g *Graph) (int64, [][]int) {
	others := make([]int, 0, g.N-2)
	for v := 0; v < g.N; v++ {
		if v != g.S && v != g.T {
			others = append(others, v)
		}
	}
	k := len(others)
	best := int64(-1)
	var cuts [][]int
	in := make([]bool, g.N)
	for mask := 0; mask < 1<<uint(k); mask++ {
		for i := range in {
			in[i] = false
		}
		in[g.S] = true
		cut := []int{g.S + 1}
		for i, v := range others {
			if mask>>uint(i)&1 == 1 {
				in[v] = true
				cut = append(cut, v+1)
			}
		}
		var total int64
		for _, e := range g.Edges {
			if in[e.U] && !in[e.V] {
				total += e.C
			}
		}
		if best < 0 || total < best {
			best = total
			cuts = cuts[:0]
		}
		if total == best {
			sorted := make([]int, len(cut))
			copy(sorted, cut)
			sort.Ints(sorted)
			cuts = append(cuts, sorted)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return lexCompare(cuts[i], cuts[j]) < 0 })
	return best, cuts
}

func equalCuts(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if lexCompare(a[i], b[i]) != 0 {
			return false
		}
	}
	return true
}
