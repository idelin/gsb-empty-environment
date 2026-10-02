// Package fas implements minimal approximators for the minimum-weight
// Feedback Arc Set (FAS) problem on directed graphs: find a minimum
// total-weight set of edges whose removal makes the graph acyclic.
//
// Three heuristics are provided and combined by Solve:
//
//	GreedyShared:     repeatedly cut the edge shared by the most cycles
//	                  (ties broken by higher weight) -- the "key edge".
//	GreedyMinWeight:  repeatedly find one cycle and cut its cheapest edge.
//	Topological:      weighted Eades-Lin-Smyth ordering; backward arcs
//	                  form the feedback set.
//
// Solve runs all three and returns the lightest result together with a
// topological ordering of the remaining graph, which is a constructive
// proof that the remainder is strictly acyclic.
package fas

import "math"

// Edge is a weighted directed arc From -> To.
type Edge struct {
	From, To int
	Weight   float64
}

// Graph is a directed multigraph on nodes 0..NumNodes-1.
type Graph struct {
	NumNodes int
	Edges    []Edge
}

// New returns an empty graph on n nodes.
func New(n int) *Graph { return &Graph{NumNodes: n} }

// Add appends a weighted edge.
func (g *Graph) Add(from, to int, w float64) {
	g.Edges = append(g.Edges, Edge{From: from, To: to, Weight: w})
}

// Copy returns a deep copy of the edge list.
func (g *Graph) Copy() *Graph {
	out := &Graph{NumNodes: g.NumNodes, Edges: make([]Edge, len(g.Edges))}
	copy(out.Edges, g.Edges)
	return out
}

func (g *Graph) removeEdge(i int) {
	g.Edges = append(g.Edges[:i], g.Edges[i+1:]...)
}

// TotalWeight sums the weights of es.
func TotalWeight(es []Edge) float64 {
	var w float64
	for _, e := range es {
		w += e.Weight
	}
	return w
}

// Subtract returns a copy of g with the multiset of edges removed removed.
func Subtract(g *Graph, removed []Edge) *Graph {
	type key struct {
		from, to int
		w        float64
	}
	cnt := make(map[key]int, len(removed))
	for _, e := range removed {
		cnt[key{e.From, e.To, e.Weight}]++
	}
	out := &Graph{NumNodes: g.NumNodes}
	for _, e := range g.Edges {
		k := key{e.From, e.To, e.Weight}
		if cnt[k] > 0 {
			cnt[k]--
			continue
		}
		out.Edges = append(out.Edges, e)
	}
	return out
}

