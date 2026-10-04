package ssp

import "math"

// Status classifies the shortest-path answer for one (source, target) pair.
type Status int

const (
	// Unreachable: no directed path from source to target exists.
	Unreachable Status = iota
	// Finite: a well-defined finite shortest distance exists.
	Finite
	// NegInf: the target is affected by a negative cycle that is reachable
	// from the source and can reach the target, so distances are
	// unbounded below (shortest "path" is -infinity).
	NegInf
)

func (s Status) String() string {
	switch s {
	case Unreachable:
		return "unreachable"
	case Finite:
		return "finite"
	case NegInf:
		return "negative-infinity"
	}
	return "unknown"
}

// Result holds the outcome of a single-source shortest-path computation.
type Result struct {
	Source   int
	Dist     []float64 // +Inf when unreachable; meaningless when NegInf
	Status   []Status
	Pred     []int // predecessor vertex on a shortest walk, -1 if none
	PredEdge []int // edge index used to relax each vertex, -1 if none

	relaxed []int // vertices relaxed during the n-th Bellman-Ford pass
}

// ShortestPaths runs the Bellman-Ford finite-step dynamic program from src.
//
// After n-1 relaxation rounds (n = number of vertices) the distances of all
// vertices not influenced by negative cycles are final. A vertex still
// relaxable in round n lies on or downstream of a reachable negative cycle;
// everything reachable from such vertices is marked NegInf. Negative cycles
// elsewhere in the graph (unreachable from src, or unable to reach a given
// target) do not affect the reported statuses.
func ShortestPaths(g *Graph, src int) *Result {
	n := g.NumVertices()
	dist := make([]float64, n)
	pred := make([]int, n)
	predEdge := make([]int, n)
	for i := 0; i < n; i++ {
		dist[i] = math.Inf(1)
		pred[i] = -1
		predEdge[i] = -1
	}
	dist[src] = 0

	var relaxed []int
	for round := 0; round < n; round++ {
		any := false
		for ei, e := range g.Edges {
			if math.IsInf(dist[e.From], 1) {
				continue
			}
			if nd := dist[e.From] + e.Weight; nd < dist[e.To] {
				dist[e.To] = nd
				pred[e.To] = e.From
				predEdge[e.To] = ei
				any = true
				if round == n-1 {
					relaxed = append(relaxed, e.To)
				}
			}
		}
		if !any {
			break
		}
	}

	// Vertices influenced by a reachable negative cycle: everything
	// reachable from a vertex relaxed in the n-th round.
	affected := make([]bool, n)
	queue := make([]int, 0, len(relaxed))
	for _, v := range relaxed {
		if !affected[v] {
			affected[v] = true
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range g.Adj[u] {
			v := g.Edges[ei].To
			if !affected[v] {
				affected[v] = true
				queue = append(queue, v)
			}
		}
	}

	res := &Result{
		Source:   src,
		Dist:     dist,
		Status:   make([]Status, n),
		Pred:     pred,
		PredEdge: predEdge,
		relaxed:  relaxed,
	}
	for v := 0; v < n; v++ {
		switch {
		case math.IsInf(dist[v], 1):
			res.Status[v] = Unreachable
		case affected[v]:
			res.Status[v] = NegInf
		default:
			res.Status[v] = Finite
		}
	}
	return res
}

// Path reconstructs a shortest path (vertex ids, endpoints included) from
// the source to target t. It returns nil when t is unreachable or NegInf.
func (r *Result) Path(g *Graph, t int) []int {
	if t < 0 || t >= g.NumVertices() || r.Status[t] != Finite {
		return nil
	}
	n := g.NumVertices()
	seen := make([]bool, n)
	var rev []int
	for v := t; v != -1; v = r.Pred[v] {
		if seen[v] { // defensive: predecessor chains of Finite vertices never loop
			return nil
		}
		seen[v] = true
		rev = append(rev, v)
	}
	path := make([]int, len(rev))
	for i, v := range rev {
		path[len(rev)-1-i] = v
	}
	if len(path) == 0 || path[0] != r.Source {
		return nil
	}
	return path
}
