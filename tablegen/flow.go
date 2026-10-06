package tablegen

// flowEdge is one directed arc; residual capacity is cap, orig is the
// initial capacity, so the carried flow is orig-cap on forward arcs.
type flowEdge struct {
	to, rev   int
	cap, orig int
}

// flowNet is a directed flow network addressed by edge indices.
type flowNet struct {
	edges []flowEdge
	adj   [][]int
}

func newFlowNet(n int) *flowNet {
	return &flowNet{adj: make([][]int, n)}
}

// addEdge appends a forward arc u->v with the given capacity plus its
// residual back-arc, and returns the forward arc's index.
func (f *flowNet) addEdge(u, v, cap int) int {
	fwd := len(f.edges)
	f.edges = append(f.edges,
		flowEdge{to: v, rev: fwd + 1, cap: cap, orig: cap},
		flowEdge{to: u, rev: fwd, cap: 0, orig: 0},
	)
	f.adj[u] = append(f.adj[u], fwd)
	f.adj[v] = append(f.adj[v], fwd+1)
	return fwd
}

// flowOn reports the flow carried by a forward arc.
func (f *flowNet) flowOn(e int) int {
	return f.edges[e].orig - f.edges[e].cap
}

// maxFlow computes Dinic's maximum flow from s to t.
func (f *flowNet) maxFlow(s, t int) int {
	total := 0
	for {
		level := f.bfsLevels(s, t)
		if level[t] < 0 {
			return total
		}
		next := make([]int, len(f.adj))
		for {
			pushed := f.pushFlow(s, t, 1<<30, level, next)
			if pushed == 0 {
				break
			}
			total += pushed
		}
	}
}

func (f *flowNet) bfsLevels(s, t int) []int {
	level := make([]int, len(f.adj))
	for i := range level {
		level[i] = -1
	}
	level[s] = 0
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range f.adj[u] {
			e := f.edges[ei]
			if e.cap > 0 && level[e.to] < 0 {
				level[e.to] = level[u] + 1
				queue = append(queue, e.to)
			}
		}
	}
	return level
}

func (f *flowNet) pushFlow(u, t, limit int, level, next []int) int {
	if u == t {
		return limit
	}
	for ; next[u] < len(f.adj[u]); next[u]++ {
		ei := f.adj[u][next[u]]
		e := &f.edges[ei]
		if e.cap > 0 && level[e.to] == level[u]+1 {
			d := limit
			if e.cap < d {
				d = e.cap
			}
			if pushed := f.pushFlow(e.to, t, d, level, next); pushed > 0 {
				e.cap -= pushed
				f.edges[e.rev].cap += pushed
				return pushed
			}
		}
	}
	return 0
}

// reachableFrom returns the set of nodes reachable from s via arcs with
// positive residual capacity (the source side of a min cut).
func (f *flowNet) reachableFrom(s int) []bool {
	reach := make([]bool, len(f.adj))
	reach[s] = true
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range f.adj[u] {
			e := f.edges[ei]
			if e.cap > 0 && !reach[e.to] {
				reach[e.to] = true
				queue = append(queue, e.to)
			}
		}
	}
	return reach
}