// TopoSort runs Kahn's algorithm. ok is false iff g contains a directed
// cycle, so a true result is a constructive proof that g is a DAG.
func TopoSort(g *Graph) (order []int, ok bool) {
	indeg := make([]int, g.NumNodes)
	adj := make([][]int, g.NumNodes)
	for _, e := range g.Edges {
		if e.From == e.To {
			return nil, false
		}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}
	var queue []int
	for i := 0; i < g.NumNodes; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, v := range adj[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if len(order) != g.NumNodes {
		return nil, false
	}
	return order, true
}

// IsDAG reports whether g is acyclic.
func IsDAG(g *Graph) bool {
	_, ok := TopoSort(g)
	return ok
}

// VerifyOrder checks that order is a permutation of all nodes and that
// every edge of g goes forward in it, i.e. that order certifies g as a DAG.
func VerifyOrder(g *Graph, order []int) bool {
	if len(order) != g.NumNodes {
		return false
	}
	pos := make([]int, g.NumNodes)
	seen := make([]bool, g.NumNodes)
	for i, v := range order {
		if v < 0 || v >= g.NumNodes || seen[v] {
			return false
		}
		seen[v] = true
		pos[v] = i
	}
	for _, e := range g.Edges {
		if pos[e.From] >= pos[e.To] {
			return false
		}
	}
	return true
}

// FindCycle returns the edge indices of one directed cycle, or nil.
func FindCycle(g *Graph) []int {
	const (
		white = iota
		gray
		black
	)
	color := make([]int, g.NumNodes)
	parentEdge := make([]int, g.NumNodes)
	parentNode := make([]int, g.NumNodes)
	adj := make([][]int, g.NumNodes)
	for i, e := range g.Edges {
		adj[e.From] = append(adj[e.From], i)
	}
	var cycle []int
	var dfs func(u int) bool
	dfs = func(u int) bool {
		color[u] = gray
		for _, ei := range adj[u] {
			v := g.Edges[ei].To
			if color[v] == gray { // back edge u->v: cycle v ~> u -> v
				path := []int{ei}
				for x := u; x != v; x = parentNode[x] {
					path = append(path, parentEdge[x])
				}
				for l, r := 0, len(path)-1; l < r; l, r = l+1, r-1 {
					path[l], path[r] = path[r], path[l]
				}
				cycle = path
				return true
			}
			if color[v] == white {
				parentEdge[v] = ei
				parentNode[v] = u
				if dfs(v) {
					return true
				}
			}
		}
		color[u] = black
		return false
	}
	for s := 0; s < g.NumNodes; s++ {
		if color[s] == white && dfs(s) {
			return cycle
		}
	}
	return nil
}

// cyclesThroughEdges returns, for every edge that lies on a cycle, one
// elementary cycle through it (shortest in hops). The empty result means
// the graph is a DAG.
func cyclesThroughEdges(g *Graph) [][]int {
	adj := make([][]int, g.NumNodes)
	for i, e := range g.Edges {
		adj[e.From] = append(adj[e.From], i)
	}
	var cycles [][]int
	for i, e := range g.Edges {
		if e.From == e.To {
			cycles = append(cycles, []int{i})
			continue
		}
		// BFS from e.To back to e.From, avoiding edge i itself.
		prev := make([]int, g.NumNodes)
		for j := range prev {
			prev[j] = -2
		}
		prev[e.To] = -1
		queue := []int{e.To}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for _, ei := range adj[u] {
				if ei == i {
					continue
				}
				v := g.Edges[ei].To
				if prev[v] == -2 {
					prev[v] = ei
					queue = append(queue, v)
				}
			}
		}
		if prev[e.From] == -2 {
			continue // edge i is on no cycle
		}
		var path []int
		for x := e.From; x != e.To; x = g.Edges[prev[x]].From {
			path = append(path, prev[x])
		}
		for l, r := 0, len(path)-1; l < r; l, r = l+1, r-1 {
			path[l], path[r] = path[r], path[l]
		}
		cycles = append(cycles, append(path, i))
	}
	return cycles
}

// GreedyShared approximates the FAS by repeatedly cutting the "key edge":
// the edge shared by the most currently present cycles, with ties broken
// toward the highest weight.
func GreedyShared(g *Graph) []Edge {
	work := g.Copy()
	var removed []Edge
	for {
		cycles := cyclesThroughEdges(work)
		if len(cycles) == 0 {
			return removed
		}
		coverage := make(map[int]int)
		for _, cyc := range cycles {
			for _, ei := range cyc {
				coverage[ei]++
			}
		}
		best := -1
		for ei, c := range coverage {
			if best == -1 ||
				c > coverage[best] ||
				(c == coverage[best] && work.Edges[ei].Weight > work.Edges[best].Weight) ||
				(c == coverage[best] && work.Edges[ei].Weight == work.Edges[best].Weight && ei < best) {
				best = ei
			}
		}
		removed = append(removed, work.Edges[best])
		work.removeEdge(best)
	}
}

