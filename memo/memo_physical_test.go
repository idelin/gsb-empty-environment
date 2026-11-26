package memo

import "testing"

// TestMergeJoinReusesNaturalOrder 验证基表自然有序时 MergeJoin 直接下推
// 排序需求并复用，省去 Sort Enforcer。
func TestMergeJoinReusesNaturalOrder(t *testing.T) {
	schema := &Schema{Tables: []*Table{
		{Name: "t1", Rows: 100, SortKeys: []string{"t1.a"},
			Distrib: Distrib{Singleton: true}},
		{Name: "t2", Rows: 200, SortKeys: []string{"t2.b"},
			Distrib: Distrib{Singleton: true}},
	}}
	tree := NewInnerJoin(NewScan("t1"), NewScan("t2"),
		[]JoinCond{{Left: "t1.a", Right: "t2.b"}})

	m := NewMemo(schema)
	if _, err := m.Build(tree); err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Explore()

	best, err := NewOptimizer(m, DefaultCostModel{}).BestPlan(
		PhysProps{SortCols: []string{"t1.a"}})
	if err != nil {
		t.Fatalf("optimize: %v", err)
	}

	kinds := countKinds(best)
	if kinds[PhysMergeJoin] != 1 {
		t.Fatalf("expected a MergeJoin, got %v in %s", kinds, best.PlanKey())
	}
	if kinds[PhysSort] != 0 {
		t.Fatalf("natural order should remove Sort, got %v in %s", kinds, best.PlanKey())
	}
	if kinds[PhysHashJoin] != 0 {
		t.Fatalf("MergeJoin expected to win, plan=%s", best.PlanKey())
	}
	if !satisfies(best.Props(), PhysProps{SortCols: []string{"t1.a"}}) {
		t.Fatalf("plan does not satisfy required sort: %v", best.Props())
	}

	// 对照：无自然有序的等价 Schema 必须显式插入 Sort。
	schemaNoOrder := &Schema{Tables: []*Table{
		{Name: "t1", Rows: 100, Distrib: Distrib{Singleton: true}},
		{Name: "t2", Rows: 200, Distrib: Distrib{Singleton: true}},
	}}
	m2 := NewMemo(schemaNoOrder)
	if _, err := m2.Build(tree); err != nil {
		t.Fatalf("build no-order: %v", err)
	}
	m2.Explore()
	best2, err := NewOptimizer(m2, DefaultCostModel{}).BestPlan(
		PhysProps{SortCols: []string{"t1.a"}})
	if err != nil {
		t.Fatalf("optimize no-order: %v", err)
	}
	if countKinds(best2)[PhysSort] == 0 {
		t.Fatalf("expected Sort enforcer without natural order, plan=%s", best2.PlanKey())
	}
}

