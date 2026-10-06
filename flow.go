package contab

// flow is a Dinic max-flow over int capacities. Nodes are 0..n-1.
type flow struct {
	graph [][]flowEdge
	edges []edgeRef // handles returned by addEdge, in order
	level []int
	iter  []int
}

type flowEdge struct {
	to, rev, cap int
}

type edgeRef struct {
	from, idx int
}

func newFlow(n int) *flow {
	return &flow{graph: make([][]flowEdge, n), level: make([]int, n), iter: make([]int, n)}
}

// addEdge appends a forward arc and returns a handle for edgeFlow.
func (f *flow) addEdge(from, to, cap int) int {
	f.graph[from] = append(f.graph[from], flowEdge{to: to, rev: len(f.graph[to]), cap: cap})
	f.graph[to] = append(f.graph[to], flowEdge{to: from, rev: len(f.graph[from]) - 1, cap: 0})
	f.edges = append(f.edges, edgeRef{from: from, idx: len(f.graph[from]) - 1})
	return len(f.edges) - 1
}

// edgeFlow returns the flow pushed through the arc behind handle h: it equals
// the residual capacity of the arc's reverse edge.
func (f *flow) edgeFlow(h int) int {
	ref := f.edges[h]
	e := f.graph[ref.from][ref.idx]
	return f.graph[e.to][e.rev].cap
}

func (f *flow) bfs(s, t int) bool {
	for i := range f.level {
		f.level[i] = -1
	}
	queue := []int{s}
	f.level[s] = 0
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, e := range f.graph[v] {
			if e.cap > 0 && f.level[e.to] < 0 {
				f.level[e.to] = f.level[v] + 1
				queue = append(queue, e.to)
			}
		}
	}
	return f.level[t] >= 0
}

func (f *flow) dfs(v, t, pushed int) int {
	if v == t || pushed == 0 {
		return pushed
	}
	for ; f.iter[v] < len(f.graph[v]); f.iter[v]++ {
		i := f.iter[v]
		e := &f.graph[v][i]
		if e.cap <= 0 || f.level[e.to] != f.level[v]+1 {
			continue
		}
		d := f.dfs(e.to, t, min(pushed, e.cap))
		if d > 0 {
			e.cap -= d
			f.graph[e.to][e.rev].cap += d
			return d
		}
	}
	return 0
}

func (f *flow) maxFlow(s, t int) int {
	total := 0
	for f.bfs(s, t) {
		for i := range f.iter {
			f.iter[i] = 0
		}
		for {
			pushed := f.dfs(s, t, int(^uint(0)>>1))
			if pushed == 0 {
				break
			}
			total += pushed
		}
	}
	return total
}

// reachable returns the nodes reachable from s via residual capacity,
// i.e. the source side of a min cut after maxFlow.
func (f *flow) reachable(s int) []bool {
	seen := make([]bool, len(f.graph))
	stack := []int{s}
	seen[s] = true
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range f.graph[v] {
			if e.cap > 0 && !seen[e.to] {
				seen[e.to] = true
				stack = append(stack, e.to)
			}
		}
	}
	return seen
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
