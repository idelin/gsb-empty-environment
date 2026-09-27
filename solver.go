package clocksync

import (
	"fmt"
	"math"
	"strings"
)

// Conflict is an infeasible set of constraints: a negative cycle in the
// constraint graph. Summing the chain yields 0 <= Total < 0.
type Conflict struct {
	Chain []Constraint
	Total float64
}

func (c *Conflict) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "conflicting constraints (cycle total %.3f < 0):", c.Total)
	for _, con := range c.Chain {
		fmt.Fprintf(&b, "\n  %s --%s--> %s: theta[%s] - theta[%s] <= %+.3f",
			con.From, con.Msg, con.To, con.To, con.From, con.Bound)
	}
	return b.String()
}

// Result is the outcome of Solve. When Conflict is non-nil the other
// fields are empty.
type Result struct {
	Ref       string
	Intervals map[string]Interval // feasible offset range relative to Ref
	Offsets   map[string]float64  // one feasible offset assignment
	Conflict  *Conflict
}

type edge struct {
	from, to int
	w        float64
	c        Constraint
}

type solver struct {
	nodes []string
	idx   map[string]int
	edges []edge
}

// Solve derives feasible clock-offset intervals per node relative to a
// reference node (theta[ref] = 0). Contradictory constraint sets yield a
// Conflict with the offending chain; sparse constraints yield wide
// (possibly unbounded) intervals.
func Solve(nodes []string, cons []Constraint) Result {
	s := newSolver(nodes, cons)

	pot, cycleStart, predEdge, hasCycle := s.potentials()
	if hasCycle {
		chain := s.extractCycle(cycleStart, predEdge)
		total := 0.0
		for _, c := range chain {
			total += c.Bound
		}
		return Result{Conflict: &Conflict{Chain: chain, Total: total}}
	}

	ref := s.nodes[0]
	hi := s.shortestPaths(s.idx[ref])
	lo := s.reverseShortestPaths(s.idx[ref])

	res := Result{
		Ref:       ref,
		Intervals: make(map[string]Interval, len(s.nodes)),
		Offsets:   make(map[string]float64, len(s.nodes)),
	}
	for i, n := range s.nodes {
		res.Intervals[n] = Interval{Lo: -lo[i], Hi: hi[i]}
		// Feasible potentials satisfy every difference constraint, so
		// shifting them to pin the reference node keeps them feasible.
		res.Offsets[n] = pot[i] - pot[s.idx[ref]]
	}
	return res
}

func newSolver(nodes []string, cons []Constraint) *solver {
	s := &solver{idx: make(map[string]int)}
	nodeIdx := func(name string) int {
		if i, ok := s.idx[name]; ok {
			return i
		}
		i := len(s.nodes)
		s.nodes = append(s.nodes, name)
		s.idx[name] = i
		return i
	}
	for _, n := range nodes {
		nodeIdx(n)
	}
	for _, c := range cons {
		s.edges = append(s.edges, edge{from: nodeIdx(c.From), to: nodeIdx(c.To), w: c.Bound, c: c})
	}
	return s
}

// potentials runs Bellman-Ford from a virtual super-source connected to
// every node with weight 0. Without negative cycles the distances are a
// feasible potential assignment; otherwise it reports a cycle entry point.
func (s *solver) potentials() (dist []float64, cycleStart int, predEdge []int, hasCycle bool) {
	n := len(s.nodes)
	dist = make([]float64, n) // super-source: all zero
	predEdge = make([]int, n)
	for i := range predEdge {
		predEdge[i] = -1
	}
	for i := 0; i < n; i++ {
		changed := false
		for ei, e := range s.edges {
			if dist[e.from]+e.w < dist[e.to] {
				dist[e.to] = dist[e.from] + e.w
				predEdge[e.to] = ei
				changed = true
				if i == n-1 {
					return dist, e.to, predEdge, true
				}
			}
		}
		if !changed {
			break
		}
	}
	return dist, -1, predEdge, false
}

// extractCycle walks predecessor edges to recover the negative cycle.
func (s *solver) extractCycle(start int, predEdge []int) []Constraint {
	v := start
	for i := 0; i < len(s.nodes); i++ {
		v = s.edges[predEdge[v]].from
	}
	var chain []Constraint
	for cur := v; ; {
		e := s.edges[predEdge[cur]]
		chain = append(chain, e.c)
		cur = e.from
		if cur == v {
			break
		}
	}
	return chain
}

// shortestPaths computes shortest distances from src; unreachable = +Inf.
func (s *solver) shortestPaths(src int) []float64 {
	dist := make([]float64, len(s.nodes))
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	for i := 0; i < len(s.nodes)-1; i++ {
		changed := false
		for _, e := range s.edges {
			if math.IsInf(dist[e.from], 1) {
				continue
			}
			if dist[e.from]+e.w < dist[e.to] {
				dist[e.to] = dist[e.from] + e.w
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return dist
}

// reverseShortestPaths computes, for each node v, the shortest distance
// from v to src in the original graph (by relaxing reversed edges).
func (s *solver) reverseShortestPaths(src int) []float64 {
	dist := make([]float64, len(s.nodes))
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	for i := 0; i < len(s.nodes)-1; i++ {
		changed := false
		for _, e := range s.edges {
			if math.IsInf(dist[e.to], 1) {
				continue
			}
			if dist[e.to]+e.w < dist[e.from] {
				dist[e.from] = dist[e.to] + e.w
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return dist
}