// TestExchangeEnforcesHashJoinColocation 验证两侧分布不一致时，HashJoin
// 通过 Exchange 获得同分布输入；自然同分布时不插入 Exchange。
func TestExchangeEnforcesHashJoinColocation(t *testing.T) {
	m := exploredMemo(t, chainTree(3))
	req := PhysProps{Distrib: Distrib{Hash: true, Columns: []string{"t1.a"}}}

	best, err := NewOptimizer(m, DefaultCostModel{}).BestPlan(req)
	if err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if !satisfies(best.Props(), req) {
		t.Fatalf("plan props %v do not satisfy %v", best.Props(), req)
	}
	kinds := countKinds(best)
	if kinds[PhysExchange] == 0 {
		t.Fatalf("expected Exchange to enforce colocation, plan=%s", best.PlanKey())
	}
	assertNoAdjacentEnforcers(t, best)

	// 两表自然同分布（按各自连接键哈希）：无需任何 Exchange。
	schemaTwo := &Schema{Tables: []*Table{
		{Name: "t1", Rows: 100, Distrib: Distrib{Hash: true, Columns: []string{"t1.a"}}},
		{Name: "t2", Rows: 200, Distrib: Distrib{Hash: true, Columns: []string{"t2.b"}}},
	}}
	twoTree := NewInnerJoin(NewScan("t1"), NewScan("t2"),
		[]JoinCond{{Left: "t1.a", Right: "t2.b"}})
	m2 := NewMemo(schemaTwo)
	if _, err := m2.Build(twoTree); err != nil {
		t.Fatalf("build: %v", err)
	}
	m2.Explore()
	best2, err := NewOptimizer(m2, DefaultCostModel{}).BestPlan(PhysProps{})
	if err != nil {
		t.Fatalf("optimize colocated: %v", err)
	}
	if countKinds(best2)[PhysExchange] != 0 {
		t.Fatalf("colocated inputs should need no Exchange, plan=%s", best2.PlanKey())
	}

	// 三表全哈希分布：左深连接在上层仍可能需要一次重分区，
	// 但计划中绝不允许相邻 Exchange 嵌套。
	schemaColocated := &Schema{Tables: []*Table{
		{Name: "t1", Rows: 100, Distrib: Distrib{Hash: true, Columns: []string{"t1.a"}}},
		{Name: "t2", Rows: 200, Distrib: Distrib{Hash: true, Columns: []string{"t2.b"}}},
		{Name: "t3", Rows: 300, Distrib: Distrib{Hash: true, Columns: []string{"t3.c"}}},
	}}
	m3 := NewMemo(schemaColocated)
	if _, err := m3.Build(chainTree(3)); err != nil {
		t.Fatalf("build: %v", err)
	}
	m3.Explore()
	best3, err := NewOptimizer(m3, DefaultCostModel{}).BestPlan(PhysProps{})
	if err != nil {
		t.Fatalf("optimize three-table: %v", err)
	}
	assertNoAdjacentEnforcers(t, best3)
}

// TestNoEnforcerRecursion 验证在组合需求与重复调用下不会产生
// Enforcer 套 Enforcer，也不会触发循环错误或失控递归。
func TestNoEnforcerRecursion(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		m := exploredMemo(t, chainTree(n))
		opt := NewOptimizer(m, DefaultCostModel{})
		for _, req := range testReqs() {
			best, err := opt.BestPlan(req)
			if err != nil {
				t.Fatalf("n=%d req=%s: %v", n, req.key(), err)
			}
			if best == nil {
				t.Fatalf("n=%d req=%s: nil plan", n, req.key())
			}
			assertNoAdjacentEnforcers(t, best)
			if !satisfies(best.Props(), req) {
				t.Fatalf("n=%d req=%s: plan props %v unsatisfied",
					n, req.key(), best.Props())
			}
		}
	}
}

// TestRequirementCacheEntries 验证对每组每种需求属性都缓存独立最优计划。
func TestRequirementCacheEntries(t *testing.T) {
	m := exploredMemo(t, chainTree(3))
	opt := NewOptimizer(m, DefaultCostModel{})
	for _, req := range testReqs() {
		if _, err := opt.BestPlan(req); err != nil {
			t.Fatalf("req=%s: %v", req.key(), err)
		}
	}
	if opt.CacheSize() == 0 {
		t.Fatalf("expected memoized best plans, cache empty")
	}

	// 重复查询必须命中缓存并返回同一计划实例。
	req := testReqs()[0]
	p1, _ := opt.BestPlan(req)
	before := opt.CacheSize()
	p2, _ := opt.BestPlan(req)
	if opt.CacheSize() != before || p1 != p2 {
		t.Fatalf("repeated request did not hit the cache")
	}
}

// TestEnforcerCycleGuard 验证 in-flight 循环检测兜底机制可被触发并返回
// 类型化错误，而不是无限递归。
func TestEnforcerCycleGuard(t *testing.T) {
	m := exploredMemo(t, chainTree(3))
	opt := NewOptimizer(m, DefaultCostModel{})
	req := PhysProps{SortCols: []string{"t1.a"}}
	key := opt.cacheKey(m.Root(), req)

	// 人为把同一 (Group, 需求) 标记为求解中，模拟 Enforcer 自循环。
	opt.inFlight[key] = true
	_, err := opt.solve(m.Root(), req)
	if err == nil {
		t.Fatalf("expected enforcer cycle error")
	}
	if _, ok := err.(*ErrEnforcerCycle); !ok {
		t.Fatalf("expected *ErrEnforcerCycle, got %T: %v", err, err)
	}
}
