// Package graphx provides a graph type with parallel edges and attribute
// maps, together with partition-based contraction that preserves edge
// provenance back to the source graph.
package graphx

import "fmt"

// Attrs holds arbitrary edge attributes.
type Attrs map[string]interface{}

// Edge is a single edge. Parallel edges (same endpoints) and self-loops are
// allowed; each edge gets a stable ID equal to its insertion index.
type Edge struct {
	ID    int
	From  int
	To    int
	Attrs Attrs
}

// Graph is a node set plus a multiset of edges. Node and edge IDs are
// integers; edge IDs are assigned sequentially and never reused.
type Graph struct {
	Directed bool

	nodes   map[int]struct{}
	nodeSeq []int
	edges   []Edge
	out     map[int][]int
	in      map[int][]int
}

// New returns an empty graph. Directed graphs distinguish Out and In;
// undirected graphs treat every edge as incident to both endpoints.
func New(directed bool) *Graph {
	return &Graph{
		Directed: directed,
		nodes:    make(map[int]struct{}),
		out:      make(map[int][]int),
		in:       make(map[int][]int),
	}
}

// AddNode inserts a node; re-adding an existing node is a no-op.
func (g *Graph) AddNode(id int) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.nodeSeq = append(g.nodeSeq, id)
}

// HasNode reports whether id is a node of the graph.
func (g *Graph) HasNode(id int) bool {
	_, ok := g.nodes[id]
	return ok
}

// AddEdge inserts an edge and returns its ID. Both endpoints must already
// be nodes of the graph.
func (g *Graph) AddEdge(from, to int, attrs Attrs) (int, error) {
	if !g.HasNode(from) {
		return -1, fmt.Errorf("graphx: AddEdge: unknown node %d", from)
	}
	if !g.HasNode(to) {
		return -1, fmt.Errorf("graphx: AddEdge: unknown node %d", to)
	}
	id := len(g.edges)
	g.edges = append(g.edges, Edge{ID: id, From: from, To: to, Attrs: attrs})
	g.out[from] = append(g.out[from], id)
	g.in[to] = append(g.in[to], id)
	if !g.Directed && from != to {
		g.out[to] = append(g.out[to], id)
		g.in[from] = append(g.in[from], id)
	}
	return id, nil
}

// Nodes returns the node IDs in insertion order.
func (g *Graph) Nodes() []int {
	out := make([]int, len(g.nodeSeq))
	copy(out, g.nodeSeq)
	return out
}

// NumNodes returns the number of nodes.
func (g *Graph) NumNodes() int { return len(g.nodeSeq) }

// NumEdges returns the number of edges.
func (g *Graph) NumEdges() int { return len(g.edges) }

// Edge returns the edge with the given ID.
func (g *Graph) Edge(id int) Edge { return g.edges[id] }

// Edges returns a copy of all edges in ID order.
func (g *Graph) Edges() []Edge {
	out := make([]Edge, len(g.edges))
	copy(out, g.edges)
	return out
}

// Out returns the outgoing edges of a node (all incident edges for
// undirected graphs).
func (g *Graph) Out(id int) []Edge { return g.edgeSlice(g.out[id]) }

// In returns the incoming edges of a node (all incident edges for
// undirected graphs).
func (g *Graph) In(id int) []Edge { return g.edgeSlice(g.in[id]) }

func (g *Graph) edgeSlice(ids []int) []Edge {
	out := make([]Edge, len(ids))
	for i, id := range ids {
		out[i] = g.edges[id]
	}
	return out
}
