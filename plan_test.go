package costmodel

import (
	"math"
	"testing"
)

var (
	w = DefaultWeights()
	// 测试表：10 万元组，1 万数据页，300 索引页。
	users = TableStats{Name: "users", Rows: 100000, Pages: 10000, IndexPages: 300}
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// 手工核算：
// SeqScan = 10000*1.0 + 100000*0.01 = 11000
// SeqScan+Filter = 11000 + 100000*0.0025 = 11250（与选择率无关）
// IndexScan(s) = (300+10000*s)*4 + 100000*0.005 + 100000*s*0.01 = 1700 + 41000*s
// 拐点：1700 + 41000*s = 11250 => s* = 9550/41000 ≈ 0.232927
const crossover = 9550.0 / 41000.0

func TestSeqScanCost(t *testing.T) {
	s := &SeqScan{Table: users}
	if got := s.Cost(w); !almostEqual(got, 11000) {
		t.Fatalf("SeqScan cost = %v, want 11000", got)
	}
	if got := s.OutputRows(); !almostEqual(got, 100000) {
		t.Fatalf("SeqScan rows = %v, want 100000", got)
	}
}

func TestFilterCostIsRecursive(t *testing.T) {
	f := &Filter{Selectivity: 0.5, Child: &SeqScan{Table: users}}
	// 总成本必须包含子树成本 + 对子计划每行一次谓词求值。
	if got := f.Cost(w); !almostEqual(got, 11250) {
		t.Fatalf("SeqScan+Filter cost = %v, want 11250", got)
	}
	if got := f.OutputRows(); !almostEqual(got, 50000) {
		t.Fatalf("Filter rows = %v, want 50000", got)
	}
}

func TestIndexScanCost(t *testing.T) {
	s := &IndexScan{Table: users, Selectivity: 0.1}
	// (300+1000)*4 + 500 + 100 = 5800
	if got := s.Cost(w); !almostEqual(got, 5800) {
		t.Fatalf("IndexScan cost = %v, want 5800", got)
	}
	if got := s.OutputRows(); !almostEqual(got, 10000) {
		t.Fatalf("IndexScan rows = %v, want 10000", got)
	}
}

func TestHashJoinCostIsRecursive(t *testing.T) {
	orders := TableStats{Name: "orders", Rows: 1000, Pages: 100}
	hj := &HashJoin{
		JoinSelectivity: 0.001,
		Build:           &SeqScan{Table: orders},
		Probe:           &Filter{Selectivity: 0.1, Child: &SeqScan{Table: users}},
	}
	// build=110, probe=11250, hash=1000*0.01+10000*0.01=110 => 11470
	if got := hj.Cost(w); !almostEqual(got, 11470) {
		t.Fatalf("HashJoin cost = %v, want 11470", got)
	}
	if got := hj.OutputRows(); !almostEqual(got, 10000) {
		t.Fatalf("HashJoin rows = %v, want 10000", got)
	}
}

func TestChooseScanPlanBySelectivity(t *testing.T) {
	cases := []struct {
		sel  float64
		want string
	}{
		{0.001, "IndexScan"},    // 高选择性：索引扫描便宜
		{0.1, "IndexScan"},      //
		{0.5, "SeqScan+Filter"}, // 低选择性：随机回表代价超过顺序扫描
		{0.9, "SeqScan+Filter"},
	}
	for _, c := range cases {
		if _, name := ChooseScanPlan(users, c.sel, w); name != c.want {
			t.Errorf("sel=%v: got %s, want %s", c.sel, name, c.want)
		}
	}
}

// 在解析拐点两侧各取一个微小邻域，验证计划恰好在阈值处切换。
func TestChooseScanPlanCrossover(t *testing.T) {
	const eps = 1e-6
	if _, name := ChooseScanPlan(users, crossover-eps, w); name != "IndexScan" {
		t.Errorf("just below crossover: got %s, want IndexScan", name)
	}
	if _, name := ChooseScanPlan(users, crossover+eps, w); name != "SeqScan+Filter" {
		t.Errorf("just above crossover: got %s, want SeqScan+Filter", name)
	}
}

// 用二分查找数值定位拐点，与解析解对比，误差应在容差内。
func TestCrossoverMatchesAnalytic(t *testing.T) {
	lo, hi := 0.0, 1.0
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if _, name := ChooseScanPlan(users, mid, w); name == "IndexScan" {
			lo = mid
		} else {
			hi = mid
		}
	}
	if got := (lo + hi) / 2; math.Abs(got-crossover) > 1e-9 {
		t.Errorf("numeric crossover = %v, want %v", got, crossover)
	}
}
