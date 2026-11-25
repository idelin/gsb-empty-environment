package migration

import "math"

type flowEdge struct {
	to  int
	rev int
	cap int
}

type dinic struct {
	graph        [][]flowEdge
	level        []int
	pointer      []int
	source       int
	sink         int
	requestCount int
}

func newDinic(requestCount, cellCount int, cellCapacities []int) *dinic {
	nodeCount := 1 + requestCount + cellCount + 1
	d := &dinic{
		graph:        make([][]flowEdge, nodeCount),
		level:        make([]int, nodeCount),
		pointer:      make([]int, nodeCount),
		source:       0,
		sink:         nodeCount - 1,
		requestCount: requestCount,
	}
	for request := 0; request < requestCount; request++ {
		d.addEdge(d.source, d.requestNode(request), 1)
	}
	for cell := 0; cell < cellCount; cell++ {
		d.addEdge(d.cellNode(requestCount, cell), d.sink, cellCapacities[cell])
	}
	return d
}

func (d *dinic) requestNode(request int) int {
	return 1 + request
}

func (d *dinic) cellNode(requestCount, cell int) int {
	return 1 + requestCount + cell
}

func (d *dinic) addEdge(from, to, capacity int) {
	fromIndex := len(d.graph[from])
	toIndex := len(d.graph[to])
	forward := flowEdge{to: to, rev: toIndex, cap: capacity}
	reverse := flowEdge{to: from, rev: fromIndex, cap: 0}
	d.graph[from] = append(d.graph[from], forward)
	d.graph[to] = append(d.graph[to], reverse)
}

func (d *dinic) addRequestEdges(requestIndex int, possible RangeSet, cells []Interval) {
	for _, interval := range possible.Intervals {
		for current := 0; current < len(cells); current++ {
			if interval.Contains(cells[current].Start) {
				d.addEdge(d.requestNode(requestIndex), d.cellNode(d.requestCount, current), 1)
			}
		}
	}
}

func (d *dinic) maxFlow() int {
	flow := 0
	for d.bfs() {
		clearInts(d.pointer)
		for {
			add := d.dfs(d.source, math.MaxInt)
			if add == 0 {
				break
			}
			flow += add
		}
	}
	return flow
}

func (d *dinic) bfs() bool {
	clearInts(d.level)
	d.level[d.source] = 1
	queue := []int{d.source}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, edge := range d.graph[node] {
			if edge.cap > 0 && d.level[edge.to] == 0 {
				d.level[edge.to] = d.level[node] + 1
				queue = append(queue, edge.to)
			}
		}
	}
	return d.level[d.sink] != 0
}

func (d *dinic) dfs(node int, pushed int) int {
	if node == d.sink || pushed == 0 {
		return pushed
	}
	for d.pointer[node] < len(d.graph[node]) {
		edgeIndex := d.pointer[node]
		edge := d.graph[node][edgeIndex]
		if edge.cap == 0 || d.level[edge.to] != d.level[node]+1 {
			d.pointer[node]++
			continue
		}
		add := d.dfs(edge.to, minInt(pushed, edge.cap))
		if add == 0 {
			d.pointer[node]++
			continue
		}
		d.graph[node][edgeIndex].cap -= add
		d.graph[edge.to][edge.rev].cap += add
		return add
	}
	return 0
}

func clearInts(values []int) {
	for index := range values {
		values[index] = 0
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt() int {
	if ^uint(0)>>63 == 0 {
		return math.MaxInt32
	}
	return math.MaxInt64
}
