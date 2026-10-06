package main

// Graph 是有向多重图：同一条边 (u,v) 可重复添加，按计数存续。
type Graph struct {
	adj   map[int]map[int]int // u -> v -> 重复计数
	nodes map[int]bool
}

func NewGraph() *Graph {
	return &Graph{adj: make(map[int]map[int]int), nodes: make(map[int]bool)}
}

// AddEdge 将边 (u,v) 计数加一；返回该边是否由 0 变为 1（结构发生变化）。
func (g *Graph) AddEdge(u, v int) bool {
	g.nodes[u] = true
	g.nodes[v] = true
	if g.adj[u] == nil {
		g.adj[u] = make(map[int]int)
	}
	g.adj[u][v]++
	return g.adj[u][v] == 1
}

// RemoveEdge 将边 (u,v) 计数减一（只减一次）；计数归零时删除该边。
// 返回该边是否由 1 变为 0（结构发生变化）。边不存在时为 no-op。
func (g *Graph) RemoveEdge(u, v int) bool {
	if g.adj[u] == nil || g.adj[u][v] == 0 {
		return false
	}
	g.adj[u][v]--
	if g.adj[u][v] == 0 {
		delete(g.adj[u], v)
		return true
	}
	return false
}

// Count 返回边 (u,v) 的当前重复计数。
func (g *Graph) Count(u, v int) int {
	if g.adj[u] == nil {
		return 0
	}
	return g.adj[u][v]
}

// Nodes 返回所有出现过的节点。
func (g *Graph) Nodes() []int {
	out := make([]int, 0, len(g.nodes))
	for n := range g.nodes {
		out = append(out, n)
	}
	return out
}

// BFSReach 计算从 s 出发、长度 >= 1 的路径可达节点集。
// s 只有在存在自环或经过环回到自身时才包含自身。
func (g *Graph) BFSReach(s int) map[int]bool {
	reach := make(map[int]bool)
	var queue []int
	for v := range g.adj[s] {
		if !reach[v] {
			reach[v] = true
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for y := range g.adj[x] {
			if !reach[y] {
				reach[y] = true
				queue = append(queue, y)
			}
		}
	}
	return reach
}
