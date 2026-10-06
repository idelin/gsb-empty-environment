package flowsolve

// dinic is a Dinic max-flow over int64 capacities. Edge ids are stable:
// addEdge returns the id of the forward half-edge.
type dinic struct {
	head []int // first half-edge id per node, -1 terminated
	next []int // next half-edge id in adjacency list
	to   []int // endpoint of half-edge
	cap  []int64
	flow []int64
}

func newDinic(numNodes int) *dinic {
	head := make([]int, numNodes)
	for i := range head {
		head[i] = -1
	}
	return &dinic{head: head}
}

func (d *dinic) addHalf(u, v int, c int64) int {
	id := len(d.to)
	d.to = append(d.to, v)
	d.cap = append(d.cap, c)
	d.flow = append(d.flow, 0)
	d.next = append(d.next, d.head[u])
	d.head[u] = id
	return id
}

func (d *dinic) addEdge(u, v int, c int64) int {
	fwd := d.addHalf(u, v, c)
	d.addHalf(v, u, 0)
	return fwd
}

func (d *dinic) flowOn(id int) int64 { return d.flow[id] }

func (d *dinic) residual(id int) int64 { return d.cap[id] - d.flow[id] }

func (d *dinic) bfsLevels(s, t int) []int {
	level := make([]int, len(d.head))
	for i := range level {
		level[i] = -1
	}
	level[s] = 0
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for id := d.head[u]; id != -1; id = d.next[id] {
			v := d.to[id]
			if d.residual(id) > 0 && level[v] < 0 {
				level[v] = level[u] + 1
				queue = append(queue, v)
			}
		}
	}
	return level
}

func (d *dinic) push(u, t int, iter []int, level []int, f int64) int64 {
	if u == t {
		return f
	}
	for ; iter[u] != -1; iter[u] = d.next[iter[u]] {
		id := iter[u]
		v := d.to[id]
		if d.residual(id) <= 0 || level[v] != level[u]+1 {
			continue
		}
		pushed := d.push(v, t, iter, level, min64(f, d.residual(id)))
		if pushed > 0 {
			d.flow[id] += pushed
			d.flow[id^1] -= pushed
			return pushed
		}
	}
	return 0
}

func (d *dinic) maxFlow(s, t int) int64 {
	var total int64
	for {
		level := d.bfsLevels(s, t)
		if level[t] < 0 {
			return total
		}
		iter := make([]int, len(d.head))
		copy(iter, d.head)
		for {
			pushed := d.push(s, t, iter, level, int64(^uint64(0)>>1))
			if pushed == 0 {
				break
			}
			total += pushed
		}
	}
}

// reachable returns the set of nodes reachable from s via residual edges.
func (d *dinic) reachable(s int) []bool {
	seen := make([]bool, len(d.head))
	seen[s] = true
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for id := d.head[u]; id != -1; id = d.next[id] {
			v := d.to[id]
			if d.residual(id) > 0 && !seen[v] {
				seen[v] = true
				queue = append(queue, v)
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
