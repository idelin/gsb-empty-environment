package main

type Status int

const (
	Unreachable Status = iota
	Finite
	NegInf
)

func (s Status) String() string {
	switch s {
	case Unreachable:
		return "unreachable"
	case Finite:
		return "finite"
	case NegInf:
		return "-inf"
	}
	return "?"
}

type TargetResult struct {
	Status      Status
	Dist        int64
	Path        []int
	Cycle       []int
	CycleWeight int64
	ToCycle     []int
	FromCycle   []int
}

const inf = int64(1) << 60

func (g *Graph) ShortestFrom(src int) []TargetResult {
	n := g.N()
	dist := make([]int64, n)
	pred := make([]int, n)
	for i := range dist {
		dist[i] = inf
		pred[i] = -1
	}
	dist[src] = 0
	dirty := make([]bool, n)
	for i := 0; i < n; i++ {
		relaxed := false
		for _, e := range g.Edges {
			if dist[e.U] == inf {
				continue
			}
			if d := dist[e.U] + e.W; d < dist[e.V] {
				dist[e.V] = d
				pred[e.V] = e.U
				relaxed = true
				if i == n-1 {
					dirty[e.V] = true
				}
			}
		}
		if !relaxed {
			break
		}
	}
	affected := make([]bool, n)
	var queue []int
	for v := 0; v < n; v++ {
		if dirty[v] {
			affected[v] = true
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range g.Adj[u] {
			v := g.Edges[ei].V
			if !affected[v] {
				affected[v] = true
				queue = append(queue, v)
			}
		}
	}
	res := make([]TargetResult, n)
	for t := 0; t < n; t++ {
		switch {
		case dist[t] == inf:
			res[t] = TargetResult{Status: Unreachable}
		case affected[t]:
			res[t] = g.buildWitness(src, t, dirty, pred)
		default:
			res[t] = TargetResult{Status: Finite, Dist: dist[t], Path: predPath(pred, src, t)}
		}
	}
	return res
}

func predPath(pred []int, src, dst int) []int {
	var rev []int
	seen := map[int]bool{}
	for cur := dst; ; cur = pred[cur] {
		rev = append(rev, cur)
		if cur == src {
			break
		}
		if seen[cur] || pred[cur] < 0 {
			return nil
		}
		seen[cur] = true
	}
	path := make([]int, len(rev))
	for i, v := range rev {
		path[len(rev)-1-i] = v
	}
	return path
}

func (g *Graph) buildWitness(src, dst int, dirty []bool, pred []int) TargetResult {
	reaches := g.ReachesTo(dst)
	for y := 0; y < g.N(); y++ {
		if !dirty[y] || !reaches[y] {
			continue
		}
		if cyc, w, ok := g.extractCycle(y, pred); ok && w < 0 {
			entry := cyc[0]
			return TargetResult{
				Status:      NegInf,
				Cycle:       cyc,
				CycleWeight: w,
				ToCycle:     g.BFSPath(src, entry),
				FromCycle:   g.BFSPath(entry, dst),
			}
		}
	}
	if cyc, w, ok := g.FindNegativeCycleBetween(src, dst); ok {
		entry := cyc[0]
		return TargetResult{
			Status:      NegInf,
			Cycle:       cyc,
			CycleWeight: w,
			ToCycle:     g.BFSPath(src, entry),
			FromCycle:   g.BFSPath(entry, dst),
		}
	}
	return TargetResult{Status: NegInf}
}

func (g *Graph) extractCycle(y int, pred []int) ([]int, int64, bool) {
	n := g.N()
	x := y
	for i := 0; i < n; i++ {
		if pred[x] < 0 {
			return nil, 0, false
		}
		x = pred[x]
	}
	rev := []int{x}
	seen := map[int]bool{x: true}
	for cur := pred[x]; ; cur = pred[cur] {
		if cur < 0 {
			return nil, 0, false
		}
		if cur == x {
			break
		}
		if seen[cur] {
			return nil, 0, false
		}
		seen[cur] = true
		rev = append(rev, cur)
	}
	cyc := make([]int, len(rev))
	for i, v := range rev {
		cyc[len(rev)-1-i] = v
	}
	var w int64
	for i := 0; i < len(cyc); i++ {
		to := cyc[(i+1)%len(cyc)]
		ew, ok := g.MinEdgeWeight(cyc[i], to)
		if !ok {
			return nil, 0, false
		}
		w += ew
	}
	return cyc, w, true
}
