// Package dom implements dominance analysis for control-flow graphs
// that have a single designated entry node.
//
// The package offers:
//   - Graph: a directed graph with duplicate-edge suppression.
//   - Analysis: dominator queries, the dominator tree, unreachable
//     node reporting, incremental recomputation after edge updates,
//     and verification of dominator-path evidence.
//   - DominatesByRemoval: an independent checker that validates the
//     analysis by deleting a candidate dominator and re-testing
//     reachability from the entry.
package dom

import "sort"

// Graph is a directed graph with a single entry node. Parallel
// (duplicate) edges are silently ignored: the edge set is stored as
// adjacency sets, so inserting the same edge twice is a no-op.
type Graph struct {
	entry int
	nodes map[int]struct{}
	succ  map[int]map[int]struct{}
	pred  map[int]map[int]struct{}
}

// NewGraph returns an empty graph whose entry node is entry.
func NewGraph(entry int) *Graph {
	g := &Graph{
		entry: entry,
		nodes: make(map[int]struct{}),
		succ:  make(map[int]map[int]struct{}),
		pred:  make(map[int]map[int]struct{}),
	}
	g.AddNode(entry)
	return g
}

// AddNode inserts n into the graph. It is safe to add a node twice.
func (g *Graph) AddNode(n int) {
	if _, ok := g.nodes[n]; ok {
		return
	}
	g.nodes[n] = struct{}{}
	g.succ[n] = make(map[int]struct{})
	g.pred[n] = make(map[int]struct{})
}

// AddEdge inserts the edge from->to, creating endpoint nodes as
// needed. It reports whether the edge was newly inserted; a duplicate
// edge returns false and leaves the graph unchanged. Self-loops
// (from == to) are allowed.
func (g *Graph) AddEdge(from, to int) bool {
	g.AddNode(from)
	g.AddNode(to)
	if _, dup := g.succ[from][to]; dup {
		return false
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return true
}

// RemoveEdge deletes the edge from->to. It reports whether the edge
// existed.
func (g *Graph) RemoveEdge(from, to int) bool {
	if !g.HasEdge(from, to) {
		return false
	}
	delete(g.succ[from], to)
	delete(g.pred[to], from)
	return true
}

// HasEdge reports whether the edge from->to exists.
func (g *Graph) HasEdge(from, to int) bool {
	_, ok := g.succ[from][to]
	return ok
}

// Entry returns the designated entry node.
func (g *Graph) Entry() int { return g.entry }

// Nodes returns all node IDs in ascending order.
func (g *Graph) Nodes() []int {
	out := make([]int, 0, len(g.nodes))
	for n := range g.nodes {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// Successors returns the successors of n in ascending order.
func (g *Graph) Successors(n int) []int { return sortedKeys(g.succ[n]) }

// Predecessors returns the predecessors of n in ascending order.
func (g *Graph) Predecessors(n int) []int { return sortedKeys(g.pred[n]) }

func sortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
