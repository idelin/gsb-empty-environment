// Package spp implements the Griffin–Wilfong Stable Paths Problem (SPP)
// analysis toolbox for a single destination prefix:
//
//   - Gao–Rexford valley-free export filtering and candidate-path expansion,
//     including customer-provider commercial-cycle detection;
//   - exact enumeration of all Stable Path Assignments (unique / multiple /
//     none) under AS_PATH loop prevention;
//   - explicit Dispute Wheel construction with machine-checkable
//     R_i >_i Q_i preference-cycle proofs;
//   - minimum policy/export repair synthesis with re-verified uniqueness.
//
// All paths are oriented from the choosing AS toward the destination:
// path[0] is the choosing AS, path[len-1] is the origin.
package spp

import "fmt"

// Relation models the commercial relationship on an undirected AS link.
type Relation int

const (
	// RelationUnknown means the link has no Gao–Rexford role. Such links are
	// permitted for abstract SPP instances (classic gadgets) and are exempt
	// from valley-free checks.
	RelationUnknown Relation = iota
	// RelationPeer connects two autonomous systems on a settlement-free
	// peering (lateral) edge.
	RelationPeer
	// RelationCustomerToProvider, stored on a directed Edge, means edge.From
	// is the customer and edge.To is the provider.
	RelationCustomerToProvider
)

func (r Relation) String() string {
	switch r {
	case RelationPeer:
		return "peer"
	case RelationCustomerToProvider:
		return "customer-to-provider"
	default:
		return "unrestricted"
	}
}

// Edge is one directed half of an AS graph link. Every undirected link is
// represented by exactly two directed edges with reciprocal relations.
type Edge struct {
	From     int
	To       int
	Relation Relation
}

// ASGraph is an undirected AS topology with explicit commercial roles.
type ASGraph struct {
	Nodes []int
	Edges []Edge

	// adj is keyed by node id; values are the outgoing directed edges.
	adj map[int][]Edge
	// undirectedLink records every unordered link exactly once.
	links map[[2]int]Link
}

// Link describes one undirected AS-to-AS link.
type Link struct {
	A, B     int
	Relation Relation
}

// NewASGraph validates the undirected links and builds both directions of
// every directed edge. For RelationCustomerToProvider the link must name the
// customer first (Link.A is customer, Link.B is provider).
func NewASGraph(nodes []int, links []Link) (*ASGraph, error) {
	g := &ASGraph{
		Nodes: append([]int(nil), nodes...),
		adj:   make(map[int][]Edge),
		links: make(map[[2]int]Link),
	}
	nodeSet := make(map[int]bool, len(nodes))
	for _, n := range nodes {
		if nodeSet[n] {
			return nil, fmt.Errorf("spp: duplicate node %d", n)
		}
		nodeSet[n] = true
	}
	for _, l := range links {
		if !nodeSet[l.A] || !nodeSet[l.B] {
			return nil, fmt.Errorf("spp: link %d-%d references unknown node", l.A, l.B)
		}
		if l.A == l.B {
			return nil, fmt.Errorf("spp: self-link on node %d is not allowed", l.A)
		}
		key := linkKey(l.A, l.B)
		if _, seen := g.links[key]; seen {
			return nil, fmt.Errorf("spp: duplicate link %d-%d", l.A, l.B)
		}
		g.links[key] = l
		switch l.Relation {
		case RelationPeer:
			g.adj[l.A] = append(g.adj[l.A], Edge{From: l.A, To: l.B, Relation: RelationPeer})
			g.adj[l.B] = append(g.adj[l.B], Edge{From: l.B, To: l.A, Relation: RelationPeer})
		case RelationCustomerToProvider:
			// A is customer, B is provider.
			g.adj[l.A] = append(g.adj[l.A], Edge{From: l.A, To: l.B, Relation: RelationCustomerToProvider})
			g.adj[l.B] = append(g.adj[l.B], Edge{From: l.B, To: l.A, Relation: RelationPeer})
		case RelationUnknown:
			g.adj[l.A] = append(g.adj[l.A], Edge{From: l.A, To: l.B, Relation: RelationUnknown})
			g.adj[l.B] = append(g.adj[l.B], Edge{From: l.B, To: l.A, Relation: RelationUnknown})
		default:
			return nil, fmt.Errorf("spp: unknown relation on link %d-%d", l.A, l.B)
		}
	}
	for _, n := range nodes {
		sortEdges(g.adj[n])
	}
	return g, nil
}

// Neighbors returns the sorted outgoing directed edges of node n.
func (g *ASGraph) Neighbors(n int) []Edge {
	return g.adj[n]
}

// Link returns the undirected link joining a and b.
func (g *ASGraph) Link(a, b int) (Link, bool) {
	l, ok := g.links[linkKey(a, b)]
	return l, ok
}

// TraversalRole reports the Gao–Rexford role observed while propagating a
// route from neighbor "from" into node "to" (the path is oriented toward the
// destination, so propagation physically runs from -> to).
func TraversalRole(g *ASGraph, from, to int) (Relation, bool) {
	for _, e := range g.adj[from] {
		if e.To == to {
			return e.Relation, true
		}
	}
	return RelationUnknown, false
}

func linkKey(a, b int) [2]int {
	if a <= b {
		return [2]int{a, b}
	}
	return [2]int{b, a}
}

func sortEdges(es []Edge) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j-1].To > es[j].To; j-- {
			es[j-1], es[j] = es[j], es[j-1]
		}
	}
}

// PathKey renders a path as a stable comparable key; the empty path is "".
func PathKey(p []int) string {
	if len(p) == 0 {
		return ""
	}
	return fmt.Sprint(p)
}

// CopyPath returns a defensive copy of p.
func CopyPath(p []int) []int {
	if p == nil {
		return nil
	}
	out := make([]int, len(p))
	copy(out, p)
	return out
}
