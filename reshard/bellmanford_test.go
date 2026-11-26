package reshard

import "math/big"

// bellmanFord independently enumerates the full reachable state graph and
// relaxes every edge |V|-1 times. It returns the exact scaled minimum cost
// for goal and whether goal is reachable, with no reliance on Dijkstra.
func bellmanFord(p Problem, start, goal state) (*big.Int, bool) {
	solver := newSolver(p)
	inf := new(big.Int).Lsh(big.NewInt(1), 200)

	index := map[string]int{stateKey(start): 0}
	states := []state{start}
	type graphEdge struct {
		from, to int
		weight   *big.Int
	}
	var edges []graphEdge

	for queue := []state{start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		from := index[stateKey(cur)]
		for _, e := range solver.edges(cur) {
			key := stateKey(e.next)
			to, seen := index[key]
			if !seen {
				to = len(states)
				index[key] = to
				states = append(states, e.next)
				queue = append(queue, e.next)
			}
			edges = append(edges, graphEdge{from: from, to: to, weight: e.weight})
		}
	}

	dist := make([]*big.Int, len(states))
	for i := range dist {
		dist[i] = new(big.Int).Set(inf)
	}
	dist[0] = big.NewInt(0)
	for iter := 0; iter < len(states)-1; iter++ {
		changed := false
		for _, e := range edges {
			cand := new(big.Int).Add(dist[e.from], e.weight)
			if cand.Cmp(dist[e.to]) < 0 {
				dist[e.to] = cand
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	goalIdx, ok := index[stateKey(goal)]
	if !ok || dist[goalIdx].Cmp(inf) >= 0 {
		return nil, false
	}
	return dist[goalIdx], true
}
