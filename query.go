package netsec

import (
	"math"
	"sort"
)

// Subgraph 是一次回溯查询的结果：包含目标连接边、全部可达源点，
// 以及所有位于“源点到目标的时间不倒退因果路径”上的节点与边。
type Subgraph struct {
	Target  Edge   // 查询目标（通常为连接边）
	Nodes   []Node // 按 ID 排序
	Edges   []Edge // 按 (时间, 顺序号) 排序
	Sources []Node // 可达源点（子图内无入边的起点），按 ID 排序
}

// Backtrace 从目标边出发回溯：先求时间（含同刻顺序号）不倒退的
// 反向可达集，再取所有可达源点到目标的正向可达部分，得到既包含
// 全部可达源点又不含无关分支的最小子图。
func (g *Graph) Backtrace(target int) Subgraph {
	te := g.edges[target]

	// 1) 反向可达性：limit[n] 是离开 n 后仍能到达目标所允许的最大时间戳。
	//    边 u->v 可反向穿越当且仅当 edge.Stamp <= limit[v]。
	limit := map[string]Stamp{te.From.ID(): te.Stamp}
	inBack := map[int]bool{}
	queue := []Node{te.From}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		l := limit[n.ID()]
		for _, ei := range g.inEdges[n.ID()] {
			e := g.edges[ei]
			if e.Stamp.Compare(l) > 0 {
				continue // 时间倒退，因果上不成立
			}
			inBack[ei] = true
			id := e.From.ID()
			if cur, ok := limit[id]; !ok || e.Stamp.Compare(cur) > 0 {
				limit[id] = e.Stamp
				queue = append(queue, e.From)
			}
		}
	}

	// 2) 源点：反向集合内没有被纳入的入边的节点，即因果链的起点。
	hasIncoming := make(map[string]bool)
	for ei := range inBack {
		hasIncoming[g.edges[ei].To.ID()] = true
	}
	var sources []Node
	for id := range limit {
		if !hasIncoming[id] {
			sources = append(sources, g.nodes[id])
		}
	}

	// 3) 正向过滤：从全部源点出发，只走反向集合内的边且时间不倒退，
	//    得到真正落在“源点->目标”因果路径上的边，剔除死端分支。
	fwd := make(map[string]Stamp) // fwd[n] = 到达 n 的路径上最大边时间戳的最小值
	queue = queue[:0]
	for _, s := range sources {
		fwd[s.ID()] = Stamp{Time: math.MinInt64}
		queue = append(queue, s)
	}
	inFwd := map[int]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		arrival := fwd[n.ID()]
		for _, ei := range g.outEdges[n.ID()] {
			if !inBack[ei] {
				continue
			}
			e := g.edges[ei]
			if e.Stamp.Compare(arrival) < 0 {
				continue
			}
			inFwd[ei] = true
			id := e.To.ID()
			if cur, ok := fwd[id]; !ok || e.Stamp.Compare(cur) < 0 {
				fwd[id] = e.Stamp
				queue = append(queue, e.To)
			}
		}
	}

	// 4) 汇总最小子图：反向可达且正向可达的边，加上目标边本身。
	keep := map[int]bool{target: true}
	for ei := range inFwd {
		keep[ei] = true
	}
	nodeSet := make(map[string]Node)
	edges := make([]Edge, 0, len(keep))
	for ei := range keep {
		e := g.edges[ei]
		edges = append(edges, e)
		nodeSet[e.From.ID()] = e.From
		nodeSet[e.To.ID()] = e.To
	}
	for _, s := range sources {
		nodeSet[s.ID()] = s
	}

	sort.Slice(edges, func(a, b int) bool {
		if c := edges[a].Stamp.Compare(edges[b].Stamp); c != 0 {
			return c < 0
		}
		if edges[a].Kind != edges[b].Kind {
			return edges[a].Kind < edges[b].Kind
		}
		return edges[a].From.ID() < edges[b].From.ID()
	})
	nodes := make([]Node, 0, len(nodeSet))
	for _, n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(a, b int) bool { return nodes[a].ID() < nodes[b].ID() })
	sort.Slice(sources, func(a, b int) bool { return sources[a].ID() < sources[b].ID() })

	return Subgraph{Target: te, Nodes: nodes, Edges: edges, Sources: sources}
}
