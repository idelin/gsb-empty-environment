// Package paretopath 在顶点数不超过 10 的有向图上求解
// 时间-费用双目标的全部 Pareto 最优简单路径（非支配标签）。
package paretopath

import "fmt"

// MaxVertices 是图允许的最大顶点数。
const MaxVertices = 10

// Edge 是一条有向边，时间与费用均非负。
type Edge struct {
	To   int
	Time int64
	Cost int64
}

// Graph 是邻接表表示的有向图。
type Graph struct {
	N   int
	Adj [][]Edge
}

// NewGraph 创建 n 个顶点的空图，顶点编号为 0..n-1。
// n 必须在 [1, MaxVertices] 内，否则 panic。
func NewGraph(n int) *Graph {
	if n < 1 || n > MaxVertices {
		panic(fmt.Sprintf("paretopath: 顶点数 %d 超出范围 [1, %d]", n, MaxVertices))
	}
	return &Graph{N: n, Adj: make([][]Edge, n)}
}

// AddEdge 添加有向边 from -> to。time 与 cost 必须非负，否则 panic。
func (g *Graph) AddEdge(from, to int, time, cost int64) {
	g.checkVertex(from)
	g.checkVertex(to)
	if time < 0 || cost < 0 {
		panic(fmt.Sprintf("paretopath: 边 %d->%d 的指标必须非负 (time=%d, cost=%d)", from, to, time, cost))
	}
	g.Adj[from] = append(g.Adj[from], Edge{To: to, Time: time, Cost: cost})
}

func (g *Graph) checkVertex(v int) {
	if v < 0 || v >= g.N {
		panic(fmt.Sprintf("paretopath: 顶点 %d 超出范围 [0, %d)", v, g.N))
	}
}

// Label 是一个路径标签：累计时间、累计费用以及顶点序列。
// Path[0] 为源点，Path[len(Path)-1] 为当前所在顶点。
type Label struct {
	Time int64
	Cost int64
	Path []int
}
