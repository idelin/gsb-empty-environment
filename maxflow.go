package mincostflow

// dinic is a standard Dinic max-flow with int64 capacities, used only
// on the auxiliary feasibility network (super source/sink included).
type dinicArc struct {
	to, rev int
	cap     int64
}

type dinicArcRef struct{ node, idx int }

type dinic struct {
	arcs  [][]dinicArc
	level []int
	it    []int
}

func newDinic(n int) *dinic {
	return &dinic{arcs: make([][]dinicArc, n)}
}

// addEdge appends a u->v arc with capacity c (plus a zero-capacity
// reverse arc) and returns where the forward arc lives.
func (d *dinic) addEdge(u, v int, c int64) dinicArcRef {
	iu := len(d.arcs[u])
	d.arcs[u] = append(d.arcs[u], dinicArc{to: v, cap: c})
	iv := len(d.arcs[v])
	d.arcs[v] = append(d.arcs[v], dinicArc{to: u, cap: 0})
	d.arcs[u][iu].rev = iv
	d.arcs[v][iv].rev = iu
	return dinicArcRef{u, iu}
}

func (d *dinic) bfs(s, t int) bool {
	for i := range d.level {
		d.level[i] = -1
	}
	d.level[s] = 0
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, a := range d.arcs[u] {
			if a.cap > 0 && d.level[a.to] < 0 {
				d.level[a.to] = d.level[u] + 1
				queue = append(queue, a.to)
			}
		}
	}
	return d.level[t] >= 0
}

func (d *dinic) dfs(u, t int, f int64) int64 {
	if u == t {
		return f
	}
	for ; d.it[u] < len(d.arcs[u]); d.it[u]++ {
		a := &d.arcs[u][d.it[u]]
		if a.cap <= 0 || d.level[a.to] != d.level[u]+1 {
			continue
		}
		pushed := f
		if a.cap < pushed {
			pushed = a.cap
		}
		pushed = d.dfs(a.to, t, pushed)
		if pushed > 0 {
			a.cap -= pushed
			d.arcs[a.to][a.rev].cap += pushed
			return pushed
		}
	}
	return 0
}

func (d *dinic) maxFlow(s, t int) int64 {
	d.level = make([]int, len(d.arcs))
	d.it = make([]int, len(d.arcs))
	flow := int64(0)
	for d.bfs(s, t) {
		for i := range d.it {
			d.it[i] = 0
		}
		for {
			pushed := d.dfs(s, t, int64(^uint64(0)>>1))
			if pushed == 0 {
				break
			}
			flow += pushed
		}
	}
	return flow
}
