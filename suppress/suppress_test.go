package suppress

import (
	"errors"
	"testing"
)

func uniform(r, c, hi int) ([][]int, [][]int) {
	lower := make([][]int, r)
	upper := make([][]int, r)
	for i := range lower {
		lower[i] = make([]int, c)
		upper[i] = make([]int, c)
		for j := range upper[i] {
			upper[i][j] = hi
		}
	}
	return lower, upper
}

func mustSolve(t *testing.T, p *Problem) *Plan {
	t.Helper()
	plan, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve 意外失败: %v", err)
	}
	if err := Verify(p, plan, 0); err != nil {
		t.Fatalf("Verify 拒绝方案: %v", err)
	}
	return plan
}

// TestClassicTwoByTwo 验证 2×2 经典案例：隐藏一个单元会被边际和恢复，
// 必须把另外三个单元全部额外隐藏。
func TestClassicTwoByTwo(t *testing.T) {
	lower, upper := uniform(2, 2, 9)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{4, 6},
		ColSums:   []int{3, 7},
		Sensitive: []Cell{{0, 0}},
	}
	plan := mustSolve(t, p)

	want := []Cell{{0, 1}, {1, 0}, {1, 1}}
	if !cellsEqual(plan.Extra, want) {
		t.Fatalf("Extra=%v, 期望 %v", plan.Extra, want)
	}
	if plan.TotalCost != 3 {
		t.Fatalf("TotalCost=%d, 期望 3", plan.TotalCost)
	}
	if len(plan.Witnesses) != 1 {
		t.Fatalf("见证数量=%d, 期望 1", len(plan.Witnesses))
	}
	w := plan.Witnesses[0]
	if w.A[0][0] != w.B[0][0]-1 {
		t.Fatalf("见证目标值 %d,%d 不是相邻两个可行值", w.A[0][0], w.B[0][0])
	}

	oracle := bruteForceOracle(t, p)
	if !cellsEqual(oracle, want) {
		t.Fatalf("穷举预言机=%v, 期望 %v", oracle, want)
	}
}

// TestThreeByThreeLexTie 验证成本相同按坐标排序。
func TestThreeByThreeLexTie(t *testing.T) {
	lower, upper := uniform(3, 3, 5)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{6, 3, 3},
		ColSums:   []int{4, 4, 4},
		Sensitive: []Cell{{0, 0}},
	}
	plan := mustSolve(t, p)
	oracle := bruteForceOracle(t, p)
	if !cellsEqual(plan.Extra, oracle) {
		t.Fatalf("Solve=%v 与穷举预言机=%v 不一致", plan.Extra, oracle)
	}
	if plan.TotalCost != len(plan.Extra) {
		t.Fatalf("统一单位成本下 TotalCost=%d 与单元数 %d 不符", plan.TotalCost, len(plan.Extra))
	}
	want := []Cell{{0, 1}, {1, 0}, {1, 1}}
	if !cellsEqual(plan.Extra, want) {
		t.Fatalf("坐标序最小集合=%v, 期望 %v", plan.Extra, want)
	}
}

// TestCustomCosts 验证非统一成本下选择成本最低而非单元数最少的集合。
func TestCustomCosts(t *testing.T) {
	lower, upper := uniform(3, 3, 5)
	cost := make([][]int, 3)
	for i := range cost {
		cost[i] = []int{1, 1, 1}
	}
	cost[0][1] = 50
	cost[1][1] = 50
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{6, 3, 3},
		ColSums:   []int{4, 4, 4},
		Sensitive: []Cell{{0, 0}},
		Cost:      cost,
	}
	plan := mustSolve(t, p)
	oracle := bruteForceOracle(t, p)
	if !cellsEqual(plan.Extra, oracle) {
		t.Fatalf("Solve=%v 与穷举预言机=%v 不一致", plan.Extra, oracle)
	}
	expectedCost := 0
	for _, cell := range plan.Extra {
		expectedCost += cost[cell.Row][cell.Col]
	}
	if plan.TotalCost != expectedCost {
		t.Fatalf("TotalCost=%d, 期望 %d", plan.TotalCost, expectedCost)
	}
	for _, cell := range plan.Extra {
		if cell == (Cell{0, 1}) || cell == (Cell{1, 1}) {
			t.Fatalf("方案不应包含高价单元，实际 %v", plan.Extra)
		}
	}
}

