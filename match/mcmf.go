package match

// Min-cost flow on the assignment network:
//
//	source -> task      cap 1, cost 0
//	task   -> employee  cap 1, cost -profit
//	employee -> sink    cap 1, cost 0
//
// Successive shortest augmenting paths (Bellman-Ford each round, so
// negative costs are handled exactly) keep the flow optimal for its
// value; costs are integers, so all arithmetic is exact int64.

const infCost = int64(1) << 60

type fedge struct {
	to, rev, cap int
	cost         int64
}

type flowNet struct {
	g [][]fedge
}

func newFlowNet(n int) *flowNet { return &flowNet{g: make([][]fedge, n)} }

func (f *flowNet) addEdge(u, v int, cost int64) {
	f.g[u] = append(f.g[u], fedge{to: v, rev: len(f.g[v]), cap: 1, cost: cost})
	f.g[v] = append(f.g[v], fedge{to: u, rev: len(f.g[u]) - 1, cap: 0, cost: -cost})
}

// buildNet wires the given task/employee subsets into a flow network.
// Node layout: 0 = source, 1..T tasks, T+1..T+E employees, last = sink.
func (s *Solver) buildNet(tasks, employees []int64) (net *flowNet, src, sink int) {
	src = 0
	sink = 1 + len(tasks) + len(employees)
	net = newFlowNet(sink + 1)
	tIdx := make(map[int64]int, len(tasks))
	eIdx := make(map[int64]int, len(employees))
	for i, t := range tasks {
		tIdx[t] = 1 + i
		net.addEdge(src, 1+i, 0)
	}
	for j, e := range employees {
		eIdx[e] = 1 + len(tasks) + j
		net.addEdge(1+len(tasks)+j, sink, 0)
	}
	for k, p := range s.profits {
		ti, ok1 := tIdx[k[0]]
		ei, ok2 := eIdx[k[1]]
		if ok1 && ok2 {
			net.addEdge(ti, ei, -p)
		}
	}
	return net, src, sink
}

// minCostFlow augments up to `limit` units src->sink, each time along
// a minimum-cost residual path. It returns the flow value, its total
// cost, and feasible potentials of the final residual graph
// (pot[v] <= pot[u] + cost(u,v) on every residual arc), computed as
// shortest distances from a virtual super-source; they exist because
// the terminal flow has no negative-cost residual cycle.
func minCostFlow(net *flowNet, src, sink, limit int) (flow int, cost int64, pot []int64) {
	n := len(net.g)
	prevV := make([]int, n)
	prevE := make([]int, n)
	dist := make([]int64, n)
	for flow < limit {
		for i := range dist {
			dist[i] = infCost
		}
		dist[src] = 0
		for i := 0; i < n; i++ {
			updated := false
			for v := 0; v < n; v++ {
				if dist[v] >= infCost {
					continue
				}
				for ei, e := range net.g[v] {
					if e.cap > 0 && dist[v]+e.cost < dist[e.to] {
						dist[e.to] = dist[v] + e.cost
						prevV[e.to] = v
						prevE[e.to] = ei
						updated = true
					}
				}
			}
			if !updated {
				break
			}
		}
		if dist[sink] >= infCost {
			break
		}
		for v := sink; v != src; v = prevV[v] {
			e := &net.g[prevV[v]][prevE[v]]
			e.cap--
			net.g[v][e.rev].cap++
		}
		flow++
		cost += dist[sink]
	}
	pot = make([]int64, n)
	for i := 0; i < n; i++ {
		updated := false
		for v := 0; v < n; v++ {
			for _, e := range net.g[v] {
				if e.cap > 0 && pot[v]+e.cost < pot[e.to] {
					pot[e.to] = pot[v] + e.cost
					updated = true
				}
			}
		}
		if !updated {
			break
		}
	}
	return flow, cost, pot
}
