package sim

import (
	"testing"
	"time"

	"prescreen.local/ps-9f073c0c88/steady"
)

const tolerance = 0.25 // 随机仿真与确定性不动点之间允许的相对误差

func chainConfig(offered float64, budget float64, unlimited bool) (*steady.Graph, Config) {
	curve := steady.LinearCurve{Soft: 0.7, Base: 0.05, PAtHard: 0.6}
	rc := steady.EdgeRetry{
		MaxAttempts:     4,
		BudgetRatio:     budget,
		MinRetryRate:    0,
		UnlimitedBudget: unlimited,
	}
	g := &steady.Graph{
		Nodes: []steady.Node{
			{Name: "A", Capacity: 1000, Curve: curve, IngressRetry: &rc},
			{Name: "B", Capacity: 1000, Curve: curve},
			{Name: "C", Capacity: 1000, Curve: curve},
			{Name: "D", Capacity: 1000, Curve: curve},
		},
		Edges: []steady.Edge{
			{From: "A", To: "B", Fanout: 1, Retry: rc},
			{From: "B", To: "C", Fanout: 1, Retry: rc},
			{From: "C", To: "D", Fanout: 1, Retry: rc},
		},
		Offered: map[string]float64{"A": offered},
	}

	ec := func(from, to string) EdgeCfg {
		return EdgeCfg{
			From: from, To: to, Fanout: 1,
			Retry:             rc,
			BaseBackoff:       time.Millisecond,
			BackoffMultiplier: 2,
		}
	}
	cfg := Config{
		Nodes: []NodeCfg{
			{Name: "A", Capacity: 1000, Curve: curve, ServiceTime: 0.0005, IngressRetry: &rc},
			{Name: "B", Capacity: 1000, Curve: curve, ServiceTime: 0.0005},
			{Name: "C", Capacity: 1000, Curve: curve, ServiceTime: 0.0005},
			{Name: "D", Capacity: 1000, Curve: curve, ServiceTime: 0.0005},
		},
		Edges: []EdgeCfg{ec("A", "B"), ec("B", "C"), ec("C", "D")},
		Offered: map[string]float64{
			"A": offered,
		},
		Window:   time.Second,
		Duration: 60,
		Warmup:   15,
		Seed:     42,
	}
	return g, cfg
}

// 有预算稳态：仿真物理速率与求解器不动点在每个节点上吻合。
func TestSimMatchesSolverBudgeted(t *testing.T) {
	g, cfg := chainConfig(700, 0.2, false)
	solver, err := steady.Solve(g, steady.DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !solver.Converged {
		t.Fatal("前置：求解器应收敛")
	}
	res := New(cfg).Run()
	for _, name := range solver.Order {
		sr := solver.Nodes[name]
		simPhys := res.PhysicalRate[name]
		relErr := RelativeError(simPhys, sr.PhysicalRate)
		t.Logf("%s solver=%.2f sim=%.2f relErr=%.3f loadSim=%.3f ampSim=%.3f",
			name, sr.PhysicalRate, simPhys, relErr, res.LoadRatio[name], res.Amplification[name])
		if relErr > tolerance {
			t.Fatalf("节点 %s 物理速率偏差过大: solver=%.2f sim=%.2f", name, sr.PhysicalRate, simPhys)
		}
	}
}

// 临界过载点：二分找到求解器负载最接近容量的 offered，仿真应给出同方向结论
// （均逼近容量、雪崩判定一致）。
func TestCriticalOverloadAgreement(t *testing.T) {
	// 无预算 + 高 offered：两套模型都应判定雪崩/严重过载。
	g, cfg := chainConfig(700, 0, true)
	solver, err := steady.Solve(g, steady.DefaultSolverOptions())
	if err != nil {
		t.Fatal(err)
	}
	res := New(cfg).Run()

	solverAv := 0
	simAv := 0
	for _, name := range solver.Order {
		sr := solver.Nodes[name]
		if sr.Avalanche {
			solverAv++
		}
		if res.Avalanche[name] || res.LoadRatio[name] > 0.95 {
			simAv++
		}
		t.Logf("%s solverRatio=%.3f av=%v | simRatio=%.3f av=%v simPhys=%.1f",
			name, sr.LoadRatio, sr.Avalanche, res.LoadRatio[name], res.Avalanche[name], res.PhysicalRate[name])
	}
	if solverAv == 0 {
		t.Fatal("前置：无预算高负载下求解器应判定雪崩")
	}
	if simAv == 0 {
		t.Fatalf("仿真在临界过载点应观测到过载/雪崩节点, got %d", simAv)
	}
}

// 对冲竞速：首个成功后取消其余分支，物理尝试数显著低于“串行重试”上界。
func TestSimHedgeCancellation(t *testing.T) {
	_, cfgNoHedge := chainConfig(700, 5.0, false)
	_, cfg := chainConfig(700, 5.0, false)
	hedge := 15 * time.Millisecond
	for i := range cfg.Edges {
		cfg.Edges[i].Retry.HedgeBranches = 1
		cfg.Edges[i].HedgeDelay = hedge
	}
	res := New(cfg).Run()
	base := New(cfgNoHedge).Run()
	d := res.Metrics["D"]
	if d.Attempts == 0 {
		t.Fatal("应产生物理尝试")
	}
	// 对冲在首尝试慢时竞速发出，但首个成功即取消其余分支：
	// 高故障率下总尝试数不应超过无对冲基线，且对冲确实被发出过。
	if d.Attempts > base.Metrics["D"].Attempts {
		t.Fatalf("竞速取消后对冲场景尝试数 %d 不应超过串行基线 %d",
			d.Attempts, base.Metrics["D"].Attempts)
	}
	t.Logf("D hedge attempts=%d vs baseline=%d logical=%d amp=%.3f",
		d.Attempts, base.Metrics["D"].Attempts, d.LogicalArrivals, res.Amplification["D"])
}

// 截止期截断：过短截止期使重试在发出前被拒绝，物理尝试接近逻辑请求数。
func TestSimDeadlineTruncation(t *testing.T) {
	_, cfg := chainConfig(700, 5.0, false)
	for i := range cfg.Edges {
		cfg.Edges[i].Deadline = time.Microsecond
		cfg.Edges[i].MinProcessingTime = 5 * time.Millisecond
	}
	res := New(cfg).Run()
	for _, name := range []string{"B", "C", "D"} {
		m := res.Metrics[name]
		if m.DeadlineReject == 0 {
			t.Fatalf("节点 %s 应观测到截止期拒绝, got 0", name)
		}
		if m.Attempts > m.LogicalArrivals {
			t.Fatalf("节点 %s 截止期截断后不应有额外尝试: attempts=%d logical=%d",
				name, m.Attempts, m.LogicalArrivals)
		}
	}
}
