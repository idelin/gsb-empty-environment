package main

import (
	"fmt"
	"sort"
	"strings"
)

// SolveResult holds every minimum s-t cut found within the budget.
type SolveResult struct {
	Version  string
	MaxFlow  int64
	Cuts     [][]string // lexicographically ordered, deduplicated node sets
	Complete bool       // false when the budget truncated the enumeration
}

// solve computes a maximum flow and enumerates all minimum s-t cuts.
//
// By the Picard-Queyranne theorem the minimum s-t cuts correspond
// one-to-one to the closed sets of the residual DAG of strongly
// connected components of ANY maximum flow. Working on the component
// quotient collapses the different residual-path representations of one
// and the same cut into a single closed set; results are additionally
// deduplicated by their canonical node-set key as a safety net.
func solve(g *Graph, budget int) (*SolveResult, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	nodes := g.NodeList()
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n] = i
	}
	n := len(nodes)
	s, t := idx[g.Source], idx[g.Sink]

	d := newDinic(n)
	for _, e := range g.Edges {
		d.addEdge(idx[e.From], idx[e.To], e.Capacity)
	}
	flow := d.maxFlow(s, t)

	// Residual graph of the computed maximum flow.
	residual := make([][]int, n)
	for u := 0; u < n; u++ {
		for _, e := range d.g[u] {
			if e.cap > 0 {
				residual[u] = append(residual[u], e.to)
			}
		}
	}
	comp := stronglyConnectedComponents(n, residual)
	nComp := 0
	for _, c := range comp {
		if c+1 > nComp {
			nComp = c + 1
		}
	}
	if comp[s] == comp[t] {
		return nil, fmt.Errorf("internal error: source and sink share a residual component")
	}

	// Component DAG (deduplicated successor lists).
	succSet := make([]map[int]bool, nComp)
	for i := range succSet {
		succSet[i] = map[int]bool{}
	}
	for u := 0; u < n; u++ {
		for _, v := range residual[u] {
			cu, cv := comp[u], comp[v]
			if cu != cv {
				succSet[cu][cv] = true
			}
		}
	}
	succ := make([][]int, nComp)
	for c := range succSet {
		for w := range succSet[c] {
			succ[c] = append(succ[c], w)
		}
		sort.Ints(succ[c])
	}
	order := topoOrder(nComp, succ)

	compNodes := make([][]string, nComp)
	for i, name := range nodes {
		compNodes[comp[i]] = append(compNodes[comp[i]], name)
	}

	closedSets, truncated := enumerateClosedSets(nComp, succ, order, comp[s], comp[t], budget)

	// Map component sets to node sets, deduplicate by canonical key.
	seen := make(map[string]bool, len(closedSets))
	cuts := make([][]string, 0, len(closedSets))
	for _, set := range closedSets {
		var names []string
		for _, c := range set {
			names = append(names, compNodes[c]...)
		}
		sort.Strings(names)
		key := strings.Join(names, "\x01")
		if seen[key] {
			continue
		}
		seen[key] = true
		cuts = append(cuts, names)
	}
	sort.Slice(cuts, func(i, j int) bool { return lexLess(cuts[i], cuts[j]) })

	return &SolveResult{
		Version:  g.GraphVersion(),
		MaxFlow:  flow,
		Cuts:     cuts,
		Complete: !truncated,
	}, nil
}

// stronglyConnectedComponents runs Tarjan's algorithm and returns the
// component id of every node.
func stronglyConnectedComponents(n int, adj [][]int) []int {
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	comp := make([]int, n)
	for i := range comp {
		comp[i] = -1
		index[i] = -1
	}
	var stack []int
	counter, nComp := 0, 0
	var visit func(u int)
	visit = func(u int) {
		index[u], low[u] = counter, counter
		counter++
		stack = append(stack, u)
		onStack[u] = true
		for _, v := range adj[u] {
			if index[v] == -1 {
				visit(v)
				if low[v] < low[u] {
					low[u] = low[v]
				}
			} else if onStack[v] && index[v] < low[u] {
				low[u] = index[v]
			}
		}
		if low[u] == index[u] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = nComp
				if w == u {
					break
				}
			}
			nComp++
		}
	}
	for u := 0; u < n; u++ {
		if index[u] == -1 {
			visit(u)
		}
	}
	return comp
}

// topoOrder returns a Kahn topological order of the component DAG.
func topoOrder(nComp int, succ [][]int) []int {
	indeg := make([]int, nComp)
	for _, outs := range succ {
		for _, w := range outs {
			indeg[w]++
		}
	}
	var queue []int
	for c := 0; c < nComp; c++ {
		if indeg[c] == 0 {
			queue = append(queue, c)
		}
	}
	var order []int
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, w := range succ[u] {
			indeg[w]--
			if indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	return order
}

// enumerateClosedSets lists every component set S with comp(s) in S,
// comp(t) not in S, and no DAG edge leaving S. Components are decided in
// reverse topological order so a component may join S exactly when all of
// its successors are already in S. Recording stops once budget sets have
// been produced, in which case truncated is true.
func enumerateClosedSets(nComp int, succ [][]int, order []int, srcComp, sinkComp, budget int) (sets [][]int, truncated bool) {
	chosen := make([]bool, nComp)
	var rec func(i int)
	rec = func(i int) {
		if truncated {
			return
		}
		if i < 0 {
			if budget >= 0 && len(sets) >= budget {
				truncated = true
				return
			}
			var set []int
			for c := 0; c < nComp; c++ {
				if chosen[c] {
					set = append(set, c)
				}
			}
			sets = append(sets, set)
			return
		}
		v := order[i]
		switch v {
		case srcComp:
			// Forced into every cut, but only feasible when all of
			// its successors are already inside the set.
			for _, w := range succ[v] {
				if !chosen[w] {
					return
				}
			}
			chosen[v] = true
			rec(i - 1)
			chosen[v] = false
		case sinkComp:
			rec(i - 1)
		default:
			rec(i - 1) // exclude v
			ok := true
			for _, w := range succ[v] {
				if !chosen[w] {
					ok = false
					break
				}
			}
			if ok {
				chosen[v] = true
				rec(i - 1)
				chosen[v] = false
			}
		}
	}
	rec(len(order) - 1)
	return sets, truncated
}

// lexLess orders node sets lexicographically: element-wise on the sorted
// names, shorter prefix first.
func lexLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
