package dagpath

import "fmt"

// Edge is one directed edge carrying a string fragment. The fragment may be
// empty. IDs need not be unique, but unique IDs give a total order on paths.
type Edge struct {
	ID    int
	From  int
	To    int
	Label string
}

// Graph is a directed acyclic graph with string-labeled edges. Nodes are
// numbered 0..NumNodes-1.
type Graph struct {
	NumNodes int
	Edges    []Edge

	out [][]int // out[u] = indices of edges leaving u
	in  [][]int // in[v]  = indices of edges entering v
}

// NewGraph validates the edge list, builds adjacency, and checks acyclicity.
func NewGraph(numNodes int, edges []Edge) (*Graph, error) {
	if numNodes < 0 {
		return nil, fmt.Errorf("dagpath: negative node count %d", numNodes)
	}
	g := &Graph{
		NumNodes: numNodes,
		Edges:    append([]Edge(nil), edges...),
		out:      make([][]int, numNodes),
		in:       make([][]int, numNodes),
	}
	for i, e := range g.Edges {
		if e.From < 0 || e.From >= numNodes || e.To < 0 || e.To >= numNodes {
			return nil, fmt.Errorf("dagpath: edge %d (%d -> %d) out of range [0,%d)", i, e.From, e.To, numNodes)
		}
		g.out[e.From] = append(g.out[e.From], i)
		g.in[e.To] = append(g.in[e.To], i)
	}
	if _, err := g.topoOrder(); err != nil {
		return nil, err
	}
	return g, nil
}

// topoOrder returns a topological ordering of all nodes (Kahn's algorithm).
func (g *Graph) topoOrder() ([]int, error) {
	indeg := make([]int, g.NumNodes)
	for _, e := range g.Edges {
		indeg[e.To]++
	}
	queue := make([]int, 0, g.NumNodes)
	for u := 0; u < g.NumNodes; u++ {
		if indeg[u] == 0 {
			queue = append(queue, u)
		}
	}
	order := make([]int, 0, g.NumNodes)
	for len(queue) > 0 {
		u := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		order = append(order, u)
		for _, ei := range g.out[u] {
			v := g.Edges[ei].To
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if len(order) != g.NumNodes {
		return nil, fmt.Errorf("dagpath: graph contains a cycle")
	}
	return order, nil
}
