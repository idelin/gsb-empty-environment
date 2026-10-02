package costmodel

import (
	"math"
	"testing"
)

var testWeights = DefaultWeights()

// users 表：10000 页、1,000,000 行；索引高 3、叶子页 2000。
var testTable = TableStats{Name: "users", NumPages: 10000, NumTuples: 1000000}
var testIndex = IndexStats{Name: "idx_users_age", Table: "users", Height: 3, LeafPages: 2000}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestSeqScanCost 验证 SeqScan 的 I/O + CPU 复合打分。
func TestSeqScanCost(t *testing.T) {
	s := &SeqScan{Table: testTable}
	// I/O: 10000 * 1.0 = 10000；CPU: 1e6 * 0.01 = 10000
	want := 20000.0
	if got := s.Cost(testWeights); !almostEqual(got, want) {
		t.Fatalf("SeqScan cost = %v, want %v", got, want)
	}
}

// TestFilterCostRecursive 验证 Filter 递归累加子树成本并叠加比较运算 CPU。
func TestFilterCostRecursive(t *testing.T) {
	f := &Filter{Child: &SeqScan{Table: testTable}, Selectivity: 0.1}
	// 子树 20000 + 比较 1e6 * 0.0025 = 2500
	want := 22500.0
	if got := f.Cost(testWeights); !almostEqual(got, want) {
		t.Fatalf("Filter(SeqScan) cost = %v, want %v", got, want)
	}
	if got := f.Rows(); !almostEqual(got, 100000) {
		t.Fatalf("Filter rows = %v, want 100000", got)
	}
}

// TestIndexScanCost 验证 IndexScan 的随机 I/O 与索引 CPU 打分。
func TestIndexScanCost(t *testing.T) {
	s := &IndexScan{Table: testTable, Index: testIndex, Selectivity: 0.01}
	// I/O: (3 + 2000*0.01 + 10000*0.01) * 4.0 = 123 * 4 = 492
	// CPU: 1e6*0.01*(0.005+0.01) = 150
	want := 642.0
	if got := s.Cost(testWeights); !almostEqual(got, want) {
		t.Fatalf("IndexScan cost = %v, want %v", got, want)
	}
}

// TestHashJoinCostRecursive 验证多子树计划的总成本递归求和。
func TestHashJoinCostRecursive(t *testing.T) {
	small := TableStats{Name: "dept", NumPages: 10, NumTuples: 100}
	j := &HashJoin{
		Outer:           &SeqScan{Table: small},
		Inner:           &SeqScan{Table: small},
		JoinSelectivity: 0.01,
	}
	// 每个 SeqScan: 10*1 + 100*0.01 = 11；建表+探测: 100*0.0025*2 = 0.5
	want := 22.5
	if got := j.Cost(testWeights); !almostEqual(got, want) {
		t.Fatalf("HashJoin cost = %v, want %v", got, want)
	}
}

// TestBreakEvenSwitch 验证选择率跨越拐点时计划正确切换。
func TestBreakEvenSwitch(t *testing.T) {
	sStar := BreakEvenSelectivity(testWeights, testTable, testIndex)
	t.Logf("break-even selectivity s* = %.6f", sStar)
	if sStar <= 0 || sStar >= 1 {
		t.Fatalf("s* = %v 超出 (0,1)，测试数据不合理", sStar)
	}

	below := sStar * 0.5
	above := math.Min(sStar*1.5, 1.0)

	if p := ChooseScanPlan(testWeights, testTable, testIndex, below); p.Rows() != 0 {
		if _, ok := p.(*IndexScan); !ok {
			t.Fatalf("s=%.6f < s* 应选择 IndexScan，实际 %s", below, p.String())
		}
	}
	if p := ChooseScanPlan(testWeights, testTable, testIndex, above); p.Rows() != 0 {
		if _, ok := p.(*Filter); !ok {
			t.Fatalf("s=%.6f > s* 应选择 SeqScan+Filter，实际 %s", above, p.String())
		}
	}

	// 拐点处两计划成本应相等。
	seq := &Filter{Child: &SeqScan{Table: testTable}, Selectivity: sStar}
	idx := &IndexScan{Table: testTable, Index: testIndex, Selectivity: sStar}
	if diff := math.Abs(seq.Cost(testWeights) - idx.Cost(testWeights)); diff > 1e-6 {
		t.Fatalf("拐点处成本差 = %v，应约等于 0", diff)
	}
}

// TestChooseScanPlanMonotonic 扫描选择率区间，验证切换只发生一次（单调性）。
func TestChooseScanPlanMonotonic(t *testing.T) {
	switches := 0
	prev := ChooseScanPlan(testWeights, testTable, testIndex, 0.0001).String()
	for s := 0.001; s <= 1.0; s += 0.001 {
		cur := ChooseScanPlan(testWeights, testTable, testIndex, s).String()
		if cur != prev {
			switches++
			prev = cur
		}
	}
	if switches != 1 {
		t.Fatalf("选择率从 0 到 1 扫描，计划切换次数 = %d，应为 1", switches)
	}
}
