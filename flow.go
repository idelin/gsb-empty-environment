package rematch

// maxMatching returns the size of a maximum bipartite matching between
// employees (rows of allowed) and m positions (Kuhn's algorithm).
func maxMatching(allowed [][]bool, m int) int {
	matchTo := make([]int, m)
	for j := range matchTo {
		matchTo[j] = -1
	}
	seen := make([]int, m)
	stamp := 0
	var try func(e int) bool
	try = func(e int) bool {
		for j := 0; j < m; j++ {
			if !allowed[e][j] || seen[j] == stamp {
				continue
			}
			seen[j] = stamp
			if matchTo[j] == -1 || try(matchTo[j]) {
				matchTo[j] = e
				return true
			}
		}
		return false
	}
	size := 0
	for e := range allowed {
		stamp++
		if try(e) {
			size++
		}
	}
	return size
}

type flowEdge struct {
	to, rev, cap, cost int
}

type flowGraph struct {
	g [][]flowEdge
}

func newFlowGraph(n int) *flowGraph {
	return &flowGraph{g: make([][]flowEdge, n)}
}

func (f *flowGraph) addEdge(u, v, cap, cost int) {
	f.g[u] = append(f.g[u], flowEdge{to: v, rev: len(f.g[v]), cap: cap, cost: cost})
	f.g[v] = append(f.g[v], flowEdge{to: u, rev: len(f.g[u]) - 1, cap: 0, cost: -cost})
}

// minCostFlow augments up to target units from src to sink along
// shortest-cost paths (Bellman-Ford) and returns the achieved flow and its
// total cost.
func minCostFlow(fg *flowGraph, src, sink, target int) (flow, cost int) {
	g := fg.g
	n := len(g)
	const inf = int(^uint(0) >> 1)
	for flow < target {
		dist := make([]int, n)
		for i := range dist {
			dist[i] = inf
		}
		prevV := make([]int, n)
		prevE := make([]int, n)
		dist[src] = 0
		for updated := true; updated; {
			updated = false
			for v := 0; v < n; v++ {
				if dist[v] == inf {
					continue
				}
				for ei, e := range g[v] {
					if e.cap > 0 && dist[e.to] > dist[v]+e.cost {
						dist[e.to] = dist[v] + e.cost
						prevV[e.to] = v
						prevE[e.to] = ei
						updated = true
					}
				}
			}
		}
		if dist[sink] == inf {
			break
		}
		d := target - flow
		for v := sink; v != src; v = prevV[v] {
			if c := g[prevV[v]][prevE[v]].cap; c < d {
				d = c
			}
		}
		flow += d
		cost += d * dist[sink]
		for v := sink; v != src; v = prevV[v] {
			e := &g[prevV[v]][prevE[v]]
			e.cap -= d
			g[v][e.rev].cap += d
		}
	}
	return flow, cost
}
