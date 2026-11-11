package netsec

// loopHeads finds targets of back edges via DFS from the entry.
func loopHeads(g *Graph) map[int]bool {
	color := make([]int, g.nodes)
	heads := make(map[int]bool)
	var dfs func(n int)
	dfs = func(n int) {
		color[n] = 1
		for _, ei := range g.out[n] {
			t := g.edges[ei].To
			switch color[t] {
			case 0:
				dfs(t)
			case 1:
				heads[t] = true
			}
		}
		color[n] = 2
	}
	dfs(g.Entry())
	return heads
}

// transfer applies an edge to a copy of src; nil means infeasible.
func transfer(src *dbm, e *Edge) *dbm {
	s := src.clone()
	switch e.Kind {
	case AssignEdge:
		s.assignAdd(e.X, e.C)
	case GuardEdge:
		s.guard(e.X, e.Y, e.Lo, e.Hi)
	}
	if !s.close() {
		return nil
	}
	return s
}

// ascend is the upward fixpoint pass: worklist propagation, joining at
// merge points and widening at loop heads after WidenDelay joins.
func ascend(g *Graph, heads map[int]bool, opt Options) []*dbm {
	pre := make([]*dbm, g.nodes)
	pre[g.Entry()] = newDBM(len(g.vars))
	joins := make(map[int]int)

	queue := []int{g.Entry()}
	inQ := make([]bool, g.nodes)
	inQ[g.Entry()] = true
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		inQ[n] = false
		if pre[n] == nil {
			continue
		}
		for _, ei := range g.out[n] {
			e := &g.edges[ei]
			s := transfer(pre[n], e)
			if s == nil {
				continue
			}
			m := e.To
			var combined *dbm
			switch {
			case pre[m] == nil:
				combined = s
			default:
				combined = join(pre[m], s)
				if heads[m] {
					joins[m]++
					if joins[m] > opt.WidenDelay {
						combined = widen(pre[m], combined)
					}
				}
			}
			if !combined.equal(pre[m]) {
				pre[m] = combined
				if !inQ[m] {
					queue = append(queue, m)
					inQ[m] = true
				}
			}
		}
	}
	return pre
}

// descend is the downward fixpoint pass: recompute each node from its
// predecessors and narrow at loop heads, for at most NarrowIters sweeps.
func descend(g *Graph, heads map[int]bool, pre []*dbm, opt Options) []*dbm {
	for it := 0; it < opt.NarrowIters; it++ {
		changed := false
		next := make([]*dbm, g.nodes)
		next[g.Entry()] = pre[g.Entry()]
		for m := 0; m < g.nodes; m++ {
			if m == g.Entry() {
				continue
			}
			var acc *dbm
			for _, ei := range g.in[m] {
				e := &g.edges[ei]
				if pre[e.From] == nil {
					continue
				}
				s := transfer(pre[e.From], e)
				if s == nil {
					continue
				}
				if acc == nil {
					acc = s
				} else {
					acc = join(acc, s)
				}
			}
			if acc == nil {
				continue // unreachable
			}
			if heads[m] && pre[m] != nil {
				acc = narrow(pre[m], acc)
			}
			next[m] = acc
			if !acc.equal(pre[m]) {
				changed = true
			}
		}
		pre = next
		if !changed {
			break
		}
	}
	return pre
}
