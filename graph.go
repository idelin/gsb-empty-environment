// Package cfgdom builds control-flow graphs and computes immediate
// dominators. Graphs may contain nodes that are unreachable from the
// entry; such nodes simply have no dominator information.
package cfgdom

import "sort"

// Graph is a directed control-flow graph with a single entry node.
// It may contain nodes unreachable from the entry.
type Graph struct {
	entry int
	nodes map[int]struct{}
	succ  map[int][]int
	pred  map[int][]int
}

// NewGraph returns an empty graph whose entry node is entry.
func NewGraph(entry int) *Graph {
	g := &Graph{
		entry: entry,
		nodes: make(map[int]struct{}),
		succ:  make(map[int][]int),
		pred:  make(map[int][]int),
	}
	g.nodes[entry] = struct{}{}
	return g
}

// Entry returns the entry node of the graph.
func (g *Graph) Entry() int { return g.entry }

// AddNode adds a node to the graph. Adding an existing node is a no-op.
func (g *Graph) AddNode(id int) { g.nodes[id] = struct{}{} }

// AddEdge adds a directed edge from -> to, creating the endpoint nodes
// if necessary. Duplicate edges are ignored. The graph may be re-analysed
// after edges are added.
func (g *Graph) AddEdge(from, to int) {
	g.AddNode(from)
	g.AddNode(to)
	for _, s := range g.succ[from] {
		if s == to {
			return
		}
	}
	g.succ[from] = append(g.succ[from], to)
	g.pred[to] = append(g.pred[to], from)
}

// Nodes returns all node ids in ascending order, including unreachable ones.
func (g *Graph) Nodes() []int {
	out := make([]int, 0, len(g.nodes))
	for n := range g.nodes {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// Successors returns the direct successors of n.
func (g *Graph) Successors(n int) []int { return g.succ[n] }

// Predecessors returns the direct predecessors of n.
func (g *Graph) Predecessors(n int) []int { return g.pred[n] }
