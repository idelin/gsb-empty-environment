package planner

import "math"

// 最小费用最大流（SSP）：每次沿单位费用最小的 s-t 路增广，
// 直到不存在增广路。得到的是“流量最大、且在最大流中费用最小”的流。
// 允许负费用边；初始无负环，SSP 保持最优性。节点 <= 19，Bellman-Ford 足够。

type edge struct {
	to, rev int
	cap     int64
	cost    int64
	origCap int64 // 记录初始容量，便于事后取流量
}

type mcfGraph struct {
	g [][]edge
}

func newMCF(n int) *mcfGraph { return &mcfGraph{g: make([][]edge, n)} }

func (m *mcfGraph) addEdge(from, to int, cap, cost int64) {
	fwd := edge{to: to, rev: len(m.g[to]), cap: cap, cost: cost, origCap: cap}
	bwd := edge{to: from, rev: len(m.g[from]), cap: 0, cost: -cost}
	m.g[from] = append(m.g[from], fwd)
	m.g[to] = append(m.g[to], bwd)
}

// flowOn 返回节点 from 第 idx 条出边上的流量。
func (m *mcfGraph) flowOn(from, idx int) int64 {
	e := m.g[from][idx]
	return e.origCap - e.cap
}

// minCostMaxFlow 返回最大流量与最小总费用。
func (m *mcfGraph) minCostMaxFlow(s, t int) (flow, cost int64) {
	n := len(m.g)
	const inf = math.MaxInt64 / 4
	for {
		dist := make([]int64, n)
		inq := make([]bool, n)
		prevV := make([]int, n)
		prevE := make([]int, n)
		for i := range dist {
			dist[i] = inf
		}
		dist[s] = 0
		// SPFA 求最小费用路
		queue := []int{s}
		inq[s] = true
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			inq[v] = false
			for i, e := range m.g[v] {
				if e.cap > 0 && dist[v]+e.cost < dist[e.to] {
					dist[e.to] = dist[v] + e.cost
					prevV[e.to] = v
					prevE[e.to] = i
					if !inq[e.to] {
						queue = append(queue, e.to)
						inq[e.to] = true
					}
				}
			}
		}
		if dist[t] == inf { // 无增广路：已达最大流
			break
		}
		add := int64(inf)
		for v := t; v != s; v = prevV[v] {
			if c := m.g[prevV[v]][prevE[v]].cap; c < add {
				add = c
			}
		}
		for v := t; v != s; v = prevV[v] {
			e := &m.g[prevV[v]][prevE[v]]
			e.cap -= add
			m.g[v][e.rev].cap += add
		}
		flow += add
		cost += add * dist[t]
	}
	return flow, cost
}
