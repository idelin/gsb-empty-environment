package steady

import (
	"errors"
	"math"
)

// EdgeRetry 是一条边上的重试/预算/对冲配置（速率语义，与 retry.Config 对应）。
type EdgeRetry struct {
	// MaxAttempts 最大尝试次数（含首次）。
	MaxAttempts int
	// BudgetRatio 额外尝试速率占常规流量速率的预算比例。
	BudgetRatio float64
	// MinRetryRate 保底：低流量下允许的最小额外尝试速率
	// （对应运行时 MinRetryConcurrency / 窗口时长，按 Little 定律换算）。
	MinRetryRate float64
	// HedgeBranches 对冲分支数（0 表示不启用对冲）。
	HedgeBranches int
	// UnlimitedBudget 为 true 时忽略预算（仅受 MaxAttempts 约束），
	// 用于复现“无预算雪崩”基线。
	UnlimitedBudget bool
}

// Edge 是 DAG 中的一条有向边。
type Edge struct {
	From string
	To   string
	// Fanout 扇出倍率：每个到达 From 的逻辑请求在该边上产生的调用数。
	Fanout float64
	Retry  EdgeRetry
}

// Node 是调用图中的一个服务节点。
type Node struct {
	// Name 节点唯一名。
	Name string
	// Capacity 硬容量：稳态可承载的物理请求速率上限。
	Capacity float64
	// Curve 负载-故障率响应曲线。
	Curve FailCurve
	// IngressRetry 外部 offered 流量进入本节点时采用的重试配置；
	// nil 表示入口流量不重试。
	IngressRetry *EdgeRetry
}

// Graph 是多跳有向无环调用图。
type Graph struct {
	Nodes []Node
	Edges []Edge
	// Offered 入口节点（无入边）的外部逻辑请求到达率。
	Offered map[string]float64
}

// ErrCyclic 表示输入图含环，不动点求解仅支持 DAG。
var ErrCyclic = errors.New("steady: graph contains a cycle")

func (g *Graph) index() (map[string]int, map[string][]Edge, []string, error) {
	idx := make(map[string]int, len(g.Nodes))
	indeg := make(map[string]int, len(g.Nodes))
	out := make(map[string][]Edge, len(g.Nodes))
	for i, n := range g.Nodes {
		idx[n.Name] = i
		indeg[n.Name] = 0
	}
	for _, ed := range g.Edges {
		if _, ok := idx[ed.From]; !ok {
			return nil, nil, nil, errors.New("steady: unknown edge source " + ed.From)
		}
		if _, ok := idx[ed.To]; !ok {
			return nil, nil, nil, errors.New("steady: unknown edge target " + ed.To)
		}
		out[ed.From] = append(out[ed.From], ed)
		indeg[ed.To]++
	}
	// Kahn 拓扑排序。
	queue := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if indeg[n.Name] == 0 {
			queue = append(queue, n.Name)
		}
	}
	order := make([]string, 0, len(g.Nodes))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		order = append(order, cur)
		for _, ed := range out[cur] {
			indeg[ed.To]--
			if indeg[ed.To] == 0 {
				queue = append(queue, ed.To)
			}
		}
	}
	if len(order) != len(g.Nodes) {
		return nil, nil, nil, ErrCyclic
	}
	return idx, out, order, nil
}

func geomSum(p float64, maxAttempts int) float64 {
	if maxAttempts <= 1 {
		return 1
	}
	if p <= 0 {
		return 1
	}
	if p >= 1 {
		return float64(maxAttempts)
	}
	return (1 - math.Pow(p, float64(maxAttempts))) / (1 - p)
}

// extraDemand 计算单位逻辑请求在给定故障率 p 下“想要”发起的额外尝试数
// （重试 + 对冲，均为首次之外的物理请求），并施加预算上限。
// logicalRate 为该边上的常规（逻辑）请求速率，用于把保底 MinRetryRate 换算为比例。
func extraDemand(r EdgeRetry, p float64, logicalRate float64) float64 {
	// 对冲与普通重试共享 MaxAttempts 尝试预算：对冲把部分“失败后重试”提前为
	// 并行竞速，因此额外尝试的期望总数仍由几何级数 geom(p, MaxAttempts)-1 给出，
	// 对冲只改变这些额外尝试的发起时机（仿真中体现为并行分支与竞速取消）。
	want := geomSum(p, r.MaxAttempts) - 1
	if r.HedgeBranches > 0 {
		// 对冲在低故障率下即可触发：至少保留 p*HedgeBranches 的额外期望，
		// 但不突破总尝试预算对应的几何级数上界。
		hedgeFloor := math.Min(float64(r.HedgeBranches), float64(r.MaxAttempts-1)) * p
		if hedgeFloor > want {
			want = hedgeFloor
		}
	}
	if r.UnlimitedBudget {
		return want
	}
	// 无任何重试配置（MaxAttempts<=1 且无对冲）：不产生额外尝试。
	if r.MaxAttempts <= 1 && r.HedgeBranches == 0 {
		return 0
	}
	// 预算以“额外速率 / 常规速率”的比例表达；保底速率 MinRetryRate 按当前
	// 常规速率折算为比例，与运行时 max(MinRetryConcurrency, floor(ratio*active)) 对应。
	floorRatio := 0.0
	if logicalRate > 0 {
		floorRatio = r.MinRetryRate / logicalRate
	}
	cap := math.Max(floorRatio, r.BudgetRatio)
	return math.Min(want, cap)
}
