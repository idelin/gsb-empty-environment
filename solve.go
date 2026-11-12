package netsec

import (
	"fmt"
	"strings"
)

// anchorNode is the virtual x0 node: x0 is fixed to 0 and every event has
// edges to and from it so that all nodes are reachable from one source.
const anchorNode = 0

// edge u -> v with weight w encodes t[v] - t[u] <= w.
type edge struct {
	from   int
	to     int
	weight int64
}

// Solution is a feasible time assignment together with the tight implied
// integer window of every event.
type Solution struct {
	p        *Problem
	earliest []int64
	latest   []int64
}

// Times returns the component-wise earliest legal assignment. Mutating the
// returned slice does not affect the solution.
func (s *Solution) Times() []int64 { return append([]int64(nil), s.earliest...) }

// Latest returns the component-wise latest legal assignment.
func (s *Solution) Latest() []int64 { return append([]int64(nil), s.latest...) }

// Window returns the tight implied integer window of event i given the full
// constraint system (never looser than the event's declared window).
func (s *Solution) Window(i int) (lo, hi int64) {
	return s.earliest[i], s.latest[i]
}

// CycleError is returned when the constraint system is contradictory. The
// listed edges form a negative cycle in the difference-constraint graph;
// summing their weights gives a strictly negative value, which is a proof
// that no integer time assignment can satisfy the constraints.
type CycleError struct {
	edges []edge
	sum   int64
}

// Edges returns the edges of the negative cycle in cycle order.
func (e *CycleError) Edges() []edge { return append([]edge(nil), e.edges...) }

// Sum is the (strictly negative) total weight of the cycle.
func (e *CycleError) Sum() int64 { return e.sum }

func (e *CycleError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "netsec: infeasible constraints, negative cycle (sum=%d):\n", e.sum)
	for _, ed := range e.edges {
		fmt.Fprintf(&b, "  edge %d->%d, w=%d\n", ed.from, ed.to, ed.weight)
	}
	return b.String()
}

// Explain renders the negative cycle as the original ordering/bound
// constraints, which is the evidence a test report can attach directly.
func (e *CycleError) Explain(p *Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "negative cycle, sum=%d:\n", e.sum)
	for _, ed := range e.edges {
		fmt.Fprintf(&b, "  %s\n", p.describe(ed))
	}
	return b.String()
}

// buildGraph lowers the problem into difference-constraint edges. Node 0 is
// the anchor; event i occupies node i+1.
func (p *Problem) buildGraph(pins map[int]int64) []edge {
	n := len(p.names)
	edges := make([]edge, 0, 2*n+len(p.orders)+len(pins)*2)

	// Declared windows:
	//   t[i] <= hi[i]  -> t[i] - t[anchor] <= hi[i]:  edge anchor -> i+1
	//   t[i] >= lo[i]  -> t[anchor] - t[i] <= -lo[i]: edge i+1 -> anchor
	for i := 0; i < n; i++ {
		v := i + 1
		edges = append(edges,
			edge{anchorNode, v, p.hi[i]},
			edge{v, anchorNode, -p.lo[i]},
		)
	}

	// Ordering, edge u->v with weight w encodes t[v]-t[u] <= w:
	//   before <= after <=> t[before]-t[after] <= 0   -> after -> before, w=0
	//   before <  after <=> t[before]-t[after] <= -1  -> after -> before, w=-1
	for _, ord := range p.orders {
		w := int64(0)
		if ord.Kind == StrictOrder {
			w = -1
		}
		from := p.index[ord.After] + 1
		to := p.index[ord.Before] + 1
		edges = append(edges, edge{from, to, w})
	}

	// Pins force t[i] == v: both t[i] <= v and v <= t[i].
	for i, v := range pins {
		node := i + 1
		edges = append(edges,
			edge{anchorNode, node, v},
			edge{node, anchorNode, -v},
		)
	}
	return edges
}

