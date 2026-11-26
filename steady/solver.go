package steady

import "math"

// SolverOptions 控制不动点迭代。
type SolverOptions struct {
	// MaxIterations 单节点自洽迭代上限。
	MaxIterations int
	// Tolerance 物理速率相对收敛容差。
	Tolerance float64
	// DivergeLoad 判定不可收敛的放大阈值：物理速率超过 DivergeLoad*容量即雪崩。
	DivergeLoad float64
}

// DefaultSolverOptions 给出保守的默认迭代参数。
func DefaultSolverOptions() SolverOptions {
	return SolverOptions{MaxIterations: 200, Tolerance: 1e-9, DivergeLoad: 100}
}

// NodeResult 是单个节点的稳态求解结果。
type NodeResult struct {
	Name string
	// LogicalRate 上游扇入产生的逻辑请求速率（不含本节点重试）。
	LogicalRate float64
	// PhysicalRate 计入重试与对冲后的实际物理到达率。
	PhysicalRate float64
	// FailProb 稳态实际故障率 p_fail(physical/capacity)。
	FailProb float64
	// LoadRatio 物理到达率 / 硬容量。
	LoadRatio float64
	// Amplification 本节点放大倍率 = physical / logical。
	Amplification float64
	// RetryExtra 稳态额外尝试（重试+对冲）速率。
	RetryExtra float64
	// Avalanche 为 true 表示击穿硬容量、迭代不可收敛。
	Avalanche bool
	// Iterations 自洽迭代实际使用的步数。
	Iterations int
}

// Result 是全图稳态结果。
type Result struct {
	Nodes map[string]*NodeResult
	// Order 与拓扑序一致的节点名。
	Order []string
	// Converged 所有节点均在容量内收敛。
	Converged bool
	// TotalAmplification 端到端总放大 = 最深节点物理速率 / 外部 offered 之和。
	TotalAmplification float64
	// OfferedTotal 外部 offered 速率之和。
	OfferedTotal float64
}

// Solve 对 DAG 做稳态不动点求解。
//
// 对每个节点，给定逻辑到达率 L 后自洽求解：
//
//	load_0 = L
//	p_k     = curve(load_k / capacity)
//	extra_k = sum_edges fanout * L_in * extraDemand(edge, p_k)
//	load_k+1= L + extra_k
//
// 由于 p_fail 随负载单调不减，序列单调不减；若在容量附近不存在不动点则发散，
// 节点进入不可收敛雪崩态。
func Solve(g *Graph, opts SolverOptions) (*Result, error) {
	if opts.MaxIterations <= 0 {
		opts = DefaultSolverOptions()
	}
	if opts.Tolerance <= 0 {
		opts.Tolerance = 1e-9
	}
	if opts.DivergeLoad <= 0 {
		opts.DivergeLoad = 1e6
	}

	_, _, order, err := g.index()
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		n := g.Nodes[i]
		nodes[n.Name] = &n
	}

	res := &Result{Nodes: map[string]*NodeResult{}, Converged: true}
	logical := map[string]float64{}
	outgoing := map[string]float64{} // 上游节点发出的逻辑请求速率（每个到达请求发出一次）

	var offeredTotal float64
	for _, v := range g.Offered {
		offeredTotal += math.Max(0, v)
	}
	res.OfferedTotal = offeredTotal

	for _, name := range order {
		// 1) 汇聚逻辑到达率：外部 offered + 上游各边 fanout*上游逻辑速率。
		L := 0.0
		type inEdge struct {
			ed         Edge
			rateWeight float64 // 该边贡献的逻辑速率（fanout * 上游 logical）
			ingress    bool
		}
		var ins []inEdge
		if offer := math.Max(0, g.Offered[name]); offer > 0 {
			L += offer
			virtual := EdgeRetry{}
			if nodes[name].IngressRetry != nil {
				virtual = *nodes[name].IngressRetry
			}
			ins = append(ins, inEdge{
				ed:         Edge{From: "", To: name, Fanout: 1, Retry: virtual},
				rateWeight: offer,
				ingress:    true,
			})
		}
		for _, ed := range g.Edges {
			if ed.To != name {
				continue
			}
			// 每个到达上游的逻辑请求都沿该边发出一次下游调用（fanout 倍率），
			// 与上游最终成败无关；边上的重试放大由下游节点故障率驱动（见下）。
			w := ed.Fanout * outgoing[ed.From]
			if w < 0 {
				w = 0
			}
			L += w
			ins = append(ins, inEdge{ed: ed, rateWeight: w})
		}
		logical[name] = L

		// 2) 自洽不动点：load = L + sum_w extraDemand(edge, p(load/cap)) * w。
		node := nodes[name]
		cap := node.Capacity
		curve := node.Curve
		load := L
		avalanche := false
		it := 0
		var p float64
		for ; it < opts.MaxIterations; it++ {
			ratio := 0.0
			if cap > 0 {
				ratio = load / cap
			} else if load > 0 {
				ratio = math.Inf(1)
			}
			p = curve.PFail(ratio)
			extra := 0.0
			for _, ie := range ins {
				extra += ie.rateWeight * extraDemand(ie.ed.Retry, p, ie.rateWeight)
			}
			next := L + extra
			if math.Abs(next-load) <= opts.Tolerance*math.Max(1, next) {
				load = next
				it++
				break
			}
			if cap > 0 && next > opts.DivergeLoad*cap {
				avalanche = true
				load = next
				it++
				break
			}
			load = next
		}
		if it >= opts.MaxIterations && !avalanche {
			// 单调序列未在步数内收敛，视为逼近容量悬崖。
			avalanche = true
		}

		ratio := 0.0
		if cap > 0 {
			ratio = load / cap
		}
		extraRate := load - L
		amp := 0.0
		if L > 0 {
			amp = load / L
		}
		if ratio > 1.0 {
			avalanche = true
		}

		nr := &NodeResult{
			Name:          name,
			LogicalRate:   L,
			PhysicalRate:  load,
			FailProb:      p,
			LoadRatio:     ratio,
			Amplification: amp,
			RetryExtra:    math.Max(0, extraRate),
			Avalanche:     avalanche,
			Iterations:    it,
		}
		res.Nodes[name] = nr
		res.Order = append(res.Order, name)
		// 每个逻辑到达都会沿出边发起下游调用；逻辑速率即上游出边的权重基数。
		outgoing[name] = L
		if avalanche {
			res.Converged = false
		}
	}

	if offeredTotal > 0 {
		// 端到端总放大 = 最深节点物理速率 / 外部 offered 总和。
		last := res.Nodes[res.Order[len(res.Order)-1]]
		res.TotalAmplification = last.PhysicalRate / offeredTotal
	}
	return res, nil
}
