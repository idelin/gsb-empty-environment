package main

import (
	"container/heap"
	"fmt"
	"sort"
)

// Edge 是一条有向边：费用非负，风险为非负整数。
type Edge struct {
	ID   int
	From int
	To   int
	Cost int64
	Risk int
}

// Graph 是有向图，允许平行边与零费用/零风险环。
type Graph struct {
	Adj   [][]Edge     // 每个点的出边，按边 ID 升序
	ByID  map[int]Edge // 边 ID -> 边
	Names []string     // 点下标 -> 名字
}

// RawEdge 是建图输入，点用名字表示。
type RawEdge struct {
	ID       int
	From, To string
	Cost     int64
	Risk     int
}

// NewGraph 校验输入并构建图。
func NewGraph(raw []RawEdge) (*Graph, map[string]int, error) {
	g := &Graph{ByID: make(map[int]Edge)}
	index := make(map[string]int)
	node := func(name string) int {
		if i, ok := index[name]; ok {
			return i
		}
		i := len(g.Names)
		index[name] = i
		g.Names = append(g.Names, name)
		g.Adj = append(g.Adj, nil)
		return i
	}
	for _, re := range raw {
		if re.Cost < 0 {
			return nil, nil, fmt.Errorf("边 %d 费用为负: %d", re.ID, re.Cost)
		}
		if re.Risk < 0 {
			return nil, nil, fmt.Errorf("边 %d 风险为负: %d", re.ID, re.Risk)
		}
		if _, dup := g.ByID[re.ID]; dup {
			return nil, nil, fmt.Errorf("边 ID 重复: %d", re.ID)
		}
		e := Edge{ID: re.ID, From: node(re.From), To: node(re.To), Cost: re.Cost, Risk: re.Risk}
		g.ByID[e.ID] = e
		g.Adj[e.From] = append(g.Adj[e.From], e)
	}
	for _, outs := range g.Adj {
		sort.Slice(outs, func(i, j int) bool { return outs[i].ID < outs[j].ID })
	}
	return g, index, nil
}

// Label 是某点的一个非支配标签（一条从源点到该点的部分路径）。
type Label struct {
	Cost int64
	Risk int
	Hops int
	Node int
	Path []int // 到达该标签的边 ID 序列
}

// comparePaths 按边 ID 序列做字典序比较；互为前缀时短者小。
func comparePaths(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// betterSolution 是最终答案的比较序：费用低者优先，同价边少者优先，再按边 ID 字典序。
func betterSolution(a, b *Label) bool {
	if a.Cost != b.Cost {
		return a.Cost < b.Cost
	}
	if a.Hops != b.Hops {
		return a.Hops < b.Hops
	}
	return comparePaths(a.Path, b.Path) < 0
}

// dominates 判断 a 是否支配 b：费用、风险、边数都不更差。
// 注意最终比较序是 (费用, 边数, 路径字典序)，风险只是约束：
// 当费用与边数都相同（无论风险是否相同）时，必须用路径字典序裁决，
// 因为同一后缀拼在等长前缀后，字典序由前缀决定。
func dominates(a, b *Label) bool {
	if a.Cost > b.Cost || a.Risk > b.Risk || a.Hops > b.Hops {
		return false
	}
	if a.Cost == b.Cost && a.Hops == b.Hops {
		return comparePaths(a.Path, b.Path) <= 0
	}
	return true
}

// labelHeap 按 (费用, 边数, 风险, 路径字典序) 弹出，保证弹出的非支配标签是永久的。
type labelHeap []*Label

func (h labelHeap) Len() int { return len(h) }
func (h labelHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.Cost != b.Cost {
		return a.Cost < b.Cost
	}
	if a.Hops != b.Hops {
		return a.Hops < b.Hops
	}
	if a.Risk != b.Risk {
		return a.Risk < b.Risk
	}
	return comparePaths(a.Path, b.Path) < 0
}
func (h labelHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *labelHeap) Push(x interface{}) { *h = append(*h, x.(*Label)) }
func (h *labelHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Solution 是求解结果。
type Solution struct {
	Found bool
	Cost  int64
	Risk  int
	Path  []int // 边 ID 序列
}

// solve 是标签设定法（多标准 Dijkstra）：
// 每个点保留一组互不支配的标签，而不是只留一个距离。
// 返回最优解和每个点的全部非支配标签（供测试核对）。
func solve(g *Graph, src, dst, budget int) (Solution, [][]*Label) {
	labels := make([][]*Label, len(g.Adj))
	pq := &labelHeap{}
	heap.Push(pq, &Label{Node: src})
	for pq.Len() > 0 {
		cur := heap.Pop(pq).(*Label)
		dominated := false
		for _, l := range labels[cur.Node] {
			if dominates(l, cur) {
				dominated = true
				break
			}
		}
		if dominated {
			continue
		}
		kept := make([]*Label, 0, len(labels[cur.Node])+1)
		for _, l := range labels[cur.Node] {
			if !dominates(cur, l) {
				kept = append(kept, l)
			}
		}
		labels[cur.Node] = append(kept, cur)
		if cur.Node == dst {
			continue // 费用与风险非负，离开终点再回来只会被支配
		}
		for _, e := range g.Adj[cur.Node] {
			nr := cur.Risk + e.Risk
			if nr > budget {
				continue
			}
			path := make([]int, len(cur.Path)+1)
			copy(path, cur.Path)
			path[len(cur.Path)] = e.ID
			heap.Push(pq, &Label{
				Cost: cur.Cost + e.Cost,
				Risk: nr,
				Hops: cur.Hops + 1,
				Node: e.To,
				Path: path,
			})
		}
	}
	best := Solution{}
	var bestLabel *Label
	for _, l := range labels[dst] {
		if bestLabel == nil || betterSolution(l, bestLabel) {
			bestLabel = l
			best = Solution{Found: true, Cost: l.Cost, Risk: l.Risk, Path: l.Path}
		}
	}
	return best, labels
}
