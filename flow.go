// Package flowsolve solves feasible-flow problems on directed networks
// with integer per-edge lower/upper bounds and per-node net-inflow demands.
//
// For every node v, a feasible flow f satisfies
//
//	sum(inflow of v) - sum(outflow of v) == Demand[v]
//
// and for every edge e, Lo[e] <= f[e] <= Hi[e]. A self-loop contributes
// equally to the inflow and outflow of its node, so it is only constrained
// by its own bounds. Parallel edges are supported, and isolated nodes are
// allowed (their demand must be zero for feasibility).
package flowsolve

import "errors"

// Edge is a directed edge from From to To with integer flow bounds.
type Edge struct {
	From int
	To   int
	Lo   int64
	Hi   int64
}

// Network describes a feasible-flow instance. Demand[v] is the required
// net inflow of node v; the demands must sum to zero.
type Network struct {
	NumNodes int
	Demand   []int64
	Edges    []Edge
}

// Certificate is a proof of infeasibility: a node set X whose required net
// inflow is strictly less than the minimum net inflow the cut into X can
// carry, i.e. demand(X) < loIn(X) - hiOut(X), where loIn sums Lo of edges
// entering X from outside and hiOut sums Hi of edges leaving X. (The
// complementary form, demand too large, is obtained by taking complements.)
type Certificate struct {
	Nodes  []bool // Nodes[v] reports whether v belongs to the violating set X.
	Demand int64  // Sum of demands inside X.
	LoIn   int64  // Sum of Lo over edges entering X from outside.
	HiOut  int64  // Sum of Hi over edges leaving X.
}

var (
	// ErrDemandSum is returned when the demands do not sum to zero.
	ErrDemandSum = errors.New("flowsolve: demands do not sum to zero")
	// ErrBadBounds is returned when an edge has Lo > Hi.
	ErrBadBounds = errors.New("flowsolve: edge with Lo > Hi")
	// ErrBadNode is returned when an edge references a node out of range.
	ErrBadNode = errors.New("flowsolve: edge references invalid node")
)

// Solve returns a feasible flow (one value per edge, in input order) or,
// if the instance is infeasible, a cut certificate proving it.
func Solve(n *Network) (flow []int64, cert *Certificate, err error) {
	if err := checkInput(n); err != nil {
		return nil, nil, err
	}

	// Reduction: put f = lo + g with 0 <= g <= Hi-Lo. Then g must satisfy
	// gin(v) - gout(v) = need(v) where need(v) = Demand[v] - loIn(v) + loOut(v).
	// Self-loops cancel in need(v) and are excluded from the auxiliary graph.
	need := make([]int64, n.NumNodes)
	copy(need, n.Demand)
	for _, e := range n.Edges {
		need[e.To] -= e.Lo
		need[e.From] += e.Lo
	}

	// Auxiliary graph: super source S feeds nodes with need < 0, nodes with
	// need > 0 drain into super sink T. Feasible iff every S-edge saturates.
	numNodes := n.NumNodes
	S := numNodes
	T := numNodes + 1
	d := newDinic(numNodes + 2)
	edgeIndex := make([]int, len(n.Edges)) // dinic edge id of each original edge, -1 for self-loops
	for i, e := range n.Edges {
		if e.From == e.To {
			edgeIndex[i] = -1
			continue
		}
		edgeIndex[i] = d.addEdge(e.From, e.To, e.Hi-e.Lo)
	}
	var total int64
	for v := 0; v < numNodes; v++ {
		if need[v] < 0 {
			d.addEdge(S, v, -need[v])
			total += -need[v]
		} else if need[v] > 0 {
			d.addEdge(v, T, need[v])
		}
	}

	if d.maxFlow(S, T) == total {
		flow = make([]int64, len(n.Edges))
		for i, e := range n.Edges {
			if edgeIndex[i] >= 0 {
				flow[i] = e.Lo + d.flowOn(edgeIndex[i])
			} else {
				// Self-loop: any value within its own bounds works.
				flow[i] = e.Lo
			}
		}
		return flow, nil, nil
	}

	// Infeasible: the set of nodes reachable from S in the residual graph
	// (intersected with the original nodes) is a violating cut.
	reach := d.reachable(S)
	cert = &Certificate{Nodes: make([]bool, numNodes)}
	for v := 0; v < numNodes; v++ {
		cert.Nodes[v] = reach[v]
	}
	fillCertificate(n, cert)
	return nil, cert, nil
}

// fillCertificate computes the aggregate quantities of the cut.
func fillCertificate(n *Network, c *Certificate) {
	for v := 0; v < n.NumNodes; v++ {
		if c.Nodes[v] {
			c.Demand += n.Demand[v]
		}
	}
	for _, e := range n.Edges {
		switch {
		case !c.Nodes[e.From] && c.Nodes[e.To]:
			c.LoIn += e.Lo
		case c.Nodes[e.From] && !c.Nodes[e.To]:
			c.HiOut += e.Hi
		}
	}
}