// GreedyMinWeight approximates the FAS by repeatedly finding one cycle and
// cutting its cheapest edge.
func GreedyMinWeight(g *Graph) []Edge {
	work := g.Copy()
	var removed []Edge
	for {
		cyc := FindCycle(work)
		if cyc == nil {
			return removed
		}
		best := cyc[0]
		for _, ei := range cyc[1:] {
			if work.Edges[ei].Weight < work.Edges[best].Weight {
				best = ei
			}
		}
		removed = append(removed, work.Edges[best])
		work.removeEdge(best)
	}
}

// Topological approximates the FAS with the weighted Eades-Lin-Smyth
// ordering heuristic: sinks slide right, sources slide left, otherwise the
// vertex with the largest (out-weight - in-weight) goes left. Edges that
// end up backward in the ordering form the feedback set.
func Topological(g *Graph) []Edge {
	n := g.NumNodes
	inW := make([]float64, n)
	outW := make([]float64, n)
	alive := make([]bool, n)
	for i := range alive {
		alive[i] = true
	}
	for _, e := range g.Edges {
		outW[e.From] += e.Weight
		inW[e.To] += e.Weight
	}
	var left, right []int
	remaining := n
	drop := func(v int) {
		alive[v] = false
		remaining--
		for _, e := range g.Edges {
			if e.From == v && alive[e.To] {
				inW[e.To] -= e.Weight
			}
			if e.To == v && alive[e.From] {
				outW[e.From] -= e.Weight
			}
		}
	}
	for remaining > 0 {
		for { // sinks move to the right end
			v := -1
			for i := 0; i < n; i++ {
				if alive[i] && outW[i] == 0 {
					v = i
					break
				}
			}
			if v == -1 {
				break
			}
			right = append(right, v)
			drop(v)
		}
		for { // sources move to the left end
			v := -1
			for i := 0; i < n; i++ {
				if alive[i] && inW[i] == 0 {
					v = i
					break
				}
			}
			if v == -1 {
				break
			}
			left = append(left, v)
			drop(v)
		}
		if remaining == 0 {
			break
		}
		best := -1 // otherwise the max (out-weight - in-weight) vertex goes left
		for i := 0; i < n; i++ {
			if alive[i] && (best == -1 || outW[i]-inW[i] > outW[best]-inW[best]) {
				best = i
			}
		}
		left = append(left, best)
		drop(best)
	}
	order := append([]int{}, left...)
	for i := len(right) - 1; i >= 0; i-- {
		order = append(order, right[i])
	}
	pos := make([]int, n)
	for i, v := range order {
		pos[v] = i
	}
	var removed []Edge
	for _, e := range g.Edges {
		if pos[e.From] >= pos[e.To] {
			removed = append(removed, e)
		}
	}
	return removed
}

// Result is the outcome of Solve: the removed feedback arc set, its total
// weight, the heuristic that produced it, and a topological ordering of
// the remaining graph (the acyclicity certificate).
type Result struct {
	Removed     []Edge
	TotalWeight float64
	Heuristic   string
	TopoOrder   []int
}

// Solve runs every heuristic and keeps the lightest feedback arc set
// (ties broken by fewer edges). The returned TopoOrder is guaranteed to
// satisfy VerifyOrder on the remaining graph.
func Solve(g *Graph) *Result {
	heuristics := []struct {
		name string
		fn   func(*Graph) []Edge
	}{
		{"greedy-shared", GreedyShared},
		{"greedy-min-weight", GreedyMinWeight},
		{"topological", Topological},
	}
	var best []Edge
	bestName := ""
	bestW := math.Inf(1)
	for _, h := range heuristics {
		rem := h.fn(g)
		w := TotalWeight(rem)
		if w < bestW || (w == bestW && len(rem) < len(best)) {
			best, bestName, bestW = rem, h.name, w
		}
	}
	rest := Subtract(g, best)
	order, ok := TopoSort(rest)
	if !ok {
		panic("fas: heuristic left a cyclic graph")
	}
	return &Result{Removed: best, TotalWeight: bestW, Heuristic: bestName, TopoOrder: order}
}