// TestZeroExtraJointAmbiguity 验证无需额外隐藏即可保护的情形：对角线两个
// 敏感单元已构成一条完整的二乘二交错环，空额外集合成本为零。
func TestZeroExtraJointAmbiguity(t *testing.T) {
	// 基表 [[1,2],[2,1]] 可沿 2×2 环双向各偏移一个单位（值域 0..4）。
	lower, upper := uniform(2, 2, 4)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{3, 3},
		ColSums:   []int{3, 3},
		Sensitive: []Cell{{0, 0}, {0, 1}, {1, 0}, {1, 1}},
	}
	plan := mustSolve(t, p)
	if len(plan.Extra) != 0 || plan.TotalCost != 0 {
		t.Fatalf("期望零额外抑制，得到 %v 成本 %d", plan.Extra, plan.TotalCost)
	}
	if len(plan.Witnesses) != 4 {
		t.Fatalf("见证数量=%d, 期望 4", len(plan.Witnesses))
	}
}

// TestUnprotectableFixedCell 验证边界钉死敏感单元时返回 ErrUnprotectable。
// 单行表没有二部图环，任何额外抑制都无法制造第二个可行值。
func TestUnprotectableFixedCell(t *testing.T) {
	lower := [][]int{{1, 2}}
	upper := [][]int{{1, 2}}
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{3},
		ColSums:   []int{1, 2},
		Sensitive: []Cell{{0, 0}},
	}
	if _, err := Solve(p); !errors.Is(err, ErrUnprotectable) {
		t.Fatalf("err=%v, 期望 ErrUnprotectable", err)
	}
}

// TestInfeasible 验证边际和与边界矛盾时返回 ErrInfeasible。
func TestInfeasible(t *testing.T) {
	lower, upper := uniform(2, 2, 1)
	p := &Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{4, 4},
		ColSums:   []int{4, 4},
		Sensitive: []Cell{{0, 0}},
	}
	if _, err := Solve(p); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err=%v, 期望 ErrInfeasible", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	base := func() *Problem {
		lower, upper := uniform(2, 2, 3)
		return &Problem{
			Lower:     lower,
			Upper:     upper,
			RowSums:   []int{3, 3},
			ColSums:   []int{3, 3},
			Sensitive: []Cell{{0, 0}},
		}
	}
	cases := map[string]func(*Problem){
		"维度为零": func(p *Problem) { p.Lower = nil },
		"五乘五超限": func(p *Problem) {
			p.Lower = make([][]int, 5)
			p.Upper = make([][]int, 5)
			for i := range p.Lower {
				p.Lower[i] = make([]int, 5)
				p.Upper[i] = make([]int, 5)
			}
		},
		"边界倒置":  func(p *Problem) { p.Lower[0][0] = 4 },
		"负下界":   func(p *Problem) { p.Lower[0][0] = -1 },
		"行列和不等": func(p *Problem) { p.RowSums[0] = 4 },
		"敏感坐标越界": func(p *Problem) {
			p.Sensitive[0] = Cell{2, 0}
		},
		"敏感单元重复": func(p *Problem) {
			p.Sensitive = []Cell{{0, 0}, {0, 0}}
		},
		"负成本": func(p *Problem) {
			p.Cost = [][]int{{0, 0}, {0, 0}}
			p.Cost[0][1] = -1
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base()
			mutate(p)
			if _, err := Solve(p); !errors.Is(err, ErrInvalidProblem) {
				t.Fatalf("err=%v, 期望 ErrInvalidProblem", err)
			}
		})
	}
}

// TestEnumerateKnownCount 核对穷举器在小型二乘二问题上的可行表数量。
func TestEnumerateKnownCount(t *testing.T) {
	lower, upper := uniform(2, 2, 2)
	p := &Problem{
		Lower:   lower,
		Upper:   upper,
		RowSums: []int{2, 2},
		ColSums: []int{2, 2},
	}
	tables, err := Enumerate(p, 0)
	if err != nil {
		t.Fatalf("Enumerate 失败: %v", err)
	}
	if len(tables) != 3 {
		t.Fatalf("可行表数量=%d, 期望 3", len(tables))
	}
	for _, table := range tables {
		for i, row := range table {
			rowTotal := 0
			for _, v := range row {
				rowTotal += v
			}
			if rowTotal != p.RowSums[i] {
				t.Fatalf("行和错误: %v", table)
			}
		}
	}
}
