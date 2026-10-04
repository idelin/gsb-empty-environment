// Package mincostflow solves minimum-cost flow problems on directed
// networks with integer per-edge lower/upper capacity bounds, integer
// node supplies/demands and linear edge costs given as big integers.
//
// The solver works in two phases:
//
//  1. Feasibility. Lower bounds are eliminated by the standard
//     transformation (edge (u,v,l,u,c) becomes capacity u-l and the
//     balances are adjusted), then a super-source/super-sink max-flow
//     decides feasibility. If no feasible flow exists, a cut
//     certificate is returned: a node set S whose net supply exceeds
//     what can leave it, i.e. balance(S) > upper(S,V\S) - lower(V\S,S)
//     (Hoffman's circulation condition).
//  2. Optimization. Starting from the feasible flow (auxiliary flow
//     PLUS the subtracted lower bounds - the auxiliary flow alone is
//     NOT a flow in the original network), negative-cost residual
//     cycles are cancelled until none remain, which yields a
//     minimum-cost feasible flow.
package mincostflow

import (
	"fmt"
	"math/big"
)

// Edge is a directed edge with integer flow bounds and a linear cost
// per unit of flow.
type Edge struct {
	From, To     int
	Lower, Upper int64
	Cost         *big.Int
}

// Graph describes the network. Balance[v] > 0 means v is a supply node
// (it must send out Balance[v] more units than it receives);
// Balance[v] < 0 means v is a demand node.
type Graph struct {
	NumNodes int
	Edges    []Edge
	Balance  []int64
}

// Status is the outcome of Solve.
type Status int

const (
	// Optimal: a feasible flow exists and Flow is a minimum-cost one.
	Optimal Status = iota
	// Unbalanced: total supply does not equal total demand.
	Unbalanced
	// BadBounds: some edge has Lower > Upper.
	BadBounds
	// Infeasible: bounds and balances are consistent in aggregate but
	// no feasible flow exists; Certificate identifies a violated cut.
	Infeasible
)

func (s Status) String() string {
	switch s {
	case Optimal:
		return "Optimal"
	case Unbalanced:
		return "Unbalanced"
	case BadBounds:
		return "BadBounds"
	case Infeasible:
		return "Infeasible"
	}
	return "Unknown"
}

// CutCertificate proves infeasibility. For the node set S (the side
// reachable from the super source in the residual graph of the
// auxiliary max-flow), the net supply that must leave S exceeds the
// capacity available for it:
//
//	Supply > OutCapacity - InLower
//
// i.e. balance(S) > upper(S, V\S) - lower(V\S, S).
type CutCertificate struct {
	InSet       []bool // InSet[v] == true iff v belongs to S
	Supply      int64  // sum of Balance over S
	OutCapacity int64  // sum of Upper over edges S -> V\S
	InLower     int64  // sum of Lower over edges V\S -> S
}

// Violation returns Supply - (OutCapacity - InLower) > 0, the amount
// by which the cut is violated.
func (c *CutCertificate) Violation() int64 {
	return c.Supply - (c.OutCapacity - c.InLower)
}

func (c *CutCertificate) String() string {
	n := 0
	for _, in := range c.InSet {
		if in {
			n++
		}
	}
	return fmt.Sprintf("cut |S|=%d: supply(S)=%d > upper(S,T)-lower(T,S)=%d (violation %d)",
		n, c.Supply, c.OutCapacity-c.InLower, c.Violation())
}

// Result is the output of Solve.
type Result struct {
	Status Status
	// Flow[e] is the flow on original edge e (Lower[e] <= Flow[e] <=
	// Upper[e]); only set when Status == Optimal.
	Flow []int64
	// Cost is the total cost sum_e Flow[e]*Cost[e]; set when Optimal.
	Cost *big.Int
	// Certificate is set when Status == Infeasible.
	Certificate *CutCertificate
	// Message carries a human-readable reason for non-Optimal status.
	Message string
}

