package suppress

// dinic 是整数容量的 Dinic 最大流实现；表至多 4×4，网络不超过 10 个节点。
type dinic struct {
	n     int
	adj   [][]int
	edges []dEdge
}

type dEdge struct {
	to  int
	rev int
	cap int
}

func newDinic(n int) *dinic {
	return &dinic{n: n, adj: make([][]int, n)}
}

func (d *dinic) addEdge(from, to, cap int) int {
	idx := len(d.edges)
	d.edges = append(d.edges, dEdge{to: to, rev: idx + 1, cap: cap})
	d.edges = append(d.edges, dEdge{to: from, rev: idx, cap: 0})
	d.adj[from] = append(d.adj[from], idx)
	d.adj[to] = append(d.adj[to], idx+1)
	return idx
}

// flow 返回正向边当前已承载的流量。
func (d *dinic) flow(idx int) int {
	return d.edges[d.edges[idx].rev].cap
}

func (d *dinic) maxFlow(source, sink int) int {
	total := 0
	for {
		level := make([]int, d.n)
		for i := range level {
			level[i] = -1
		}
		level[source] = 0
		queue := []int{source}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, idx := range d.adj[v] {
				e := d.edges[idx]
				if e.cap > 0 && level[e.to] < 0 {
					level[e.to] = level[v] + 1
					queue = append(queue, e.to)
				}
			}
		}
		if level[sink] < 0 {
			return total
		}
		cursor := make([]int, d.n)
		var augment func(v, pushed int) int
		augment = func(v, pushed int) int {
			if v == sink {
				return pushed
			}
			for ; cursor[v] < len(d.adj[v]); cursor[v]++ {
				idx := d.adj[v][cursor[v]]
				e := d.edges[idx]
				if e.cap > 0 && level[e.to] == level[v]+1 {
					if got := augment(e.to, min(pushed, e.cap)); got > 0 {
						d.edges[idx].cap -= got
						d.edges[e.rev].cap += got
						return got
					}
				}
			}
			return 0
		}
		for {
			got := augment(source, int(^uint(0)>>1))
			if got == 0 {
				break
			}
			total += got
		}
	}
}

// feasibleTable 判定给定逐单元边界下是否存在满足行列和的整数表；存在时
// 同时返回一张完整可行表。下界先折算进边际和，再在 行-列 二部图上跑
// 最大流：源→行、行→列（容量为上界减下界）、列→汇。
func feasibleTable(lower, upper [][]int, rowSums, colSums []int) ([][]int, bool) {
	r, c := len(lower), len(lower[0])
	rowCap := append([]int(nil), rowSums...)
	colCap := append([]int(nil), colSums...)
	for i := 0; i < r; i++ {
		for j := 0; j < c; j++ {
			l := lower[i][j]
			rowCap[i] -= l
			colCap[j] -= l
			if rowCap[i] < 0 || colCap[j] < 0 {
				return nil, false
			}
		}
	}
	target := 0
	for _, v := range rowCap {
		target += v
	}

	source := 0
	rowNode := func(i int) int { return 1 + i }
	colNode := func(j int) int { return 1 + r + j }
	sink := 1 + r + c
	net := newDinic(sink + 1)
	for i := 0; i < r; i++ {
		net.addEdge(source, rowNode(i), rowCap[i])
	}
	cellEdge := make([][]int, r)
	for i := 0; i < r; i++ {
		cellEdge[i] = make([]int, c)
		for j := 0; j < c; j++ {
			cellEdge[i][j] = net.addEdge(rowNode(i), colNode(j), upper[i][j]-lower[i][j])
		}
	}
	for j := 0; j < c; j++ {
		net.addEdge(colNode(j), sink, colCap[j])
	}
	if net.maxFlow(source, sink) != target {
		return nil, false
	}
	table := make([][]int, r)
	for i := 0; i < r; i++ {
		table[i] = make([]int, c)
		for j := 0; j < c; j++ {
			table[i][j] = lower[i][j] + net.flow(cellEdge[i][j])
		}
	}
	return table, true
}
