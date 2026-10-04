package ssp

// Evidence witnesses, for a NegInf target, one concrete simple negative
// cycle that is reachable from the source and can reach the target,
// together with the connecting paths.
type Evidence struct {
	Cycle       []int   // simple negative cycle, in traversal order
	CycleWeight float64 // total weight of the cycle (< 0)
	FromSource  []int   // a path from the source to Cycle[0]
	ToTarget    []int   // a path from Cycle[0] to the target
	CycleEdges  []int   // edge indices realising the cycle hops
}

// Evidence produces a witness for why target t has status NegInf: a simple
// negative cycle reachable from the source whose vertices can reach t.
// It returns nil when t is not NegInf.
//
// Method: any vertex still relaxable in the n-th Bellman-Ford round sits on
// or downstream of a reachable negative cycle. Walking predecessor pointers
// n steps from such a vertex is guaranteed to land on a negative cycle, and
// following predecessors until a vertex repeats extracts a simple cycle.
// Only candidates that can reach t are considered, so the reported cycle
// always satisfies both reachability sides; existence of such a candidate
// is guaranteed exactly when t is NegInf.
func (r *Result) Evidence(g *Graph, t int) *Evidence {
	n := g.NumVertices()
	if t < 0 || t >= n || r.Status[t] != NegInf {
		return nil
	}
	canReachT := g.CanReach(t)

	for _, start := range r.relaxed {
		if !canReachT[start] {
			continue
		}
		// Walk n predecessor steps to be sure we are on a cycle.
		x := start
		ok := true
		for i := 0; i < n; i++ {
			if r.Pred[x] < 0 {
				ok = false
				break
			}
			x = r.Pred[x]
		}
		if !ok {
			continue
		}
		// Follow predecessors until a vertex repeats: a simple cycle.
		pos := make(map[int]int)
		var chain []int
		cur := x
		for {
			if p, seen := pos[cur]; seen {
				chain = chain[p:]
				break
			}
			pos[cur] = len(chain)
			chain = append(chain, cur)
			if r.Pred[cur] < 0 {
				ok = false
				break
			}
			cur = r.Pred[cur]
		}
		if !ok || len(chain) == 0 {
			continue
		}
		// chain is in predecessor (reverse) order; flip to edge direction.
		cycle := make([]int, len(chain))
		for i, v := range chain {
			cycle[len(chain)-1-i] = v
		}
		// Collect the realising edges and the total weight.
		edges := make([]int, len(cycle))
		weight := 0.0
		valid := true
		for i, u := range cycle {
			v := cycle[(i+1)%len(cycle)]
			ei := r.PredEdge[v]
			if ei < 0 || g.Edges[ei].From != u || g.Edges[ei].To != v {
				valid = false
				break
			}
			edges[i] = ei
			weight += g.Edges[ei].Weight
		}
		if !valid || weight >= 0 {
			continue
		}
		entry := cycle[0]
		fromSource := g.BFSPath(r.Source, entry)
		toTarget := g.BFSPath(entry, t)
		if fromSource == nil || toTarget == nil {
			continue
		}
		return &Evidence{
			Cycle:       cycle,
			CycleWeight: weight,
			FromSource:  fromSource,
			ToTarget:    toTarget,
			CycleEdges:  edges,
		}
	}
	return nil
}
