package mapmatch

import (
	"container/heap"
	"math"
)

// Node 是道路图中的一个节点。
type Node struct {
	ID int64
	LatLon
}

// Edge 是一条有向路段。双向道路应添加正反两条路段。
type Edge struct {
	ID     int64
	From   int64
	To     int64
	Points []LatLon // 路段折线，首末点应分别对应 From/To 节点
	Length float64  // 折线总长（米）
}

// project 把 p 投影到路段折线上，返回吸附点、沿路段里程（米）与偏离距离（米）。
func (e *Edge) project(p LatLon) (snapped LatLon, pos, dist float64) {
	dist = math.Inf(1)
	var acc float64
	for i := 0; i+1 < len(e.Points); i++ {
		ax, ay := toXY(e.Points[i], p)
		bx, by := toXY(e.Points[i+1], p)
		dx, dy := bx-ax, by-ay
		l2 := dx*dx + dy*dy
		var t float64
		if l2 > 0 {
			t = -(ax*dx + ay*dy) / l2
			t = math.Max(0, math.Min(1, t))
		}
		px, py := ax+t*dx, ay+t*dy
		d := math.Hypot(px, py)
		segLen := math.Sqrt(l2)
		if d < dist {
			dist = d
			pos = acc + t*segLen
			snapped = toLatLon(px, py, p)
		}
		acc += segLen
	}
	return snapped, pos, dist
}

// Graph 是有向道路图。
type Graph struct {
	nodes map[int64]*Node
	edges map[int64]*Edge
	adj   map[int64][]*Edge
	index *gridIndex
}

// NewGraph 创建空道路图。
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[int64]*Node),
		edges: make(map[int64]*Edge),
		adj:   make(map[int64][]*Edge),
	}
}

// AddNode 添加节点。
func (g *Graph) AddNode(id int64, lat, lon float64) {
	g.nodes[id] = &Node{ID: id, LatLon: LatLon{Lat: lat, Lon: lon}}
}

// AddEdge 添加有向路段，并根据折线计算长度。
func (g *Graph) AddEdge(id, from, to int64, pts []LatLon) {
	var length float64
	for i := 0; i+1 < len(pts); i++ {
		length += haversine(pts[i], pts[i+1])
	}
	e := &Edge{ID: id, From: from, To: to, Points: pts, Length: length}
	g.edges[id] = e
	g.adj[from] = append(g.adj[from], e)
}

// Edge 按 ID 取路段，不存在时返回 nil。
func (g *Graph) Edge(id int64) *Edge { return g.edges[id] }

// BuildIndex 构建路段空间索引。NewMatcher 会在索引缺失时自动调用。
func (g *Graph) BuildIndex() {
	idx := newGridIndex(0.001) // 约 111 米格网
	for _, e := range g.edges {
		idx.add(e)
	}
	g.index = idx
}

// shortestPaths 用 Dijkstra 计算 from 到所有节点的路网最短距离（米）。
func (g *Graph) shortestPaths(from int64) map[int64]float64 {
	dist := map[int64]float64{from: 0}
	h := &spHeap{{node: from}}
	heap.Init(h)
	for h.Len() > 0 {
		it := heap.Pop(h).(spItem)
		if d, ok := dist[it.node]; ok && it.dist > d {
			continue
		}
		for _, e := range g.adj[it.node] {
			nd := it.dist + e.Length
			if cur, ok := dist[e.To]; !ok || nd < cur {
				dist[e.To] = nd
				heap.Push(h, spItem{node: e.To, dist: nd})
			}
		}
	}
	return dist
}

type spItem struct {
	node int64
	dist float64
}

type spHeap []spItem

func (h spHeap) Len() int            { return len(h) }
func (h spHeap) Less(i, j int) bool  { return h[i].dist < h[j].dist }
func (h spHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *spHeap) Push(x interface{}) { *h = append(*h, x.(spItem)) }
func (h *spHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
