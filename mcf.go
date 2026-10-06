// Package mcf solves exact minimum-cost flow problems on small
// transportation graphs (integer capacities, possibly negative costs).
//
// The exact solver uses lower-bound transformation, a max-flow
// feasibility phase, and negative-cycle canceling in the residual
// network, so it never stops at a merely shortest-augmenting-path
// solution: residual negative cycles are canceled until none remain,
// which certifies optimality. Instances whose supplies cannot be
// routed within capacity are reported as infeasible, never as a
// partial "best effort" flow.
package mcf

import "math"

// Edge is a directed capacitated arc. Flow f must satisfy
// Lo <= f <= Cap and contributes Cost*f to the objective.
type Edge struct {
	From, To int
	Lo, Cap  int
	Cost     int
}

// Graph is a transportation network on nodes 0..N-1.
type Graph struct {
	N     int
	Edges []Edge
}

// Status is the outcome class of a solve.
type Status int

const (
	// Optimal means the returned flow is a certified optimum.
	Optimal Status = iota
	// Infeasible means no flow satisfies supplies within [Lo, Cap].
	Infeasible
	// BudgetExhausted means the search stopped early; only bounds hold.
	BudgetExhausted
)

func (s Status) String() string {
	switch s {
	case Optimal:
		return "optimal"
	case Infeasible:
		return "infeasible"
	case BudgetExhausted:
		return "budget_exhausted"
	}
	return "unknown"
}

// Result carries a solver answer: per-edge flow and total cost.
type Result struct {
	Status Status
	Flow   []int
	Cost   int
}

// residual arc
type arc struct {
	to, rev int
	cap     int
	cost    int
	edge    int  // index into Graph.Edges, -1 for auxiliary arcs
	forward bool // true if this residual arc raises edge flow
}

type network struct {
	g [][]arc
}

func (net *network) addArc(from, to, cap, cost, edge int, forward bool) {
	net.g[from] = append(net.g[from], arc{to: to, rev: len(net.g[to]), cap: cap, cost: cost, edge: edge, forward: forward})
	net.g[to] = append(net.g[to], arc{to: from, rev: len(net.g[from]) - 1, cap: 0, cost: -cost, edge: edge, forward: !forward})
}

// build constructs the residual network for the feasibility/optimality
// phases. supply[v] is the required net outflow of v; balances must
// sum to zero. It returns the network, the adjusted balances, and the
// super source/sink indices.
func build(g Graph, supply []int) (*network, []int, int, int) {
	n := g.N
	S, T := n, n+1
	net := &network{g: make([][]arc, n+2)}
	bal := make([]int, n)
	copy(bal, supply)
	for i, e := range g.Edges {
		bal[e.From] -= e.Lo
		bal[e.To] += e.Lo
		net.addArc(e.From, e.To, e.Cap-e.Lo, e.Cost, i, true)
	}
	for v := 0; v < n; v++ {
		if bal[v] > 0 {
			net.addArc(S, v, bal[v], 0, -1, true)
		} else if bal[v] < 0 {
			net.addArc(v, T, -bal[v], 0, -1, true)
		}
	}
	return net, bal, S, T
}

