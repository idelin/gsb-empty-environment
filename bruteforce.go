package router

// BruteForce is a reference implementation used to cross-check ShortestPath
// on small graphs. It enumerates every state-simple route (no arrival edge
// repeated) from src to dst by exhaustive state expansion and returns the
// best one under the same ordering as ShortestPath: minimum total cost,
// ties broken by the lexicographically smallest edge-id sequence.
//
// Because a repeated arrival-edge state closes a cycle — forbidden when
// zero-cost and never beneficial otherwise — restricting enumeration to
// state-simple routes does not change the answer, and it makes the
// enumeration finite. Running time is exponential; use only on small
// graphs.
func (g *Graph) BruteForce(src, dst int) (path []int, cost int64, ok bool) {
	if src == dst {
		return nil, 0, true
	}
	var best label
	onPath := make([]bool, len(g.edges)) // per edge index
	var cur []int

	var dfs func(edgeIdx int, c int64)
	dfs = func(edgeIdx int, c int64) {
		e := g.edges[edgeIdx]
		onPath[edgeIdx] = true
		cur = append(cur, e.ID)
		defer func() {
			cur = cur[:len(cur)-1]
			onPath[edgeIdx] = false
		}()

		if e.To == dst {
			if better(c, cur, best) {
				best = label{cost: c, path: append([]int(nil), cur...), ok: true}
			}
			return
		}
		for _, nextIdx := range g.out[e.To] {
			if onPath[nextIdx] {
				continue
			}
			ne := g.edges[nextIdx]
			wait, allowed := g.turnCost(e.ID, ne.ID)
			if !allowed {
				continue
			}
			dfs(nextIdx, c+wait+ne.Time)
		}
	}

	for _, idx := range g.out[src] {
		dfs(idx, g.edges[idx].Time)
	}

	if !best.ok {
		return nil, 0, false
	}
	return best.path, best.cost, true
}
