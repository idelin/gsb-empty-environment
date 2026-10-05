// Package hgsolve implements a hypergraph derivation solver.
//
// Model: each hyperedge fires only when ALL of its premises are derived,
// producing its target. The same target may be produced by several edges.
// A proof of a target is a DAG of hyperedges grounded in the initial facts;
// its cost is the sum of the costs of the DISTINCT edges it uses, so a proof
// node shared by several descendants is charged only once. Because of
// sharing, a plain per-node shortest-path (tree) computation overestimates
// the optimum; the exact optimum is found by a bounded search over fact-set
// states, bracketed by cheap lower/upper bounds, and cross-checked on small
// instances by exhaustive subset enumeration.
package hgsolve

import "sort"

// Edge is a hyperedge: once every premise in From is derived, To becomes
// derivable. Cost is paid at most once per proof, however often the
// conclusion is reused.
type Edge struct {
	ID   string   `json:"id"`
	From []string `json:"from"`
	To   string   `json:"to"`
	Cost float64  `json:"cost"`
}

// Graph is a compiled hypergraph with node indices for fast set operations.
type Graph struct {
	Edges []Edge

	index  map[string]int
	names  []string
	prem   [][]int // edge -> sorted unique premise node indices
	tgt    []int   // edge -> target node index
	byPrem [][]int // node -> edges using it as a premise
}

// NewGraph compiles edges into a Graph. Edge IDs need not be unique but
// should be, so proofs can name edges unambiguously.
func NewGraph(edges []Edge) *Graph {
	g := &Graph{Edges: edges, index: map[string]int{}}
	nodeIdx := func(name string) int {
		if i, ok := g.index[name]; ok {
			return i
		}
		i := len(g.names)
		g.index[name] = i
		g.names = append(g.names, name)
		return i
	}
	g.prem = make([][]int, len(edges))
	g.tgt = make([]int, len(edges))
	for i, e := range edges {
		seen := map[int]bool{}
		for _, p := range e.From {
			pi := nodeIdx(p)
			if !seen[pi] {
				seen[pi] = true
				g.prem[i] = append(g.prem[i], pi)
			}
		}
		sort.Ints(g.prem[i])
		g.tgt[i] = nodeIdx(e.To)
	}
	g.byPrem = make([][]int, len(g.names))
	for i := range edges {
		for _, p := range g.prem[i] {
			g.byPrem[p] = append(g.byPrem[p], i)
		}
	}
	return g
}

// Has reports whether name is a known node (fact, premise, or target).
func (g *Graph) Has(name string) bool {
	_, ok := g.index[name]
	return ok
}

// Derivable returns the sorted set of all nodes derivable from facts by
// forward chaining. Cyclic edges with no factual grounding never fire, so
// self-supporting cycles are correctly excluded.
func (g *Graph) Derivable(facts []string) []string {
	derived := make([]bool, len(g.names))
	remaining := make([]int, len(g.Edges))
	for i := range g.Edges {
		remaining[i] = len(g.prem[i])
	}
	var queue []int
	for _, f := range facts {
		if i, ok := g.index[f]; ok && !derived[i] {
			derived[i] = true
			queue = append(queue, i)
		}
	}
	// Zero-premise edges fire immediately.
	for i := range g.Edges {
		if remaining[i] == 0 && !derived[g.tgt[i]] {
			derived[g.tgt[i]] = true
			queue = append(queue, g.tgt[i])
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, ei := range g.byPrem[n] {
			remaining[ei]--
			if remaining[ei] == 0 && !derived[g.tgt[ei]] {
				derived[g.tgt[ei]] = true
				queue = append(queue, g.tgt[ei])
			}
		}
	}
	out := []string{}
	for i, ok := range derived {
		if ok {
			out = append(out, g.names[i])
		}
	}
	sort.Strings(out)
	return out
}
