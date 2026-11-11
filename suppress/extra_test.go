package suppress

import (
	"errors"
	"testing"
)

// TestFourByFour 覆盖最大维度，确认坐标序最小最低成本方案与穷举一致。
func TestFourByFour(t *testing.T) {
	lower, upper := uniform(4, 4, 2)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{4, 4, 4, 4},
		ColSums:   []int{4, 4, 4, 4},
		Sensitive: []Cell{{0, 0}, {2, 3}},
	}
	plan := mustSolve(t, p)
	oracle := bruteForceOracle(t, p)
	if !cellsEqual(plan.Extra, oracle) {
		t.Fatalf("Solve=%v 与预言机=%v 不一致", plan.Extra, oracle)
	}
	if len(plan.Witnesses) != 2 {
		t.Fatalf("见证数量=%d, 期望 2", len(plan.Witnesses))
	}
}

// TestEmptySensitive 验证没有敏感单元时返回空方案、零成本与空见证。
func TestEmptySensitive(t *testing.T) {
	lower, upper := uniform(2, 2, 3)
	p := &Problem{
		Lower:   lower,
		Upper:   upper,
		RowSums: []int{3, 3},
		ColSums: []int{3, 3},
	}
	plan, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve 失败: %v", err)
	}
	if len(plan.Extra) != 0 || plan.TotalCost != 0 || len(plan.Witnesses) != 0 {
		t.Fatalf("空敏感单元应返回空方案，得到 %+v", plan)
	}
}

// TestEnumerateLimit 验证显式上限与 ErrDomainTooLarge。
func TestEnumerateLimit(t *testing.T) {
	lower, upper := uniform(2, 2, 9)
	p := &Problem{
		Lower:   lower,
		Upper:   upper,
		RowSums: []int{9, 9},
		ColSums: []int{9, 9},
	}
	tables, err := Enumerate(p, 5)
	if !errors.Is(err, ErrDomainTooLarge) {
		t.Fatalf("err=%v, 期望 ErrDomainTooLarge", err)
	}
	if len(tables) != 5 {
		t.Fatalf("截断数量=%d, 期望 5", len(tables))
	}
	full, err := Enumerate(p, 0)
	if err != nil {
		t.Fatalf("完整穷举失败: %v", err)
	}
	if len(full) <= 5 {
		t.Fatalf("测试前提失效：可行表数量 %d 不够大", len(full))
	}
}

// TestVerifyRejectsBadWitness 验证 Verify 能识别公开单元不一致的伪造见证。
func TestVerifyRejectsBadWitness(t *testing.T) {
	lower, upper := uniform(2, 2, 4)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{3, 3},
		ColSums:   []int{3, 3},
		Sensitive: []Cell{{0, 0}, {0, 1}, {1, 0}, {1, 1}},
	}
	plan := mustSolve(t, p)
	plan.Witnesses[0].A[0][0] = -1
	plan.Witnesses[0].A[0][1] = 4
	err := Verify(p, plan, 0)
	if err == nil {
		t.Fatal("伪造见证应当被 Verify 拒绝")
	}
}

// TestWitnessAgreement 逐单元核对两份见证在抑制集合之外完全相同。
func TestWitnessAgreement(t *testing.T) {
	lower, upper := uniform(3, 3, 5)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{6, 3, 3},
		ColSums:   []int{4, 4, 4},
		Sensitive: []Cell{{0, 0}},
	}
	plan := mustSolve(t, p)
	hidden := map[Cell]bool{}
	for _, cell := range plan.Extra {
		hidden[cell] = true
	}
	for _, s := range p.Sensitive {
		hidden[s] = true
	}
	for _, w := range plan.Witnesses {
		if w.A[w.Sensitive.Row][w.Sensitive.Col] >= w.B[w.Sensitive.Row][w.Sensitive.Col] {
			t.Fatalf("目标单元两份见证未严格递增: %+v", w)
		}
		for i := range w.A {
			for j := range w.A[i] {
				if !hidden[Cell{i, j}] && w.A[i][j] != w.B[i][j] {
					t.Fatalf("公开单元 (%d,%d) 在见证间不一致", i, j)
				}
			}
		}
	}
}
