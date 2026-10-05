// Package dom computes dominator relations on control-flow graphs with
// one or more entry nodes, builds dominator trees, supports incremental
// recomputation after single-edge updates, and cross-checks results with
// an independent remove-and-test-reachability method.
package dom

import "sort"

// Graph is a directed control-flow graph. Nodes are identified by ints.
// Duplicate edges are ignored; self loops are allowed. Nodes that are not
// reachable from any entry are reported separately by the analysis.
type Graph struct {
	nodes   map[int]struct{}
	entries map[int]struct{}
	succ    map[int]map[int]struct{}
	pred    map[int]map[int]struct{}
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes:   make(map[int]struct{}),
		entries: make(map[int]struct{}),
		succ:    make(map[int]map[int]struct{}),
		pred:    make(map[int]map[int]struct{}),
	}
}

// AddNode registers n. Nodes are also added implicitly by AddEdge/AddEntry.
func (g *Graph) AddNode(n int) { g.nodes[n] = struct{}{} }

// AddEntry registers n as an entry node. With several entries the analysis
// behaves as if a virtual super-root pointed at every entry.
func (g *Graph) AddEntry(n int) {
	g.AddNode(n)
	g.entries[n] = struct{}{}
}

// AddEdge inserts from->to. It reports whether the edge was new; duplicate
// edges are ignored and return false.
func (g *Graph) AddEdge(from, to int) bool {
	g.AddNode(from)
	g.AddNode(to)
	if g.succ[from] == nil {
		g.succ[from] = make(map[int]struct{})
	}
	if _, dup := g.succ[from][to]; dup {
		return false
	}
	g.succ[from][to] = struct{}{}
	if g.pred[to] == nil {
		g.pred[to] = make(map[int]struct{})
	}
	g.pred[to][from] = struct{}{}
	return true
}

// RemoveEdge deletes from->to and reports whether it existed.
func (g *Graph) RemoveEdge(from, to int) bool {
	if !g.HasEdge(from, to) {
		return false
	}
	delete(g.succ[from], to)
	delete(g.pred[to], from)
	return true
}

// HasEdge reports whether from->to exists.
func (g *Graph) HasEdge(from, to int) bool {
	_, ok := g.succ[from][to]
	return ok
}

// IsEntry reports whether n is an entry node.
func (g *Graph) IsEntry(n int) bool {
	_, ok := g.entries[n]
	return ok
}

// Nodes returns all node IDs in ascending order.
func (g *Graph) Nodes() []int { return sortedKeys(g.nodes) }

// Entries returns all entry node IDs in ascending order.
func (g *Graph) Entries() []int { return sortedKeys(g.entries) }

// Successors returns the successors of n in ascending order.
func (g *Graph) Successors(n int) []int { return sortedKeys(g.succ[n]) }

// Predecessors returns the predecessors of n in ascending order.
func (g *Graph) Predecessors(n int) []int { return sortedKeys(g.pred[n]) }

// reachableSet returns the nodes reachable from the entries, treating
// blocked nodes as removed (they are neither visited nor traversed).
func (g *Graph) reachableSet(blocked map[int]struct{}) map[int]struct{} {
	return g.reachableFrom(g.Entries(), blocked)
}

// reachableFrom returns the nodes reachable from starts, treating blocked
// nodes as removed.
func (g *Graph) reachableFrom(starts []int, blocked map[int]struct{}) map[int]struct{} {
	reach := make(map[int]struct{})
	var stack []int
	push := func(n int) {
		if _, ok := blocked[n]; ok {
			return
		}
		if _, ok := reach[n]; ok {
			return
		}
		reach[n] = struct{}{}
		stack = append(stack, n)
	}
	for _, s := range starts {
		push(s)
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range g.Successors(n) {
			push(s)
		}
	}
	return reach
}

func sortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