// dinic computes max flow from s to t, mutating residual capacities.
func dinic(net *network, s, t int) int {
	n := len(net.g)
	flow := 0
	for {
		level := make([]int, n)
		for i := range level {
			level[i] = -1
		}
		level[s] = 0
		queue := []int{s}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, a := range net.g[v] {
				if a.cap > 0 && level[a.to] < 0 {
					level[a.to] = level[v] + 1
					queue = append(queue, a.to)
				}
			}
		}
		if level[t] < 0 {
			return flow
		}
		it := make([]int, n)
		var dfs func(v, f int) int
		dfs = func(v, f int) int {
			if v == t {
				return f
			}
			for ; it[v] < len(net.g[v]); it[v]++ {
				a := &net.g[v][it[v]]
				if a.cap > 0 && level[a.to] == level[v]+1 {
					if d := dfs(a.to, min(f, a.cap)); d > 0 {
						a.cap -= d
						net.g[a.to][a.rev].cap += d
						return d
					}
				}
			}
			return 0
		}
		for {
			f := dfs(s, math.MaxInt32)
			if f == 0 {
				break
			}
			flow += f
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// cancelNegativeCycles augments around negative-cost residual cycles
// until none remain (Bellman-Ford detection). This is the step that
// distinguishes an exact solver from a shortest-augmenting-path one.
func cancelNegativeCycles(net *network) {
	n := len(net.g)
	for {
		dist := make([]int, n)
		inQ := make([]bool, n)
		prevN := make([]int, n)
		prevA := make([]int, n)
		for i := range prevN {
			prevN[i] = -1
		}
		// SPFA relaxation; any node relaxed for the n-th time lies on
		// or reaches a negative cycle.
		queue := make([]int, 0, n)
		for v := 0; v < n; v++ {
			queue = append(queue, v)
			inQ[v] = true
		}
		cnt := make([]int, n)
		cycNode := -1
		for len(queue) > 0 && cycNode < 0 {
			v := queue[0]
			queue = queue[1:]
			inQ[v] = false
			for ai, a := range net.g[v] {
				if a.cap > 0 && dist[v]+a.cost < dist[a.to] {
					dist[a.to] = dist[v] + a.cost
					prevN[a.to] = v
					prevA[a.to] = ai
					cnt[a.to] = cnt[v] + 1
					if cnt[a.to] >= n {
						cycNode = a.to
						break
					}
					if !inQ[a.to] {
						inQ[a.to] = true
						queue = append(queue, a.to)
					}
				}
			}
		}
		if cycNode < 0 {
			return
		}
		// Walk predecessors n times to land inside the cycle.
		v := cycNode
		for i := 0; i < n; i++ {
			v = prevN[v]
		}
		// Find the cycle bottleneck.
		bottleneck := math.MaxInt32
		u := v
		for {
			a := net.g[prevN[u]][prevA[u]]
			if a.cap < bottleneck {
				bottleneck = a.cap
			}
			u = prevN[u]
			if u == v {
				break
			}
		}
		u = v
		for {
			a := &net.g[prevN[u]][prevA[u]]
			a.cap -= bottleneck
			net.g[a.to][a.rev].cap += bottleneck
			u = prevN[u]
			if u == v {
				break
			}
		}
	}
}

// Solve returns the exact minimum-cost flow meeting the given supplies
// (supply[v] = required net outflow; must sum to zero). On infeasible
// instances Status is Infeasible and Flow is nil.
func Solve(g Graph, supply []int) Result {
	sum := 0
	for _, s := range supply {
		sum += s
	}
	if sum != 0 || len(supply) != g.N {
		return Result{Status: Infeasible}
	}
	for _, e := range g.Edges {
		if e.Lo < 0 || e.Cap < e.Lo {
			return Result{Status: Infeasible}
		}
	}
	net, bal, S, T := build(g, supply)
	need := 0
	for _, b := range bal {
		if b > 0 {
			need += b
		}
	}
	if dinic(net, S, T) != need {
		return Result{Status: Infeasible}
	}
	cancelNegativeCycles(net)
	flow := make([]int, len(g.Edges))
	for v := 0; v < g.N; v++ {
		for _, a := range net.g[v] {
			if a.edge >= 0 && a.forward {
				// reverse arc capacity == flow sent on this arc
				flow[a.edge] = net.g[a.to][a.rev].cap
			}
		}
	}
	for i, e := range g.Edges {
		flow[i] += e.Lo
	}
	return Result{Status: Optimal, Flow: flow, Cost: flowCost(g, flow)}
}

// flowCost evaluates the total cost of a concrete flow vector.
func flowCost(g Graph, flow []int) int {
	c := 0
	for i, e := range g.Edges {
		c += e.Cost * flow[i]
	}
	return c
}

// Feasible reports whether flow satisfies bounds and supplies.
func Feasible(g Graph, supply, flow []int) bool {
	if len(flow) != len(g.Edges) || len(supply) != g.N {
		return false
	}
	bal := make([]int, g.N)
	for i, e := range g.Edges {
		if flow[i] < e.Lo || flow[i] > e.Cap {
			return false
		}
		bal[e.From] += flow[i]
		bal[e.To] -= flow[i]
	}
	for v := range bal {
		if bal[v] != supply[v] {
			return false
		}
	}
	return true
}
