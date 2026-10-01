// Package alldiff implements Régin's filtering algorithm for the
// AllDifferent global constraint.
//
// The constraint AllDifferent(x1..xn) requires every pair of variables to
// take distinct values. Régin's algorithm models the constraint as a
// bipartite graph between variables and values, computes a maximum
// matching, and then uses the strongly connected components of the residual
// digraph to remove every edge (variable-value assignment) that cannot
// appear in any maximum matching — i.e. in any feasible solution.
//
// Reference: J.-C. Régin, "A Filtering Algorithm for Constraints of
// Difference in CSPs", AAAI-94.
package alldiff

import (
	"fmt"
	"sort"
)

// Variable is one constrained variable with its current finite domain.
type Variable struct {
	Name   string
	Domain []int
}

// Edge is a variable-value assignment possibility.
type Edge struct {
	Variable string
	Value    int
}

// Result reports the outcome of one propagation round.
type Result struct {
	// Feasible is false when no matching saturating all variables exists;
	// the constraint is inconsistent before any search is performed.
	Feasible bool
	// Reason explains infeasibility (empty when feasible).
	Reason string
	// Matching is a maximum variable-value matching (only when feasible).
	Matching []Edge
	// Domains holds the pruned domain of every variable (only when feasible).
	Domains map[string][]int
	// Removed lists the edges eliminated by the SCC filtering phase.
	Removed []Edge
}

// Propagate runs Régin's algorithm on AllDifferent(vars...).
//
// Phase 1 (matching): find a maximum matching in the variable-value
// bipartite graph. If it covers fewer than len(vars) variables the
// constraint is infeasible and Propagate reports failure immediately,
// before any search.
//
// Phase 2 (filtering): orient matched edges value->variable and unmatched
// edges variable->value, add a dummy node closing alternating paths that
// start at free values, and compute strongly connected components. An edge
// is kept iff it belongs to the matching or its endpoints share an SCC.
func Propagate(vars []Variable) *Result {
	n := len(vars)

	// Collect the union of all domains as the right-hand vertex set.
	valueSet := make(map[int]bool)
	for _, v := range vars {
		for _, val := range v.Domain {
			valueSet[val] = true
		}
	}
	values := make([]int, 0, len(valueSet))
	for val := range valueSet {
		values = append(values, val)
	}
	sort.Ints(values)
	valueIndex := make(map[int]int, len(values))
	for i, val := range values {
		valueIndex[val] = i
	}
	m := len(values)

	// Bipartite adjacency: variable i -> value-node indices.
	adj := make([][]int, n)
	for i, v := range vars {
		seen := make(map[int]bool)
		for _, val := range v.Domain {
			j := valueIndex[val]
			if !seen[j] {
				seen[j] = true
				adj[i] = append(adj[i], j)
			}
		}
		sort.Ints(adj[i])
	}

	// ---- Phase 1: maximum matching (Hopcroft-Karp). ----
	matchVar, matchVal, size := hopcroftKarp(adj, n, m)
	if size < n {
		return &Result{
			Feasible: false,
			Reason: fmt.Sprintf(
				"no matching saturates all %d variables: maximum matching covers %d variables over %d distinct values (Hall set violated)",
				n, size, m),
		}
	}

	res := &Result{
		Feasible: true,
		Domains:  make(map[string][]int, n),
	}
	for i, v := range vars {
		res.Matching = append(res.Matching, Edge{Variable: v.Name, Value: values[matchVar[i]]})
	}

	// ---- Phase 2: SCC-based filtering on the residual digraph. ----
	// Node layout: [0, n) variables, [n, n+m) values, n+m dummy.
	dummy := n + m
	digraph := make([][]int, n+m+1)
	for i := 0; i < n; i++ {
		for _, j := range adj[i] {
			if matchVar[i] == j {
				// Matched edge: oriented value -> variable.
				digraph[n+j] = append(digraph[n+j], i)
			} else {
				// Unmatched edge: oriented variable -> value.
				digraph[i] = append(digraph[i], j+n)
			}
		}
	}
	// Dummy node turns alternating paths from free values into cycles.
	for j := 0; j < m; j++ {
		if matchVal[j] == -1 {
			digraph[n+j] = append(digraph[n+j], dummy)
		}
	}
	for i := 0; i < n; i++ {
		digraph[dummy] = append(digraph[dummy], i)
	}

	scc := tarjanSCC(digraph)

	// Keep an edge iff it is matched or its endpoints share an SCC.
	for i, v := range vars {
		kept := make([]int, 0, len(adj[i]))
		for _, j := range adj[i] {
			if matchVar[i] == j || scc[i] == scc[n+j] {
				kept = append(kept, values[j])
			} else {
				res.Removed = append(res.Removed, Edge{Variable: v.Name, Value: values[j]})
			}
		}
		res.Domains[v.Name] = kept
	}
	return res
}

// hopcroftKarp computes a maximum matching between n left vertices and
// m right vertices. adj[u] lists the right neighbours of left vertex u.
// It returns the matching from both sides and its size.
func hopcroftKarp(adj [][]int, n, m int) (matchLeft, matchRight []int, size int) {
	matchLeft = make([]int, n)
	matchRight = make([]int, m)
	for i := range matchLeft {
		matchLeft[i] = -1
	}
	for j := range matchRight {
		matchRight[j] = -1
	}
	dist := make([]int, n)

	bfs := func() bool {
		queue := make([]int, 0, n)
		found := false
		for u := 0; u < n; u++ {
			if matchLeft[u] == -1 {
				dist[u] = 0
				queue = append(queue, u)
			} else {
				dist[u] = -1
			}
		}
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			for _, v := range adj[u] {
				w := matchRight[v]
				if w == -1 {
					found = true
				} else if dist[w] == -1 {
					dist[w] = dist[u] + 1
					queue = append(queue, w)
				}
			}
		}
		return found
	}

	var dfs func(u int) bool
	dfs = func(u int) bool {
		for _, v := range adj[u] {
			w := matchRight[v]
			if w == -1 || (dist[w] == dist[u]+1 && dfs(w)) {
				matchLeft[u] = v
				matchRight[v] = u
				return true
			}
		}
		dist[u] = -1
		return false
	}

	for bfs() {
		for u := 0; u < n; u++ {
			if matchLeft[u] == -1 && dfs(u) {
				size++
			}
		}
	}
	return matchLeft, matchRight, size
}

// tarjanSCC returns, for every vertex of the directed graph, the index of
// its strongly connected component.
func tarjanSCC(g [][]int) []int {
	n := len(g)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	comp := make([]int, n)
	for i := range index {
		index[i] = -1
		comp[i] = -1
	}
	var stack []int
	counter := 0
	ncomp := 0

	var strongconnect func(v int)
	strongconnect = func(v int) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range g[v] {
			if index[w] == -1 {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = ncomp
				if w == v {
					break
				}
			}
			ncomp++
		}
	}

	for v := 0; v < n; v++ {
		if index[v] == -1 {
			strongconnect(v)
		}
	}
	return comp
}
