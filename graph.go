// Package pathcount 提供小规模图（最多 22 个顶点）上 s 到 t 的
// 简单路径（不重复顶点）计数与分页枚举能力，路径必须覆盖指定的必经点集合。
package pathcount

import "fmt"

// MaxVertices 是支持的顶点数上限。状态用 32 位子集掩码表示，
// 上限 22 是为了约束子集状态记忆的空间开销。
const MaxVertices = 22

// Graph 是有向图，邻接关系用位掩码存储。
type Graph struct {
	n   int
	adj []uint32
}

// NewGraph 构造 n 个顶点的空图，顶点编号为 0..n-1。
// n 超出 [1, MaxVertices] 时 panic。
func NewGraph(n int) *Graph {
	if n < 1 || n > MaxVertices {
		panic(fmt.Sprintf("pathcount: vertex count %d out of range [1, %d]", n, MaxVertices))
	}
	return &Graph{n: n, adj: make([]uint32, n)}
}

// NumVertices 返回顶点数。
func (g *Graph) NumVertices() int { return g.n }

func (g *Graph) checkVertex(v int) {
	if v < 0 || v >= g.n {
		panic(fmt.Sprintf("pathcount: vertex %d out of range [0, %d)", v, g.n))
	}
}

// AddEdge 添加有向边 u -> v，重复添加幂等。
func (g *Graph) AddEdge(u, v int) {
	g.checkVertex(u)
	g.checkVertex(v)
	g.adj[u] |= 1 << uint(v)
}

// AddUndirectedEdge 添加双向边。
func (g *Graph) AddUndirectedEdge(u, v int) {
	g.AddEdge(u, v)
	g.AddEdge(v, u)
}

// HasEdge 报告边 u -> v 是否存在。
func (g *Graph) HasEdge(u, v int) bool {
	g.checkVertex(u)
	g.checkVertex(v)
	return g.adj[u]&(1<<uint(v)) != 0
}

// RemoveEdge 删除有向边 u -> v。
func (g *Graph) RemoveEdge(u, v int) {
	g.checkVertex(u)
	g.checkVertex(v)
	g.adj[u] &^= 1 << uint(v)
}

// Neighbors 按顶点编号升序返回 v 的出边邻居。
func (g *Graph) Neighbors(v int) []int {
	g.checkVertex(v)
	var out []int
	for w := 0; w < g.n; w++ {
		if g.adj[v]&(1<<uint(w)) != 0 {
			out = append(out, w)
		}
	}
	return out
}

// clone 返回图的深拷贝。
func (g *Graph) clone() *Graph {
	adj := make([]uint32, g.n)
	copy(adj, g.adj)
	return &Graph{n: g.n, adj: adj}
}
