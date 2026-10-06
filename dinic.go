package flowbounds

// arc is a residual-graph arc; cap is its residual capacity.
type arc struct {
	to, rev int
	cap     int64
}

// arcRef locates a forward arc in the adjacency lists.
type arcRef struct {
	from, idx int
}

// dinic is a Dinic max-flow solver over int64 capacities.
type dinic struct {
	g     [][]arc
	arcs  []arcRef // forward arcs in insertion order
	level []int
	it    []int
}

func newDinic(n int) *dinic {
	return &dinic{g: make([][]arc, n), level: make([]int, n), it: make([]int, n)}
}

// addEdge adds a forward arc u->v with the given capacity plus a zero
// capacity reverse arc, and returns the forward arc's index.
func (d *dinic) addEdge(u, v int, cap int64) int {
	d.g[u] = append(d.g[u], arc{to: v, rev: len(d.g[v]), cap: cap})
	d.g[v] = append(d.g[v], arc{to: u, rev: len(d.g[u]) - 1, cap: 0})
	d.arcs = append(d.arcs, arcRef{from: u, idx: len(d.g[u]) - 1})
	return len(d.arcs) - 1
}

// flowOn returns the flow pushed through the arc added as index i.
// The reverse arc starts at zero and accumulates exactly that amount.
func (d *dinic) flowOn(i int) int64 {
	ref := d.arcs[i]
	a := d.g[ref.from][ref.idx]
	return d.g[a.to][a.rev].cap
}

func (d *dinic) bfs(s, t int) bool {
	for i := range d.level {
		d.level[i] = -1
	}
	d.level[s] = 0
	queue := []int{s}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, a := range d.g[v] {
			if a.cap > 0 && d.level[a.to] < 0 {
				d.level[a.to] = d.level[v] + 1
				queue = append(queue, a.to)
			}
		}
	}
	return d.level[t] >= 0
}

func (d *dinic) dfs(v, t int, f int64) int64 {
	if v == t {
		return f
	}
	for ; d.it[v] < len(d.g[v]); d.it[v]++ {
		i := d.it[v]
		a := &d.g[v][i]
		if a.cap > 0 && d.level[a.to] == d.level[v]+1 {
			pushed := f
			if a.cap < pushed {
				pushed = a.cap
			}
			pushed = d.dfs(a.to, t, pushed)
			if pushed > 0 {
				a.cap -= pushed
				d.g[a.to][a.rev].cap += pushed
				return pushed
			}
		}
	}
	return 0
}

func (d *dinic) maxFlow(s, t int) int64 {
	var flow int64
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

// reachableFrom reports which nodes are reachable from s via arcs with
// positive residual capacity (the source side of a minimum cut).
func (d *dinic) reachableFrom(s int) []bool {
	reach := make([]bool, len(d.g))
	reach[s] = true
	queue := []int{s}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, a := range d.g[v] {
			if a.cap > 0 && !reach[a.to] {
				reach[a.to] = true
				queue = append(queue, a.to)
			}
		}
	}
	return reach
}
