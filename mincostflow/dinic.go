package mincostflow

// dinic is a standard Dinic max-flow over int64 capacities.
type dinic struct {
	n     int
	to    []int
	cap   []int64
	next  []int // next edge with the same head in the adjacency list
	head  []int
	level []int
	it    []int
}

func newDinic(n int) *dinic {
	head := make([]int, n)
	for i := range head {
		head[i] = -1
	}
	return &dinic{n: n, head: head, level: make([]int, n), it: make([]int, n)}
}

// addEdge appends a directed edge u->v with capacity c plus its
// residual reverse edge, and returns the index of the forward edge.
func (d *dinic) addEdge(u, v int, c int64) int {
	idx := len(d.to)
	d.to = append(d.to, v)
	d.cap = append(d.cap, c)
	d.next = append(d.next, d.head[u])
	d.head[u] = idx
	d.to = append(d.to, u)
	d.cap = append(d.cap, 0)
	d.next = append(d.next, d.head[v])
	d.head[v] = idx + 1
	return idx
}

// flowOn returns the flow pushed through the forward edge idx.
func (d *dinic) flowOn(idx int) int64 {
	return d.cap[idx^1]
}

func (d *dinic) bfs(s, t int) bool {
	for i := range d.level {
		d.level[i] = -1
	}
	queue := make([]int, 0, d.n)
	d.level[s] = 0
	queue = append(queue, s)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for e := d.head[u]; e != -1; e = d.next[e] {
			if d.cap[e] > 0 && d.level[d.to[e]] < 0 {
				d.level[d.to[e]] = d.level[u] + 1
				queue = append(queue, d.to[e])
			}
		}
	}
	return d.level[t] >= 0
}

func (d *dinic) dfs(u, t int, f int64) int64 {
	if u == t {
		return f
	}
	for ; d.it[u] != -1; d.it[u] = d.next[d.it[u]] {
		e := d.it[u]
		v := d.to[e]
		if d.cap[e] > 0 && d.level[v] == d.level[u]+1 {
			if pushed := d.dfs(v, t, min64(f, d.cap[e])); pushed > 0 {
				d.cap[e] -= pushed
				d.cap[e^1] += pushed
				return pushed
			}
		}
	}
	return 0
}

func (d *dinic) maxFlow(s, t int) int64 {
	var total int64
	for d.bfs(s, t) {
		copy(d.it, d.head)
		for {
			pushed := d.dfs(s, t, int64(^uint64(0)>>1))
			if pushed == 0 {
				break
			}
			total += pushed
		}
	}
	return total
}

// reachable returns the set of nodes reachable from s via edges with
// positive residual capacity.
func (d *dinic) reachable(s int) []bool {
	seen := make([]bool, d.n)
	stack := []int{s}
	seen[s] = true
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for e := d.head[u]; e != -1; e = d.next[e] {
			if d.cap[e] > 0 && !seen[d.to[e]] {
				seen[d.to[e]] = true
				stack = append(stack, d.to[e])
			}
		}
	}
	return seen
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
