package main

type mfEdge struct {
	to  int
	rev int
	cap int64
}

// dinic computes a maximum s-t flow for integer capacities.
type dinic struct {
	g     [][]mfEdge
	level []int
	iter  []int
}

func newDinic(n int) *dinic {
	return &dinic{g: make([][]mfEdge, n), level: make([]int, n), iter: make([]int, n)}
}

func (d *dinic) addEdge(u, v int, c int64) {
	d.g[u] = append(d.g[u], mfEdge{to: v, rev: len(d.g[v]), cap: c})
	d.g[v] = append(d.g[v], mfEdge{to: u, rev: len(d.g[u]) - 1, cap: 0})
}

func (d *dinic) bfs(s int) {
	for i := range d.level {
		d.level[i] = -1
	}
	d.level[s] = 0
	q := []int{s}
	for len(q) > 0 {
		u := q[0]
		q = q[1:]
		for _, e := range d.g[u] {
			if e.cap > 0 && d.level[e.to] < 0 {
				mark := d.level[u] + 1
				d.level[e.to] = mark
				q = append(q, e.to)
			}
		}
	}
}

func (d *dinic) dfs(u, t int, f int64) int64 {
	if u == t {
		return f
	}
	for ; d.iter[u] < len(d.g[u]); d.iter[u]++ {
		i := d.iter[u]
		e := &d.g[u][i]
		if e.cap > 0 && d.level[u] < d.level[e.to] {
			pushed := d.dfs(e.to, t, min64(f, e.cap))
			if pushed > 0 {
				e.cap -= pushed
				d.g[e.to][e.rev].cap += pushed
				return pushed
			}
		}
	}
	return 0
}

const inf64 = int64(1) << 62

// maxFlow runs Dinic's algorithm; afterwards every arc with cap > 0 is a
// residual arc of the resulting maximum flow.
func (d *dinic) maxFlow(s, t int) int64 {
	var flow int64
	for {
		d.bfs(s)
		if d.level[t] < 0 {
			return flow
		}
		for i := range d.iter {
			d.iter[i] = 0
		}
		for {
			f := d.dfs(s, t, inf64)
			if f == 0 {
				break
			}
			flow += f
		}
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