// Solve finds a feasible flow respecting all bounds and balances and,
// among all feasible flows, one of minimum total cost.
func Solve(g *Graph) *Result {
	if g.NumNodes < 0 || len(g.Balance) != g.NumNodes {
		return &Result{Status: BadBounds, Message: "Balance length must equal NumNodes"}
	}
	for i, e := range g.Edges {
		if e.From < 0 || e.From >= g.NumNodes || e.To < 0 || e.To >= g.NumNodes {
			return &Result{Status: BadBounds,
				Message: fmt.Sprintf("edge %d has endpoint out of range", i)}
		}
		if e.Lower > e.Upper {
			return &Result{Status: BadBounds,
				Message: fmt.Sprintf("edge %d (%d->%d): lower bound %d exceeds upper bound %d",
					i, e.From, e.To, e.Lower, e.Upper)}
		}
		if e.Cost == nil {
			return &Result{Status: BadBounds,
				Message: fmt.Sprintf("edge %d has nil cost", i)}
		}
	}

	// Total supply must equal total demand.
	var total int64
	for _, b := range g.Balance {
		total += b
	}
	if total != 0 {
		return &Result{Status: Unbalanced,
			Message: fmt.Sprintf("total supply minus total demand is %d, not zero", total)}
	}

	n := g.NumNodes

	// Eliminate lower bounds: every edge keeps only capacity
	// Upper-Lower, and each node's required net outflow becomes
	//   adj[v] = Balance[v] - lowerOut(v) + lowerIn(v).
	adj := make([]int64, n)
	copy(adj, g.Balance)
	for _, e := range g.Edges {
		adj[e.From] -= e.Lower
		adj[e.To] += e.Lower
	}

	// Auxiliary network: original edges with capacity Upper-Lower,
	// super source s feeding nodes with adj[v] > 0, super sink t
	// draining nodes with adj[v] < 0. Feasible iff every unit of
	// positive adjusted balance can be routed to the sink.
	s, t := n, n+1
	din := newDinic(n + 2)
	edgeSlot := make([]int, len(g.Edges)) // dinic edge index carrying edge e's flow
	for i, e := range g.Edges {
		edgeSlot[i] = din.addEdge(e.From, e.To, e.Upper-e.Lower)
	}
	var need int64
	for v := 0; v < n; v++ {
		if adj[v] > 0 {
			din.addEdge(s, v, adj[v])
			need += adj[v]
		} else if adj[v] < 0 {
			din.addEdge(v, t, -adj[v])
		}
	}

	flow := din.maxFlow(s, t)
	if flow < need {
		// The set reachable from s in the residual graph is a cut
		// whose demand cannot be met; translate it back to the
		// original network's quantities.
		reach := din.reachable(s)
		cert := &CutCertificate{InSet: make([]bool, n)}
		for v := 0; v < n; v++ {
			cert.InSet[v] = reach[v]
			if reach[v] {
				cert.Supply += g.Balance[v]
			}
		}
		for _, e := range g.Edges {
			if reach[e.From] && !reach[e.To] {
				cert.OutCapacity += e.Upper
			}
			if !reach[e.From] && reach[e.To] {
				cert.InLower += e.Lower
			}
		}
		return &Result{
			Status:      Infeasible,
			Certificate: cert,
			Message:     "no feasible flow: " + cert.String(),
		}
	}

	// Recover the flow in the ORIGINAL network: the auxiliary network
	// only carries the part above the lower bounds, so the lower
	// bounds must be added back. Returning the auxiliary flow
	// directly would violate the balances of the original problem.
	f := make([]int64, len(g.Edges))
	for i, e := range g.Edges {
		f[i] = e.Lower + din.flowOn(edgeSlot[i])
	}

	// Phase 2: minimise the linear cost by cancelling negative-cost
	// residual cycles.
	cancelNegativeCycles(g, f)

	cost := new(big.Int)
	for i, e := range g.Edges {
		cost.Add(cost, new(big.Int).Mul(big.NewInt(f[i]), e.Cost))
	}
	return &Result{Status: Optimal, Flow: f, Cost: cost}
}

// CheckFlow verifies that f is a feasible flow for g: bounds on every
// edge and balance conservation at every node. It returns nil when f
// is feasible, otherwise a description of the first violation.
func CheckFlow(g *Graph, f []int64) error {
	if len(f) != len(g.Edges) {
		return fmt.Errorf("flow has %d entries, want %d", len(f), len(g.Edges))
	}
	net := make([]int64, g.NumNodes)
	for i, e := range g.Edges {
		if f[i] < e.Lower || f[i] > e.Upper {
			return fmt.Errorf("edge %d (%d->%d): flow %d outside [%d,%d]",
				i, e.From, e.To, f[i], e.Lower, e.Upper)
		}
		net[e.From] -= f[i]
		net[e.To] += f[i]
	}
	for v := 0; v < g.NumNodes; v++ {
		if -net[v] != g.Balance[v] {
			return fmt.Errorf("node %d: net outflow %d, want balance %d",
				v, -net[v], g.Balance[v])
		}
	}
	return nil
}

// FlowCost returns the total cost of flow f.
func FlowCost(g *Graph, f []int64) *big.Int {
	cost := new(big.Int)
	for i, e := range g.Edges {
		cost.Add(cost, new(big.Int).Mul(big.NewInt(f[i]), e.Cost))
	}
	return cost
}
