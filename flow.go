// Package mincostflow solves minimum-cost flow problems on directed
// networks with integer per-edge lower/upper bounds and node
// supplies/demands. Edge costs and the total cost are arbitrary
// precision integers (math/big).
//
// Problem statement: for each edge e, find an integer flow f_e with
// Lower_e <= f_e <= Upper_e, such that for every node v
//
//	outflow(v) - inflow(v) = Balance[v]
//
// (Balance[v] > 0 is a supply, Balance[v] < 0 a demand), minimizing
// the linear cost sum_e f_e * Cost_e.
//
// Solve proceeds in two phases:
//  1. Feasibility. Lower bounds are eliminated by the standard
//     transformation f = Lower + f' (0 <= f' <= Upper-Lower) with
//     adjusted balances, and a max-flow is computed on an auxiliary
//     network with a super source/sink. If the max-flow cannot satisfy
//     all adjusted balances, the problem is infeasible and the min-cut
//     of the auxiliary network yields a certificate: a node set X whose
//     net demand exceeds (capacity into X) - (lower bounds out of X).
//  2. Optimality. Starting from the feasible flow, negative-cost
//     cycles in the residual network are found (Bellman-Ford over
//     big.Int distances) and cancelled until none remain, which is
//     exactly the optimality condition for min-cost flow.
//
// The flow returned in Result.Flow is always expressed on the ORIGINAL
// network (f_e = Lower_e + f'_e), never the raw auxiliary-network flow.
package mincostflow

import (
	"fmt"
	"math/big"
)

// Edge is a directed arc with integer flow bounds and a linear cost.
type Edge struct {
	From, To     int
	Lower, Upper int64
	Cost         *big.Int // nil means zero cost
}

// Graph describes the network: N nodes numbered 0..N-1, edges, and
// per-node balances (supply/demand). Balances must sum to zero.
type Graph struct {
	N       int
	Edges   []Edge
	Balance []int64
}

// Status is the outcome of Solve.
type Status int

const (
	// Optimal: a feasible, cost-minimal flow was found.
	Optimal Status = iota
	// Infeasible: no flow satisfies bounds and conservation; Cut is set.
	Infeasible
	// InvalidInput: malformed input (bad bounds, unbalanced totals).
	InvalidInput
)

// Result of Solve.
type Result struct {
	Status Status
	Reason string // human-readable detail for InvalidInput

	// Flow[i] is the flow on Edges[i] of the ORIGINAL network
	// (lower bounds already added back). Set only when Status==Optimal.
	Flow []int64
	// Cost is the total cost sum_e Flow[e]*Cost_e. Set when Optimal.
	Cost *big.Int

	// Certificate of infeasibility (set only when Status==Infeasible):
	// Cut[v] is true for v in the violating set X, with
	//   CutDemand    = -sum_{v in X} Balance[v]            (net demand of X)
	//   CutCapacity  = u(A->X) - l(X->A), A = V\X          (max net inflow)
	// and CutDemand > CutCapacity, so conservation cannot hold on X.
	Cut         []bool
	CutDemand   int64
	CutCapacity int64
}

func (e Edge) cost() *big.Int {
	if e.Cost == nil {
		return big.NewInt(0)
	}
	return e.Cost
}

// Solve finds a feasible min-cost flow, or proves infeasibility.
func Solve(g *Graph) *Result {
	n := g.N
	if n < 0 || len(g.Balance) != n {
		return &Result{Status: InvalidInput,
			Reason: fmt.Sprintf("N=%d but %d balance entries", n, len(g.Balance))}
	}
	for i, e := range g.Edges {
		if e.From < 0 || e.From >= n || e.To < 0 || e.To >= n {
			return &Result{Status: InvalidInput,
				Reason: fmt.Sprintf("edge %d has endpoint out of range", i)}
		}
		if e.Lower > e.Upper {
			return &Result{Status: InvalidInput,
				Reason: fmt.Sprintf("edge %d: lower bound %d exceeds upper bound %d",
					i, e.Lower, e.Upper)}
		}
	}
	total := new(big.Int)
	for _, b := range g.Balance {
		total.Add(total, big.NewInt(b))
	}
	if total.Sign() != 0 {
		return &Result{Status: InvalidInput,
			Reason: fmt.Sprintf("total supply != total demand (balances sum to %s)", total)}
	}

	// Phase 1: feasibility via lower-bound elimination + max-flow.
	// Adjusted balance b'(v) = b(v) - (lower out of v) + (lower into v).
	adj := make([]int64, n)
	copy(adj, g.Balance)
	for _, e := range g.Edges {
		adj[e.From] -= e.Lower
		adj[e.To] += e.Lower
	}

	s, t := n, n+1
	d := newDinic(n + 2)
	arcOf := make([]dinicArcRef, len(g.Edges))
	for i, e := range g.Edges {
		arcOf[i] = d.addEdge(e.From, e.To, e.Upper-e.Lower)
	}
	supply := int64(0)
	for v := 0; v < n; v++ {
		if adj[v] > 0 {
			d.addEdge(s, v, adj[v])
			supply += adj[v]
		} else if adj[v] < 0 {
			d.addEdge(v, t, -adj[v])
		}
	}

	if d.maxFlow(s, t) != supply {
		return infeasibleResult(g, d, s)
	}

	// Feasible flow on the transformed network: f' = cap - remaining.
	m := len(g.Edges)
	fprime := make([]int64, m)
	for i, e := range g.Edges {
		used := (e.Upper - e.Lower) - d.arcs[arcOf[i].node][arcOf[i].idx].cap
		fprime[i] = used
	}

	// Phase 2: cancel negative-cost residual cycles until optimal.
	res, back := buildResidual(g, fprime)
	for {
		cycle := findNegativeCycle(res)
		if cycle == nil {
			break
		}
		delta := int64(0)
		for _, loc := range cycle {
			c := res[loc.node][loc.idx].cap
			if delta == 0 || c < delta {
				delta = c
			}
		}
		for _, loc := range cycle {
			a := &res[loc.node][loc.idx]
			a.cap -= delta
			res[a.to][a.rev].cap += delta
		}
	}

	// Map back to the ORIGINAL network: f_e = Lower_e + f'_e, where
	// f'_e is the flow accumulated on the backward residual arc.
	flow := make([]int64, m)
	cost := new(big.Int)
	for i, e := range g.Edges {
		f := e.Lower + res[back[i].node][back[i].idx].cap
		flow[i] = f
		cost.Add(cost, new(big.Int).Mul(big.NewInt(f), e.cost()))
	}
	return &Result{Status: Optimal, Flow: flow, Cost: cost}
}

