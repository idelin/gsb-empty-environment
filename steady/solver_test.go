package steady

import (
	"math"
	"testing"
)

func chainGraph(budget float64, unlimited bool) *Graph {
	curve := LinearCurve{Soft: 0.7, Base: 0.05, PAtHard: 0.6}
	retryCfg := EdgeRetry{
		MaxAttempts:     4,
		BudgetRatio:     budget,
		MinRetryRate:    0,
		UnlimitedBudget: unlimited,
	}
	nodes := []Node{
		{Name: "A", Capacity: 1000, Curve: curve, IngressRetry: &retryCfg},
		{Name: "B", Capacity: 1000, Curve: curve},
		{Name: "C", Capacity: 1000, Curve: curve},
		{Name: "D", Capacity: 1000, Curve: curve},
	}
	edge := func(from, to string) Edge {
		return Edge{
			From: from, To: to, Fanout: 1,
			Retry: retryCfg,
		}
	}
	return &Graph{
		Nodes:   nodes,
		Edges:   []Edge{edge("A", "B"), edge("B", "C"), edge("C", "D")},
		Offered: map[string]float64{"A": 700},
	}
}

// 无预算：4 跳链在正反馈下不可收敛，末端节点进入雪崩。
func TestUnboundedChainAvalanche(t *testing.T) {
	g := chainGraph(0, true)
	res, err := Solve(g, DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Converged {
		t.Fatal("无预算链必须判定为不可收敛雪崩")
	}
	d := res.Nodes["D"]
	if !d.Avalanche {
		t.Fatalf("末端 D 应雪崩, loadRatio=%f p=%f", d.LoadRatio, d.FailProb)
	}
	if d.LoadRatio <= 1 {
		t.Fatalf("雪崩节点应击穿硬容量, loadRatio=%f", d.LoadRatio)
	}
	// 放大倍率应沿链条单调放大。
	amps := []float64{
		res.Nodes["A"].Amplification,
		res.Nodes["B"].Amplification,
		res.Nodes["C"].Amplification,
		res.Nodes["D"].Amplification,
	}
	for i := 1; i < len(amps); i++ {
		if amps[i] < amps[i-1] {
			t.Fatalf("放大倍率应沿链不减: %v", amps)
		}
	}
	t.Logf("无预算放大倍率 A->D: %v", amps)
}

// 有 20%% 预算：链条收敛，所有节点负载低于容量，放大倍率被显著压低。
func TestBudgetedChainConverges(t *testing.T) {
	g := chainGraph(0.2, false)
	res, err := Solve(g, DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Converged {
		for _, n := range res.Order {
			r := res.Nodes[n]
			t.Logf("%s logical=%f physical=%f ratio=%f p=%f avalanche=%v",
				n, r.LogicalRate, r.PhysicalRate, r.LoadRatio, r.FailProb, r.Avalanche)
		}
		t.Fatal("20% 预算链应收敛")
	}
	for _, n := range res.Order {
		r := res.Nodes[n]
		if r.LoadRatio >= 1 {
			t.Fatalf("节点 %s 不应击穿容量, ratio=%f", n, r.LoadRatio)
		}
		if r.Amplification > 1.2+1e-9 {
			t.Fatalf("节点 %s 放大倍率应被预算压在 1.2 附近, got %f", n, r.Amplification)
		}
	}
	t.Logf("有预算总放大倍率: %f", res.TotalAmplification)
}

// 64 倍上界复现：恒定高故障率 + 无预算时，每跳放大趋近 4，4 节点链端到端 ~64。
func TestWorstCase64xAmplification(t *testing.T) {
	curve := ConstCurve{P: 0.999}
	ingress := EdgeRetry{MaxAttempts: 4, UnlimitedBudget: true}
	g := &Graph{
		Nodes: []Node{
			{Name: "A", Capacity: 1e9, Curve: curve, IngressRetry: &ingress},
			{Name: "B", Capacity: 1e9, Curve: curve},
			{Name: "C", Capacity: 1e9, Curve: curve},
			{Name: "D", Capacity: 1e9, Curve: curve},
		},
		Edges: []Edge{
			{From: "A", To: "B", Fanout: 1, Retry: EdgeRetry{MaxAttempts: 4, UnlimitedBudget: true}},
			{From: "B", To: "C", Fanout: 1, Retry: EdgeRetry{MaxAttempts: 4, UnlimitedBudget: true}},
			{From: "C", To: "D", Fanout: 1, Retry: EdgeRetry{MaxAttempts: 4, UnlimitedBudget: true}},
		},
		Offered: map[string]float64{"A": 100},
	}
	res, err := Solve(g, DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	// 三条边每跳 geom(0.999, 4) ≈ 3.994；三跳连乘 ≈ 63.7。
	edgeProduct := res.Nodes["B"].Amplification * res.Nodes["C"].Amplification * res.Nodes["D"].Amplification
	if math.Abs(edgeProduct-64) > 1.5 {
		t.Fatalf("边侧端到端放大应逼近 64, got %f", edgeProduct)
	}
	t.Logf("最坏情况边侧端到端放大: %f (TotalAmplification=%f)", edgeProduct, res.TotalAmplification)
}

// 保底：低流量下 MinRetryRate 折算的比例仍允许保底重试。
func TestMinRetryRateFloor(t *testing.T) {
	g := chainGraph(0.0, false)
	for i := range g.Edges {
		g.Edges[i].Retry.MinRetryRate = 5 // 每秒保底 5 个额外尝试
	}
	res, err := Solve(g, DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	b := res.Nodes["B"]
	// 逻辑 100，保底比例 5/100=5%，放大应略高于 1。
	if b.Amplification <= 1 {
		t.Fatalf("保底应产生额外尝试, amp=%f", b.Amplification)
	}
	if b.RetryExtra <= 0 {
		t.Fatal("RetryExtra 应为正")
	}
}

// 最大安全预算：二分结果落在“收敛/雪崩”边界上。
func TestMaxSafeBudgets(t *testing.T) {
	// 其它边先给保守的安全预算，逐边求单条边可放开的最大 budgetRatio。
	g := chainGraph(0.1, false)
	safe, err := MaxSafeBudgets(g, DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, sb := range safe {
		if !sb.Needed {
			continue // 该边在基线配置下本就安全
		}
		if sb.MaxBudgetRatio <= 0 || sb.MaxBudgetRatio == BudgetUnlimited {
			t.Fatalf("安全预算应为有限正值, got %f", sb.MaxBudgetRatio)
		}
		// 边界下验证：略低于安全值收敛，略高于则雪崩。
		lo := cloneGraph(g)
		hi := cloneGraph(g)
		applyBudget(lo, sb.Edge, sb.MaxBudgetRatio*0.99, false)
		applyBudget(hi, sb.Edge, sb.MaxBudgetRatio*1.05, false)
		if !graphConverged(lo, DefaultSolverOptions()) {
			t.Fatalf("低于安全预算应收敛: %f", sb.MaxBudgetRatio)
		}
		if graphConverged(hi, DefaultSolverOptions()) {
			t.Fatalf("高于安全预算应雪崩: %f", sb.MaxBudgetRatio)
		}
		t.Logf("边 %s->%s 最大安全 budgetRatio=%.4f", sb.Edge.From, sb.Edge.To, sb.MaxBudgetRatio)
	}
}

// 环检测：含环图返回 ErrCyclic。
func TestCycleRejected(t *testing.T) {
	g := &Graph{
		Nodes: []Node{{Name: "X", Capacity: 10, Curve: ConstCurve{P: 0.1}}, {Name: "Y", Capacity: 10, Curve: ConstCurve{P: 0.1}}},
		Edges: []Edge{
			{From: "X", To: "Y", Retry: EdgeRetry{MaxAttempts: 2}},
			{From: "Y", To: "X", Retry: EdgeRetry{MaxAttempts: 2}},
		},
		Offered: map[string]float64{"X": 1},
	}
	if _, err := Solve(g, DefaultSolverOptions()); err != ErrCyclic {
		t.Fatalf("应返回 ErrCyclic, got %v", err)
	}
}

// 单调性自检：故障率曲线在软阈值后单调不减。
func TestCurveMonotonic(t *testing.T) {
	c := LinearCurve{Soft: 0.7, Base: 0.05, PAtHard: 0.6}
	prev := 0.0
	for x := 0.0; x <= 2.0; x += 0.05 {
		p := c.PFail(x)
		if p < prev {
			t.Fatalf("故障率曲线必须单调不减: x=%f p=%f prev=%f", x, p, prev)
		}
		prev = p
	}
}