// bellmanFord runs Bellman-Ford from the anchor. It returns the shortest
// distances, or a negative cycle (in cycle order) if one is reachable.
func bellmanFord(nodeCount int, edges []edge) ([]int64, []edge) {
	const inf = int64(1) << 62
	dist := make([]int64, nodeCount)
	predNode := make([]int, nodeCount)
	predEdge := make([]int, nodeCount)
	for i := range dist {
		dist[i] = inf
		predNode[i] = -1
		predEdge[i] = -1
	}
	dist[anchorNode] = 0

	// At most nodeCount-1 relaxation rounds are needed; run one extra round
	// and remember any edge that still relaxes: its destination sits on or
	// reaches a negative cycle.
	var cycleStart int = -1
	for round := 0; round < nodeCount; round++ {
		cycleStart = -1
		for edgeIndex, e := range edges {
			if dist[e.from] == inf {
				continue
			}
			nd := dist[e.from] + e.weight
			if nd < dist[e.to] {
				dist[e.to] = nd
				predNode[e.to] = e.from
				predEdge[e.to] = edgeIndex
				cycleStart = e.to
			}
		}
		if cycleStart == -1 {
			return dist, nil
		}
	}
	if cycleStart == -1 {
		return dist, nil
	}

	// Walk predecessor pointers nodeCount times to land guaranteed on the
	// cycle, then collect it until the node repeats.
	onCycle := cycleStart
	for i := 0; i < nodeCount; i++ {
		onCycle = predNode[onCycle]
	}
	seen := map[int]bool{onCycle: true}
	cycleNodes := []int{onCycle}
	for u := predNode[onCycle]; !seen[u]; u = predNode[u] {
		seen[u] = true
		cycleNodes = append(cycleNodes, u)
	}

	// Reverse into traversal order and map node pairs back to edges.
	for i, j := 0, len(cycleNodes)-1; i < j; i, j = i+1, j-1 {
		cycleNodes[i], cycleNodes[j] = cycleNodes[j], cycleNodes[i]
	}
	cycleEdges := make([]edge, 0, len(cycleNodes))
	var sum int64
	for _, node := range cycleNodes {
		ed := edges[predEdge[node]]
		cycleEdges = append(cycleEdges, ed)
		sum += ed.weight
	}
	return nil, cycleEdges
}

// allPairsShortest returns all-pairs shortest distances with a large
// sentinel for unreachable pairs. dist[u][v] is the tightest constant w such
// that t[v]-t[u] <= w. A Bellman-Ford run per source is used because the
// graph carries negative weights; feasibility (no negative cycle) is already
// established by the caller.
func allPairsShortest(nodeCount int, edges []edge) [][]int64 {
	const inf = int64(1) << 62
	dist := make([][]int64, nodeCount)
	for src := 0; src < nodeCount; src++ {
		row := make([]int64, nodeCount)
		for j := range row {
			row[j] = inf
		}
		row[src] = 0
		for round := 0; round < nodeCount-1; round++ {
			changed := false
			for _, e := range edges {
				if row[e.from] == inf {
					continue
				}
				if cand := row[e.from] + e.weight; cand < row[e.to] {
					row[e.to] = cand
					changed = true
				}
			}
			if !changed {
				break
			}
		}
		dist[src] = row
	}
	return dist
}

// solve runs the full analysis. pins optionally forces events to fixed
// integer values and is used by the boundary-sample generators.
func (p *Problem) solve(pins map[int]int64) (*Solution, *CycleError) {
	nodeCount := len(p.names) + 1
	edges := p.buildGraph(pins)

	if _, cycle := bellmanFord(nodeCount, edges); cycle != nil {
		var sum int64
		for _, e := range cycle {
			sum += e.weight
		}
		return nil, &CycleError{edges: cycle, sum: sum}
	}

	dist := allPairsShortest(nodeCount, edges)
	n := len(p.names)
	earliest := make([]int64, n)
	latest := make([]int64, n)
	for i := 0; i < n; i++ {
		v := i + 1
		// dist[a][b] is the tightest w with t[b]-t[a] <= w, anchor == 0:
		//   t[v] <= dist[anchor][v]            (upper bound)
		//   -t[v] <= dist[v][anchor] => t[v] >= -dist[v][anchor]
		earliest[i] = -dist[v][anchorNode]
		latest[i] = dist[anchorNode][v]
	}
	return &Solution{p: p, earliest: earliest, latest: latest}, nil
}

// Solve checks feasibility and, for a feasible instance, returns the
// earliest legal assignment plus each event's tight implied window. For a
// contradictory instance it returns a [CycleError] carrying negative-cycle
// evidence; callers should type-assert the error.
func (p *Problem) Solve() (*Solution, error) {
	sol, cyc := p.solve(nil)
	if cyc != nil {
		return nil, cyc
	}
	return sol, nil
}
