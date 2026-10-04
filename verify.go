package ssp

import (
	"fmt"
	"math"
)

// EnumerateSimpleCycles returns every simple directed cycle of the graph as
// a vertex-id slice in traversal order (the closing hop back to the first
// vertex is implied). Each cycle is reported once, rotated so its smallest
// vertex id comes first. Self-loops appear as single-vertex cycles.
//
// This is exponential and intended for cross-checking small graphs only.
func EnumerateSimpleCycles(g *Graph) [][]int {
	n := g.NumVertices()
	var cycles [][]int
	for start := 0; start < n; start++ {
		// DFS from start, visiting only vertices >= start so each cycle is
		// found exactly once (anchored at its smallest vertex).
		visited := make([]bool, n)
		visited[start] = true
		path := []int{start}
		var dfs func(u int)
		dfs = func(u int) {
			for _, ei := range g.Adj[u] {
				v := g.Edges[ei].To
				if v == start {
					cyc := make([]int, len(path))
					copy(cyc, path)
					cycles = append(cycles, cyc)
				} else if v > start && !visited[v] {
					visited[v] = true
					path = append(path, v)
					dfs(v)
					path = path[:len(path)-1]
					visited[v] = false
				}
			}
		}
		dfs(start)
	}
	return cycles
}

// CycleWeight returns the minimum total weight with which the given vertex
// cycle can be realised: for every hop the cheapest parallel edge is taken,
// so the cycle is "negative-capable" iff this minimum is negative.
func CycleWeight(g *Graph, cycle []int) float64 {
	total := 0.0
	for i, u := range cycle {
		v := cycle[(i+1)%len(cycle)]
		w, ok := g.MinEdgeWeight(u, v)
		if !ok {
			return math.Inf(1)
		}
		total += w
	}
	return total
}

// BruteForceStatus independently derives the expected status of target t
// for source src by enumerating all simple cycles: t is NegInf iff some
// negative simple cycle is reachable from src and can reach t.
func BruteForceStatus(g *Graph, src, t int) Status {
	reachFromSrc := g.Reachable(src)
	if !reachFromSrc[t] {
		return Unreachable
	}
	canReachT := g.CanReach(t)
	for _, cyc := range EnumerateSimpleCycles(g) {
		if CycleWeight(g, cyc) >= 0 {
			continue // zero and positive cycles never pollute results
		}
		for _, v := range cyc {
			if reachFromSrc[v] && canReachT[v] {
				return NegInf
			}
		}
	}
	return Finite
}

// CrossCheck compares the Bellman-Ford result for src against brute-force
// simple-cycle enumeration for every target, returning a list of mismatch
// descriptions (empty when everything agrees).
func CrossCheck(g *Graph, src int, res *Result) []string {
	var problems []string
	for t := 0; t < g.NumVertices(); t++ {
		want := BruteForceStatus(g, src, t)
		got := res.Status[t]
		if want != got {
			problems = append(problems, fmt.Sprintf(
				"source %s target %s: bellman-ford=%s brute-force=%s",
				g.Names[src], g.Names[t], got, want))
		}
	}
	return problems
}

// FiniteStepDP recomputes shortest distances with an independent k-step
// dynamic program: dp[v] after round k is the minimum weight of any walk
// from src to v using at most k edges. After n-1 rounds it matches the
// Bellman-Ford distances for every vertex not affected by a negative cycle.
func FiniteStepDP(g *Graph, src int) []float64 {
	n := g.NumVertices()
	dp := make([]float64, n)
	for i := range dp {
		dp[i] = math.Inf(1)
	}
	dp[src] = 0
	for round := 0; round < n-1; round++ {
		next := make([]float64, n)
		copy(next, dp)
		for _, e := range g.Edges {
			if math.IsInf(dp[e.From], 1) {
				continue
			}
			if nd := dp[e.From] + e.Weight; nd < next[e.To] {
				next[e.To] = nd
			}
		}
		dp = next
	}
	return dp
}
