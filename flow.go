package main

// 最小费用最大流，用于在一个时段内把"兑换组合所需的积分"
// 分配到各个批次上。费用取批次的到期时段，从而在所有可行
// 支付方案中优先消耗最早到期的积分（FEFO），最小化到期损失。

type edge struct {
	to, rev, cap, cost int
}

type graph struct {
	adj [][]edge
}

func newGraph(n int) *graph {
	return &graph{adj: make([][]edge, n)}
}

func (g *graph) addEdge(u, v, cap, cost int) {
	g.adj[u] = append(g.adj[u], edge{v, len(g.adj[v]), cap, cost})
	g.adj[v] = append(g.adj[v], edge{u, len(g.adj[u]) - 1, 0, -cost})
}

// minCostFlow 从 s 向 t 推送至多 maxf 单位流量（SPFA 增广），
// 返回实际推送的流量；费用最小的增广路优先。
func (g *graph) minCostFlow(s, t, maxf int) int {
	n := len(g.adj)
	flow := 0
	for flow < maxf {
		dist := make([]int, n)
		inq := make([]bool, n)
		prevV := make([]int, n)
		prevE := make([]int, n)
		for i := range dist {
			dist[i] = -1
		}
		dist[s] = 0
		q := []int{s}
		inq[s] = true
		for len(q) > 0 {
			u := q[0]
			q = q[1:]
			inq[u] = false
			for i, e := range g.adj[u] {
				if e.cap > 0 && (dist[e.to] < 0 || dist[e.to] > dist[u]+e.cost) {
					dist[e.to] = dist[u] + e.cost
					prevV[e.to] = u
					prevE[e.to] = i
					if !inq[e.to] {
						q = append(q, e.to)
						inq[e.to] = true
					}
				}
			}
		}
		if dist[t] < 0 {
			break
		}
		d := maxf - flow
		for v := t; v != s; v = prevV[v] {
			if c := g.adj[prevV[v]][prevE[v]].cap; c < d {
				d = c
			}
		}
		for v := t; v != s; v = prevV[v] {
			e := &g.adj[prevV[v]][prevE[v]]
			e.cap -= d
			g.adj[v][e.rev].cap += d
		}
		flow += d
	}
	return flow
}

// pay 计算一个时段内某个兑换组合的批次扣减方案。
// 返回 ded[offerIdx][batchIdx] = 扣减积分；若积分不足或来源受限
// 导致无法支付，返回 ok=false。
func pay(batches []stateBatch, offers []Offer, units []int) (ded [][]int, ok bool) {
	nB, nO := len(batches), len(offers)
	src, sink := 0, 1+nB+nO
	g := newGraph(sink + 1)
	for i, b := range batches {
		g.addEdge(src, 1+i, b.remaining, b.expiry) // 费用 = 到期时段，先用快过期的
	}
	type edgeRef struct{ batch, offer, node, idx int }
	var refs []edgeRef
	required := 0
	for j, o := range offers {
		need := units[j] * o.Cost
		required += need
		g.addEdge(1+nB+j, sink, need, 0)
		for i, b := range batches {
			if sourceAllowed(o, b.source) {
				refs = append(refs, edgeRef{i, j, 1 + i, len(g.adj[1+i])})
				g.addEdge(1+i, 1+nB+j, b.remaining, 0)
			}
		}
	}
	if g.minCostFlow(src, sink, required) < required {
		return nil, false
	}
	ded = make([][]int, nO)
	for j := range offers {
		ded[j] = make([]int, nB)
	}
	for _, r := range refs {
		e := g.adj[r.node][r.idx]
		used := batches[r.batch].remaining - e.cap
		if used > 0 {
			ded[r.offer][r.batch] = used
		}
	}
	return ded, true
}

func sourceAllowed(o Offer, source string) bool {
	if len(o.AllowedSources) == 0 {
		return true
	}
	for _, s := range o.AllowedSources {
		if s == source {
			return true
		}
	}
	return false
}