// infeasibleResult extracts the min-cut certificate from the auxiliary
// max-flow: X = original nodes NOT reachable from the super source in
// the residual network. For X, net demand exceeds u(A->X) - l(X->A).
func infeasibleResult(g *Graph, d *dinic, s int) *Result {
	n := g.N
	reach := make([]bool, n+2)
	queue := []int{s}
	reach[s] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, a := range d.arcs[u] {
			if a.cap > 0 && !reach[a.to] {
				reach[a.to] = true
				queue = append(queue, a.to)
			}
		}
	}
	cut := make([]bool, n)
	demand := int64(0)
	for v := 0; v < n; v++ {
		cut[v] = !reach[v]
		if cut[v] {
			demand -= g.Balance[v]
		}
	}
	capIn := int64(0)
	lowOut := int64(0)
	for _, e := range g.Edges {
		inFrom, inTo := cut[e.From], cut[e.To]
		if !inFrom && inTo {
			capIn += e.Upper
		}
		if inFrom && !inTo {
			lowOut += e.Lower
		}
	}
	return &Result{
		Status:      Infeasible,
		Cut:         cut,
		CutDemand:   demand,
		CutCapacity: capIn - lowOut,
	}
}

// resArc is a residual arc for the cost-optimization phase.
type resArc struct {
	to, rev int
	cap     int64
	cost    *big.Int
}

type resRef struct{ node, idx int }

// buildResidual creates the residual network of the original graph at
// the feasible transformed flow fprime. It returns the network and,
// for each edge, the location of its backward arc (whose capacity
// always equals the current f'_e).
func buildResidual(g *Graph, fprime []int64) ([][]resArc, []resRef) {
	res := make([][]resArc, g.N)
	back := make([]resRef, len(g.Edges))
	add := func(u, v int, c int64, w *big.Int) (int, int) {
		iu := len(res[u])
		res[u] = append(res[u], resArc{to: v, cap: c, cost: w})
		iv := len(res[v])
		res[v] = append(res[v], resArc{to: u, cap: 0, cost: new(big.Int).Neg(w)})
		res[u][iu].rev = iv
		res[v][iv].rev = iu
		return iu, iv
	}
	for i, e := range g.Edges {
		fwd, bwd := add(e.From, e.To, 0, e.cost())
		res[e.From][fwd].cap = (e.Upper - e.Lower) - fprime[i]
		res[e.To][bwd].cap = fprime[i]
		back[i] = resRef{e.To, bwd}
	}
	return res, back
}

type arcLoc struct{ node, idx int }

// findNegativeCycle returns one negative-cost residual cycle as a list
// of arc locations, or nil if none exists (optimality certificate).
// Bellman-Ford with all distances initialized to zero, big.Int costs.
func findNegativeCycle(res [][]resArc) []arcLoc {
	n := len(res)
	dist := make([]*big.Int, n)
	pred := make([]arcLoc, n)
	for i := range dist {
		dist[i] = new(big.Int)
		pred[i] = arcLoc{-1, -1}
	}
	last := -1
	for iter := 0; iter < n; iter++ {
		last = -1
		for u := 0; u < n; u++ {
			for ai, a := range res[u] {
				if a.cap <= 0 {
					continue
				}
				nd := new(big.Int).Add(dist[u], a.cost)
				if nd.Cmp(dist[a.to]) < 0 {
					dist[a.to] = nd
					pred[a.to] = arcLoc{u, ai}
					last = a.to
				}
			}
		}
		if last == -1 {
			return nil
		}
	}
	// `last` was relaxed in the n-th round: walking predecessors n
	// times lands on a node that lies on a negative cycle.
	v := last
	for i := 0; i < n; i++ {
		v = pred[v].node
	}
	start := v
	var cycle []arcLoc
	for {
		p := pred[v]
		cycle = append(cycle, p)
		v = p.node
		if v == start {
			break
		}
	}
	return cycle
}
