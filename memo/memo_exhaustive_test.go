package memo

import "testing"

// TestMemoMatchesExhaustiveOptimum 在 3~5 表连接树上，对每种需求属性比较
// Memo 物理搜索与全物理计划空间穷举的全局最小代价与最优计划结构。
func TestMemoMatchesExhaustiveOptimum(t *testing.T) {
	cost := DefaultCostModel{}
	for _, n := range []int{3, 4, 5} {
		tree := chainTree(n)
		schema := testSchema()

		m := NewMemo(schema)
		if _, err := m.Build(tree); err != nil {
			t.Fatalf("build: %v", err)
		}
		m.Explore()
		opt := NewOptimizer(m, cost)

		for _, req := range testReqs() {
			memPlan, err := opt.BestPlan(req)
			if err != nil {
				t.Fatalf("n=%d req=%s memo: %v", n, req.key(), err)
			}
			exhPlan, err := ExhaustiveBest(schema, cost, tree, req)
			if err != nil {
				t.Fatalf("n=%d req=%s exhaustive: %v", n, req.key(), err)
			}
			if exhPlan == nil {
				t.Fatalf("n=%d req=%s: exhaustive returned no plan", n, req.key())
			}
			if memPlan.Cost() != exhPlan.Cost() {
				t.Fatalf("n=%d req=%s cost mismatch memo=%d exhaustive=%d\nmemo=%s\nexh =%s",
					n, req.key(), memPlan.Cost(), exhPlan.Cost(),
					memPlan.PlanKey(), exhPlan.PlanKey())
			}
			if memPlan.PlanKey() != exhPlan.PlanKey() {
				t.Fatalf("n=%d req=%s plan mismatch\nmemo=%s\nexh =%s",
					n, req.key(), memPlan.PlanKey(), exhPlan.PlanKey())
			}
		}
	}
}

// TestCustomCostModel 验证精确整型代价公式可整体替换，且两求解器仍保持一致。
func TestCustomCostModel(t *testing.T) {
	custom := customModel{}
	tree := chainTree(4)
	schema := testSchema()

	m := NewMemo(schema)
	if _, err := m.Build(tree); err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Explore()

	for _, req := range testReqs() {
		memPlan, err := NewOptimizer(m, custom).BestPlan(req)
		if err != nil {
			t.Fatalf("memo: %v", err)
		}
		exhPlan, err := ExhaustiveBest(schema, custom, tree, req)
		if err != nil {
			t.Fatalf("exhaustive: %v", err)
		}
		if memPlan.Cost() != exhPlan.Cost() || memPlan.PlanKey() != exhPlan.PlanKey() {
			t.Fatalf("req=%s mismatch memo=(%d,%s) exh=(%d,%s)",
				req.key(), memPlan.Cost(), memPlan.PlanKey(),
				exhPlan.Cost(), exhPlan.PlanKey())
		}
	}
}

type customModel struct{}

func (customModel) ScanCost(rows int64) int64 { return 100 + rows*3 }
func (customModel) HashJoinCost(l, r, out int64) int64 {
	return 2*l + 2*r + out + 50
}
func (customModel) MergeJoinCost(l, r, out int64) int64 {
	return l + r + out + 25
}
func (customModel) SortCost(rows int64, cols int) int64 {
	return rows * int64(cols+1) * 7
}
func (customModel) ExchangeCost(rows int64, cols int) int64 {
	return rows*int64(cols)*11 + 13
}
