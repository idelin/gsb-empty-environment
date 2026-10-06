package edgerouter

// Enumerate finds the optimal path by exhaustively expanding every simple
// path in the state space (state = arrival edge). States are never repeated
// along a path, so zero-cost cycles cannot loop forever and the enumeration
// is finite. It is exponential and intended to cross-check ShortestPath on
// small graphs.
func (g *Graph) Enumerate(src, dst int) (Path, bool) {
	if src == dst {
		return Path{Total: 0, Edges: nil}, true
	}

	var best Path
	found := false
	visited := make(map[int]bool)

	var dfs func(edge int, cost int64, seq []int)
	dfs = func(edge int, cost int64, seq []int) {
		// Prune: costs are non-negative and appending edges only grows a
		// sequence lexicographically, so no extension of a label that is
		// already worse than the incumbent can ever win.
		if found && !lessPath(cost, seq, best.Total, best.Edges) {
			return
		}
		if g.edges[edge].To == dst {
			best = Path{Total: cost, Edges: append([]int(nil), seq...)}
			found = true
			return
		}
		visited[edge] = true
		for _, next := range g.out[g.edges[edge].To] {
			if visited[next] {
				continue
			}
			extra, ok := g.transition(edge, next)
			if !ok {
				continue
			}
			dfs(next, cost+extra, append(seq, next))
		}
		visited[edge] = false
	}

	for _, id := range g.out[src] {
		e := g.edges[id]
		dfs(id, e.Time, []int{id})
	}
	return best, found
}
