package steady

import "math"

// BudgetUnlimited 表示该边即使不设预算上限也不会导致雪崩。
const BudgetUnlimited = math.MaxFloat64

// SafeBudget 是单条边的最大安全预算结果。
type SafeBudget struct {
	Edge Edge
	// MaxBudgetRatio 可恢复全链路稳态的最大 budgetRatio；
	// 等于 BudgetUnlimited 表示无需预算约束。
	MaxBudgetRatio float64
	// Needed 为 false 表示该边当前（或无预算）配置下本就安全。
	Needed bool
}

func cloneGraph(g *Graph) *Graph {
	cp := &Graph{Offered: map[string]float64{}}
	cp.Nodes = append([]Node(nil), g.Nodes...)
	cp.Edges = make([]Edge, len(g.Edges))
	copy(cp.Edges, g.Edges)
	// 深拷贝 IngressRetry 指针，避免多个克隆共享配置。
	for i := range cp.Nodes {
		if g.Nodes[i].IngressRetry != nil {
			ir := *g.Nodes[i].IngressRetry
			cp.Nodes[i].IngressRetry = &ir
		}
	}
	for k, v := range g.Offered {
		cp.Offered[k] = v
	}
	return cp
}

func graphConverged(g *Graph, opts SolverOptions) bool {
	r, err := Solve(g, opts)
	if err != nil {
		return false
	}
	return r.Converged
}

// applyBudget 在图上设置目标边的预算。入口节点 A 的入站重试与边 A->B 由同一
// 代理决策器管理，因此收紧边预算时同步收紧 A 的 IngressRetry，保证口径一致。
func applyBudget(g *Graph, target Edge, ratio float64, unlimited bool) {
	for i := range g.Edges {
		if sameEdge(g.Edges[i], target) {
			g.Edges[i].Retry.BudgetRatio = ratio
			g.Edges[i].Retry.UnlimitedBudget = unlimited
		}
	}
	for i := range g.Nodes {
		if g.Nodes[i].Name == target.From && g.Nodes[i].IngressRetry != nil {
			g.Nodes[i].IngressRetry.BudgetRatio = ratio
			g.Nodes[i].IngressRetry.UnlimitedBudget = unlimited
		}
	}
}

// MaxSafeBudgets 对每条边二分求出可使全链路回到收敛稳态的最大 budgetRatio。
//
// 这是逐边独立计算（其它边保持图中给定配置）；调用方可迭代多轮以获得联合
// 收紧后的保守取值。返回结果按 g.Edges 顺序排列。
func MaxSafeBudgets(g *Graph, opts SolverOptions) ([]SafeBudget, error) {
	if opts.MaxIterations <= 0 {
		opts = DefaultSolverOptions()
	}
	out := make([]SafeBudget, 0, len(g.Edges))
	for _, target := range g.Edges {
		// 先测试无预算是否安全。
		base := cloneGraph(g)
		applyBudget(base, target, 0, true)
		if graphConverged(base, opts) {
			out = append(out, SafeBudget{Edge: target, MaxBudgetRatio: BudgetUnlimited, Needed: false})
			continue
		}

		setRatio := func(r float64) *Graph {
			cg := cloneGraph(g)
			applyBudget(cg, target, r, false)
			return cg
		}

		// ratio=0（仅保底）时仍不可收敛，则任何预算都无法救回。
		lo, hi := 0.0, 10.0
		if !graphConverged(setRatio(lo), opts) {
			out = append(out, SafeBudget{Edge: target, MaxBudgetRatio: 0, Needed: true})
			continue
		}

		// 扩大上界直到不安全，保证解落在 [lo, hi) 内。
		for graphConverged(setRatio(hi), opts) {
			hi *= 2
			if hi > 1e6 {
				break
			}
		}
		for step := 0; step < 60; step++ {
			mid := (lo + hi) / 2
			if graphConverged(setRatio(mid), opts) {
				lo = mid
			} else {
				hi = mid
			}
		}
		out = append(out, SafeBudget{Edge: target, MaxBudgetRatio: lo, Needed: true})
	}
	return out, nil
}

func sameEdge(a, b Edge) bool {
	return a.From == b.From && a.To == b.To
}
