// Package flowgraph analyzes control-flow graphs that may contain
// unreachable nodes. It computes immediate dominators relative to a
// single entry node.
package flowgraph

// Graph is a directed flow graph with nodes numbered 0..NumNodes()-1.
// One node is designated as the entry. Nodes unreachable from the entry
// are permitted and simply have no dominators.
type Graph struct {
	numNodes int
	entry    int
	succ     [][]int
	pred     [][]int
}

// New returns a Graph with numNodes nodes (0..numNodes-1) and the given
// entry node. It panics if entry is out of range.
func New(numNodes, entry int) *Graph {
	if entry < 0 || entry >= numNodes {
		panic("flowgraph: entry out of range")
	}
	return &Graph{
		numNodes: numNodes,
		entry:    entry,
		succ:     make([][]int, numNodes),
		pred:     make([][]int, numNodes),
	}
}

// NumNodes returns the number of nodes in the graph.
func (g *Graph) NumNodes() int { return g.numNodes }

// Entry returns the entry node.
func (g *Graph) Entry() int { return g.entry }

// AddEdge adds a directed edge from -> to. It panics if either endpoint
// is out of range. Duplicate edges are ignored. The graph may be
// re-analyzed after adding edges.
func (g *Graph) AddEdge(from, to int) {
	if from < 0 || from >= g.numNodes || to < 0 || to >= g.numNodes {
		panic("flowgraph: edge endpoint out of range")
	}
	for _, s := range g.succ[from] {
		if s == to {
			return
		}
	}
	g.succ[from] = append(g.succ[from], to)
	g.pred[to] = append(g.pred[to], from)
}

// Successors returns the successor nodes of n.
func (g *Graph) Successors(n int) []int { return g.succ[n] }

// Predecessors returns the predecessor nodes of n.
func (g *Graph) Predecessors(n int) []int { return g.pred[n] }
