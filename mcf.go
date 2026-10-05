package main

// mcf.go：次目标（最小总绝对改价）的求解。
//
// 固定"保持原价"的商品集合 kept 后，问题是一个差分约束 LP：
//   min  Σ_{i 自由} |p_i - orig_i|
//   s.t. p_v - p_u <= w        （价差约束）
//        p_i = orig_i          （kept 商品，含锁定商品）
// 该 LP 的对偶是最小费用流（约束矩阵全单模 ⇒ 整数最优解），
// 下面在对偶流网络上用连续最短路（SSP）求解，再从最优势还原整数价格。

type mcfEdge struct {
	to, rev   int
	cap, cost int64
}

type flowGraph struct {
	g [][]mcfEdge
}

func (fg *flowGraph) addEdge(from, to int, cap, cost int64) {
	fg.g[from] = append(fg.g[from], mcfEdge{to: to, rev: len(fg.g[to]), cap: cap, cost: cost})
	fg.g[to] = append(fg.g[to], mcfEdge{to: from, rev: len(fg.g[from]) - 1, cap: 0, cost: -cost})
}

// minChangeForKeptSet 在 kept 商品固定为原价的前提下，
// 返回满足全部价差约束且 Σ|p_i-orig_i| 最小的整数价格向量与最小总改价额。
//
// 对偶流网络（节点：商品 0..n-1，接地 G=n，超源 S=n+1，超汇 T=n+2）：
//   - 价差边 u->v，容量 INF，费用 w（对应 p_v - p_u <= w）；
//   - kept 商品 i：G->i 费用 orig_i、i->G 费用 -orig_i，容量 INF；
//   - 自由商品 i：S->i 容量 1 费用 orig_i、i->T 容量 1 费用 -orig_i；
//   - S->G、G->T 容量 INF 费用 0。
//
// 最小费用 S-T 流（费用取负）即 LP 最优值；最优节点势 pi 给出 p_i = pi_i - pi_G。
//
// 正确性要点：初始残余图无负环（kept 集合的相容性已保证），
// 每次沿 S 到 T 的最短路增广不会引入负环（SSP 标准结论），
// 因此 Bellman-Ford 的父指针链必然终止于 S。
func minChangeForKeptSet(n int, pairArcs []arc, orig []int64, kept []bool) ([]int64, int64) {
	G := n
	S := n + 1
	T := n + 2
	N := n + 3
	fg := &flowGraph{g: make([][]mcfEdge, N)}
	for _, a := range pairArcs {
		fg.addEdge(a.from, a.to, INF, a.w)
	}
	for i := 0; i < n; i++ {
		if kept[i] {
			fg.addEdge(G, i, INF, orig[i])
			fg.addEdge(i, G, INF, -orig[i])
		} else {
			fg.addEdge(S, i, 1, orig[i])
			fg.addEdge(i, T, 1, -orig[i])
		}
	}
	fg.addEdge(S, G, INF, 0)
	fg.addEdge(G, T, INF, 0)

	// SSP：只要 S 到 T 的最短路费用为负就增广。
	// 负最短路必经过某条容量 1 的 S->i 或 i->T 边，故增广次数 <= 2*自由商品数。
	var totalCost int64
	prevv := make([]int, N)
	preve := make([]int, N)
	dist := make([]int64, N)
	for iter := 0; ; iter++ {
		if iter > 4*N+10 {
			panic("min-cost flow: 增广次数异常（实现错误）")
		}
		for i := range dist {
			dist[i] = INF
		}
		dist[S] = 0
		for round := 0; round < N; round++ {
			updated := false
			for v := 0; v < N; v++ {
				if dist[v] >= INF/2 {
					continue
				}
				for ei, e := range fg.g[v] {
					if e.cap > 0 && dist[e.to] > dist[v]+e.cost {
						dist[e.to] = dist[v] + e.cost
						prevv[e.to] = v
						preve[e.to] = ei
						updated = true
					}
				}
			}
			if !updated {
				break
			}
		}
		if dist[T] >= INF/2 || dist[T] >= 0 {
			break
		}
		d := INF
		for v := T; v != S; v = prevv[v] {
			if c := fg.g[prevv[v]][preve[v]].cap; c < d {
				d = c
			}
		}
		for v := T; v != S; v = prevv[v] {
			e := &fg.g[prevv[v]][preve[v]]
			e.cap -= d
			fg.g[v][e.rev].cap += d
			totalCost += d * e.cost
		}
	}

	// 最优流的残余图无负环；用全 0 初始化的 Bellman-Ford 求最优势 pi，
	// 满足对任意残余边 u->v 有 pi_v <= pi_u + cost。
	pi := make([]int64, N)
	for round := 0; round < N; round++ {
		updated := false
		for v := 0; v < N; v++ {
			for _, e := range fg.g[v] {
				if e.cap > 0 && pi[e.to] > pi[v]+e.cost {
					pi[e.to] = pi[v] + e.cost
					updated = true
				}
			}
		}
		if !updated {
			break
		}
	}
	p := make([]int64, n)
	for i := 0; i < n; i++ {
		p[i] = pi[i] - pi[G]
	}
	return p, -totalCost
}
