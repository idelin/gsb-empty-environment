package mapmatch

import (
	"container/heap"
	"math"
)

// Edge is a directed road segment between two nodes.
type Edge struct {
	From int // index into Graph.Nodes
	To   int // index into Graph.Nodes
}

// Graph is a directed road network. Nodes are WGS84 coordinates; edges are
// straight segments between nodes. Bidirectional streets are modeled as two
// opposing directed edges (see AddUndirected).
type Graph struct {
	Nodes   []Point
	Edges   []Edge
	lengths []float64 // cached haversine length per edge
	adj     [][]int   // adj[node] = outgoing edge IDs
}

// NewGraph creates a road graph over the given nodes.
func NewGraph(nodes []Point) *Graph {
	return &Graph{Nodes: nodes, adj: make([][]int, len(nodes))}
}

// AddEdge adds a directed edge and returns its ID.
func (g *Graph) AddEdge(from, to int) int {
	id := len(g.Edges)
	g.Edges = append(g.Edges, Edge{From: from, To: to})
	g.lengths = append(g.lengths, Haversine(g.Nodes[from], g.Nodes[to]))
	g.adj[from] = append(g.adj[from], id)
	return id
}

// AddUndirected adds both directions of a two-way street.
func (g *Graph) AddUndirected(a, b int) {
	g.AddEdge(a, b)
	g.AddEdge(b, a)
}

// EdgeLength returns the length of an edge in meters.
func (g *Graph) EdgeLength(id int) float64 { return g.lengths[id] }

// Candidate is a possible match of one GPS observation onto an edge.
type Candidate struct {
	EdgeID   int
	Fraction float64 // position along the edge, 0..1
	Matched  Point   // projected point on the edge
	Dist     float64 // perpendicular distance from observation, meters
}

// nodeDist is a priority-queue item for Dijkstra.
type nodeDist struct {
	node int
	d    float64
}

type nodeHeap []nodeDist

func (h nodeHeap) Len() int            { return len(h) }
func (h nodeHeap) Less(i, j int) bool  { return h[i].d < h[j].d }
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(nodeDist)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// ShortestPath returns the shortest path distance (meters) between two nodes
// following directed edges, or +Inf if unreachable.
func (g *Graph) ShortestPath(src, dst int) float64 {
	if src == dst {
		return 0
	}
	dist := make([]float64, len(g.Nodes))
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	pq := &nodeHeap{{src, 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		cur := heap.Pop(pq).(nodeDist)
		if cur.node == dst {
			return cur.d
		}
		if cur.d > dist[cur.node] {
			continue
		}
		for _, eid := range g.adj[cur.node] {
			nd := cur.d + g.lengths[eid]
			to := g.Edges[eid].To
			if nd < dist[to] {
				dist[to] = nd
				heap.Push(pq, nodeDist{to, nd})
			}
		}
	}
	return math.Inf(1)
}

// RouteDistance returns the shortest on-network travel distance between two
// candidates, respecting edge direction. It returns +Inf when unreachable.
func (g *Graph) RouteDistance(a, b Candidate) float64 {
	ea := g.Edges[a.EdgeID]
	eb := g.Edges[b.EdgeID]
	la := g.lengths[a.EdgeID]
	lb := g.lengths[b.EdgeID]
	best := math.Inf(1)
	// Same edge: use the along-edge distance in either direction. GPS noise
	// makes consecutive projections jitter back and forth along the edge;
	// requiring forward travel would force a full network loop and wrongly
	// forbid the transition.
	if a.EdgeID == b.EdgeID {
		best = math.Abs(b.Fraction-a.Fraction) * la
	}
	// General case: leave edge a at its head, traverse the network, then
	// enter edge b at its tail.
	d := (1-a.Fraction)*la + g.ShortestPath(ea.To, eb.From) + b.Fraction*lb
	if d < best {
		best = d
	}
	return best
}
