package main

type Cycle struct {
	Nodes  []int
	Weight int64
}

func (g *Graph) SimpleCycles() []Cycle {
	n := g.N()
	var out []Cycle
	for _, e := range g.Edges {
		if e.U == e.V {
			out = append(out, Cycle{Nodes: []int{e.U}, Weight: e.W})
		}
	}
	visited := make([]bool, n)
	var path []int
	var weight int64
	var dfs func(start, u int)
	dfs = func(start, u int) {
		for _, ei := range g.Adj[u] {
			e := g.Edges[ei]
			v := e.V
			if v == start {
				if len(path) >= 2 {
					nodes := make([]int, len(path))
					copy(nodes, path)
					out = append(out, Cycle{Nodes: nodes, Weight: weight + e.W})
				}
			} else if v > start && !visited[v] {
				visited[v] = true
				path = append(path, v)
				weight += e.W
				dfs(start, v)
				weight -= e.W
				path = path[:len(path)-1]
				visited[v] = false
			}
		}
	}
	for s := 0; s < n; s++ {
		visited[s] = true
		path = []int{s}
		weight = 0
		dfs(s, s)
		visited[s] = false
	}
	return out
}

func (g *Graph) MinSimplePath(src, dst int) (int64, bool) {
	if src == dst {
		return 0, true
	}
	visited := make([]bool, g.N())
	visited[src] = true
	best := inf
	var dfs func(u int, w int64)
	dfs = func(u int, w int64) {
		for _, ei := range g.Adj[u] {
			e := g.Edges[ei]
			if e.V == dst {
				if w+e.W < best {
					best = w + e.W
				}
			} else if !visited[e.V] {
				visited[e.V] = true
				dfs(e.V, w+e.W)
				visited[e.V] = false
			}
		}
	}
	dfs(src, 0)
	if best == inf {
		return 0, false
	}
	return best, true
}

func (g *Graph) FindNegativeCycleBetween(src, dst int) ([]int, int64, bool) {
	reach := g.ReachableFrom(src)
	back := g.ReachesTo(dst)
	for _, c := range g.SimpleCycles() {
		if c.Weight >= 0 {
			continue
		}
		for _, v := range c.Nodes {
			if reach[v] && back[v] {
				return c.Nodes, c.Weight, true
			}
		}
	}
	return nil, 0, false
}

func (g *Graph) BruteStatus(src, dst int) (Status, int64) {
	reach := g.ReachableFrom(src)
	if !reach[dst] {
		return Unreachable, 0
	}
	if _, _, ok := g.FindNegativeCycleBetween(src, dst); ok {
		return NegInf, 0
	}
	d, _ := g.MinSimplePath(src, dst)
	return Finite, d
}

func (g *Graph) DPStatus(src, dst int) (Status, int64) {
	n := g.N()
	prev := make([]int64, n)
	for i := range prev {
		prev[i] = inf
	}
	prev[src] = 0
	distN1 := prev[dst]
	dN := make([]int64, n)
	copy(dN, prev)
	for k := 1; k <= 2*n; k++ {
		cur := make([]int64, n)
		copy(cur, prev)
		for _, e := range g.Edges {
			if prev[e.U] != inf && prev[e.U]+e.W < cur[e.V] {
				cur[e.V] = prev[e.U] + e.W
			}
		}
		if k == n-1 {
			distN1 = cur[dst]
		}
		if k == n {
			copy(dN, cur)
		}
		prev = cur
	}
	if distN1 == inf {
		return Unreachable, 0
	}
	back := g.ReachesTo(dst)
	for u := 0; u < n; u++ {
		if prev[u] < dN[u] && back[u] {
			return NegInf, 0
		}
	}
	return Finite, distN1
}
