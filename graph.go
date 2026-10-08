// Package graphcontract provides a multigraph implementation together with
// partition-based contraction that preserves parallel edges, tracks the
// provenance of every contracted edge back to the original graph, and
// optionally aggregates edge attributes.
package graphcontract

// NodeID identifies a node in a graph.
type NodeID int

// EdgeID identifies an edge in a graph. IDs are assigned sequentially in
// insertion order and are stable for the lifetime of the graph.
type EdgeID int

// Attributes holds numeric edge attributes keyed by name. Attributes of
// merged edges are combined per key by an Aggregator.
type Attributes map[string]float64

// Edge is a single directed (or undirected) edge of a multigraph.
type Edge struct {
	ID    EdgeID
	From  NodeID
	To    NodeID
	Attrs Attributes
}

// Graph is a multigraph: parallel edges between the same pair of nodes are
// allowed and preserved.
type Graph struct {
	directed bool
	nodes    []NodeID
	nodeSet  map[NodeID]struct{}
	edges    []*Edge
}

// New returns an empty graph. If directed is false, edges are treated as
// undirected when merging parallel edges during contraction.
func New(directed bool) *Graph {
	return &Graph{
		directed: directed,
		nodeSet:  make(map[NodeID]struct{}),
	}
}

// Directed reports whether the graph is directed.
func (g *Graph) Directed() bool { return g.directed }

// AddNode adds n to the graph. Adding an existing node is a no-op.
func (g *Graph) AddNode(n NodeID) {
	if _, ok := g.nodeSet[n]; ok {
		return
	}
	g.nodeSet[n] = struct{}{}
	g.nodes = append(g.nodes, n)
}

// AddEdge adds an edge from->to with the given attributes and returns its
// ID. Endpoint nodes are added implicitly. The attributes map is copied.
func (g *Graph) AddEdge(from, to NodeID, attrs Attributes) EdgeID {
	g.AddNode(from)
	g.AddNode(to)
	cp := make(Attributes, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	id := EdgeID(len(g.edges))
	g.edges = append(g.edges, &Edge{ID: id, From: from, To: to, Attrs: cp})
	return id
}

// Nodes returns the nodes in insertion order.
func (g *Graph) Nodes() []NodeID {
	out := make([]NodeID, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// HasNode reports whether n belongs to the graph.
func (g *Graph) HasNode(n NodeID) bool {
	_, ok := g.nodeSet[n]
	return ok
}

// Edges returns the edges in insertion order.
func (g *Graph) Edges() []*Edge {
	out := make([]*Edge, len(g.edges))
	copy(out, g.edges)
	return out
}

// Edge returns the edge with the given ID.
func (g *Graph) Edge(id EdgeID) *Edge { return g.edges[int(id)] }

// NumNodes returns the number of nodes.
func (g *Graph) NumNodes() int { return len(g.nodes) }

// NumEdges returns the number of edges.
func (g *Graph) NumEdges() int { return len(g.edges) }
