package fas

// Result is the outcome of a FAS approximation run.
type Result struct {
	Algorithm string // name of the heuristic that produced the result
	Removed   []Edge // feedback arc set: edges to cut
	Remaining []Edge // edges left in the graph (guaranteed acyclic)
	Order     []int  // topological order of the remaining graph
}

// RemovedWeight is the total weight of the cut edge set.
func (r *Result) RemovedWeight() float64 { return TotalWeight(r.Removed) }

// findCycle returns one cycle (as an edge list) or false if acyclic.
func findCycle(numNodes int, edges []Edge) ([]Edge, bool) {
	adj := make([][]int, numNodes)
	for i, e := range edges {
		if e.From == e.To {
			return []Edge{e}, true // self-loop
		}
		adj[e.From] = append(adj[e.From], i)
	}
	const (
		white = iota
		gray
		black
	)
	color := make([]int, numNodes)
	var path []int // edge indices on the current DFS stack
	var cycleIdx []int
	var visit func(u int) bool
	visit = func(u int) bool {
		color[u] = gray
		for _, ei := range adj[u] {
			e := edges[ei]
			switch color[e.To] {
			case gray:
				// Back edge: the cycle is the path from e.To down to u,
				// plus this closing edge.
				start := 0
				for i, pi := range path {
					if edges[pi].From == e.To {
						start = i
						break
					}
				}
				cycleIdx = append(append([]int(nil), path[start:]...), ei)
				return true
			case white:
				path = append(path, ei)
				if visit(e.To) {
					return true
				}
				path = path[:len(path)-1]
			}
		}
		color[u] = black
		return false
	}
	for v := 0; v < numNodes; v++ {
		if color[v] == white && visit(v) {
			cycle := make([]Edge, len(cycleIdx))
			for i, ei := range cycleIdx {
				cycle[i] = edges[ei]
			}
			return cycle, true
		}
	}
	return nil, false
}

// removeOne returns edges with the first occurrence of target removed.
func removeOne(edges []Edge, target Edge) []Edge {
	for i, e := range edges {
		if e == target {
			out := make([]Edge, 0, len(edges)-1)
			out = append(out, edges[:i]...)
			return append(out, edges[i+1:]...)
		}
	}
	return edges
}

// subtract returns base minus one occurrence of each edge in cut.
func subtract(base, cut []Edge) []Edge {
	out := append([]Edge(nil), base...)
	for _, e := range cut {
		out = removeOne(out, e)
	}
	return out
}

// Minimize makes a feedback arc set irredundant: every removed edge that
// can be put back without recreating a cycle is restored. The result is
// minimal by inclusion (removing any further edge from the FAS breaks the
// acyclicity guarantee).
func Minimize(numNodes int, removed, remaining []Edge) (minRemoved, minRemaining []Edge) {
	rem := append([]Edge(nil), remaining...)
	for _, e := range removed {
		if e.From != e.To && !hasPath(numNodes, rem, e.To, e.From) {
			rem = append(rem, e) // safe to restore
		} else {
			minRemoved = append(minRemoved, e)
		}
	}
	return minRemoved, rem
}

func buildResult(name string, g *Graph, removed []Edge) *Result {
	removed, remaining := Minimize(g.NumNodes, removed, subtract(g.Edges, removed))
	order, ok := TopologicalOrder(g.NumNodes, remaining)
	if !ok {
		panic("fas: internal error, remaining graph is cyclic")
	}
	return &Result{Algorithm: name, Removed: removed, Remaining: remaining, Order: order}
}

// GreedyCycleCut repeatedly finds a cycle and cuts its heaviest edge —
// the "key edge" shared by the most flow through that cycle — until the
// graph is acyclic, then minimizes the cut set.
func GreedyCycleCut(g *Graph) *Result {
	remaining := append([]Edge(nil), g.Edges...)
	var removed []Edge
	for {
		cycle, ok := findCycle(g.NumNodes, remaining)
		if !ok {
			break
		}
		heaviest := cycle[0]
		for _, e := range cycle[1:] {
			if e.Weight > heaviest.Weight {
				heaviest = e
			}
		}
		removed = append(removed, heaviest)
		remaining = removeOne(remaining, heaviest)
	}
	return buildResult("greedy-cycle-cut", g, removed)
}

// EadesOrder computes a vertex ordering with the weighted Eades-Lin-Smyth
// heuristic: sinks go to the tail, sources to the head, otherwise the
// vertex with the largest (out-weight - in-weight) is placed next.
func EadesOrder(g *Graph) []int {
	n := g.NumNodes
	inDeg := make([]int, n)
	outDeg := make([]int, n)
	inW := make([]float64, n)
	outW := make([]float64, n)
	preds := make([][]int, n) // edge indices
	succs := make([][]int, n) // edge indices
	for i, e := range g.Edges {
		if e.From == e.To {
			continue // self-loops are always cut, ignore for ordering
		}
		outDeg[e.From]++
		inDeg[e.To]++
		outW[e.From] += e.Weight
		inW[e.To] += e.Weight
		succs[e.From] = append(succs[e.From], i)
		preds[e.To] = append(preds[e.To], i)
	}
	active := make([]bool, n)
	for i := range active {
		active[i] = true
	}
	order := make([]int, n)
	left, right := 0, n-1
	remaining := n
	remove := func(v int) {
		active[v] = false
		remaining--
		for _, ei := range succs[v] {
			e := g.Edges[ei]
			if active[e.To] {
				inDeg[e.To]--
				inW[e.To] -= e.Weight
			}
		}
		for _, ei := range preds[v] {
			e := g.Edges[ei]
			if active[e.From] {
				outDeg[e.From]--
				outW[e.From] -= e.Weight
			}
		}
	}
	for remaining > 0 {
		progressed := true
		for progressed {
			progressed = false
			for v := 0; v < n; v++ {
				if active[v] && outDeg[v] == 0 { // sink -> tail
					order[right] = v
					right--
					remove(v)
					progressed = true
				}
			}
			for v := 0; v < n; v++ {
				if active[v] && inDeg[v] == 0 { // source -> head
					order[left] = v
					left++
					remove(v)
					progressed = true
				}
			}
		}
		if remaining == 0 {
			break
		}
		best := -1
		var bestScore float64
		for v := 0; v < n; v++ {
			if !active[v] {
				continue
			}
			if score := outW[v] - inW[v]; best == -1 || score > bestScore {
				best, bestScore = v, score
			}
		}
		order[left] = best
		left++
		remove(best)
	}
	return order
}

// FASFromOrder returns the edges that point backward in the given order;
// removing exactly those edges makes the graph acyclic.
func FASFromOrder(g *Graph, order []int) []Edge {
	pos := make([]int, g.NumNodes)
	for i, v := range order {
		pos[v] = i
	}
	var fas []Edge
	for _, e := range g.Edges {
		if e.From == e.To || pos[e.From] > pos[e.To] {
			fas = append(fas, e)
		}
	}
	return fas
}

// Hybrid runs both heuristics — greedy cycle cutting and the weighted
// topological (Eades) ordering — keeps the cheaper cut set, and then
// minimizes it. This is the recommended entry point.
func Hybrid(g *Graph) *Result {
	greedy := GreedyCycleCut(g)
	topo := FASFromOrder(g, EadesOrder(g))
	best := greedy.Removed
	if TotalWeight(topo) < TotalWeight(best) {
		best = topo
	}
	res := buildResult("hybrid(greedy+topological-weight)", g, best)
	return res
}
