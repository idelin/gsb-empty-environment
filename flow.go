package main

// Dinic computes a maximum flow with int64 capacities.

type flowEdge struct {
	to  int
	rev int
	cap int64
}

type Dinic struct {
	n   int
	adj [][]flowEdge
}

func NewDinic(n int) *Dinic {
	return &Dinic{n: n, adj: make([][]flowEdge, n)}
}

func (d *Dinic) AddEdge(u, v int, c int64) {
	d.adj[u] = append(d.adj[u], flowEdge{to: v, rev: len(d.adj[v]), cap: c})
	d.adj[v] = append(d.adj[v], flowEdge{to: u, rev: len(d.adj[u]) - 1, cap: 0})
}

func (d *Dinic) MaxFlow(s, t int) int64 {
	var flow int64
	level := make([]int, d.n)
	it := make([]int, d.n)
	for {
		for i := range level {
			level[i] = -1
		}
		level[s] = 0
		queue := []int{s}
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			for _, e := range d.adj[u] {
				if e.cap > 0 && level[e.to] < 0 {
					level[e.to] = level[u] + 1
					queue = append(queue, e.to)
				}
			}
		}
		if level[t] < 0 {
			return flow
		}
		for i := range it {
			it[i] = 0
		}
		for {
			pushed := d.augment(s, t, level, it, int64(^uint64(0)>>1))
			if pushed == 0 {
				break
			}
			flow += pushed
		}
	}
}

func (d *Dinic) augment(u, t int, level, it []int, f int64) int64 {
	if u == t {
		return f
	}
	for ; it[u] < len(d.adj[u]); it[u]++ {
		e := &d.adj[u][it[u]]
		if e.cap > 0 && level[e.to] == level[u]+1 {
			pushed := d.augment(e.to, t, level, it, min64(f, e.cap))
			if pushed > 0 {
				e.cap -= pushed
				d.adj[e.to][e.rev].cap += pushed
				return pushed
			}
		}
	}
	return 0
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// residualAdjacency returns the arcs with positive residual capacity.
func residualAdjacency(d *Dinic) [][]int {
	adj := make([][]int, d.n)
	for u := 0; u < d.n; u++ {
		for _, e := range d.adj[u] {
			if e.cap > 0 {
				adj[u] = append(adj[u], e.to)
			}
		}
	}
	return adj
}

// maxFlowValue runs Dinic on g and returns the max-flow value together
// with the residual adjacency of the resulting flow.
func maxFlowValue(g *Graph) (int64, [][]int) {
	d := NewDinic(g.N)
	for _, e := range g.Edges {
		d.AddEdge(e.U, e.V, e.C)
	}
	f := d.MaxFlow(g.S, g.T)
	return f, residualAdjacency(d)
}
