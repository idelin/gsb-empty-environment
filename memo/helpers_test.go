package memo

import "testing"

// 测试共用的 5 表 Schema：不同表刻意配置不同的原生排序与分布属性，
// 以便覆盖自然有序复用与 Exchange 强制两类路径。
func testSchema() *Schema {
	return &Schema{Tables: []*Table{
		{Name: "t1", Rows: 100, SortKeys: []string{"t1.a"},
			Distrib: Distrib{Hash: true, Columns: []string{"t1.a"}}},
		{Name: "t2", Rows: 200, SortKeys: nil,
			Distrib: Distrib{Hash: true, Columns: []string{"t2.b"}}},
		{Name: "t3", Rows: 300, SortKeys: []string{"t3.c"},
			Distrib: Distrib{Singleton: true}},
		{Name: "t4", Rows: 400, SortKeys: nil,
			Distrib: Distrib{Singleton: true}},
		{Name: "t5", Rows: 500, SortKeys: []string{"t5.e"},
			Distrib: Distrib{Hash: true, Columns: []string{"t5.e"}}},
	}}
}

// chainTree 构造 n 表链式内连接（3<=n<=5）。
func chainTree(n int) *LogicalNode {
	tree := NewInnerJoin(
		NewScan("t1"),
		NewScan("t2"),
		[]JoinCond{{Left: "t1.a", Right: "t2.b"}},
	)
	if n >= 3 {
		tree = NewInnerJoin(tree, NewScan("t3"),
			[]JoinCond{{Left: "t2.b", Right: "t3.c"}})
	}
	if n >= 4 {
		tree = NewInnerJoin(tree, NewScan("t4"),
			[]JoinCond{{Left: "t3.c", Right: "t4.d"}})
	}
	if n >= 5 {
		tree = NewInnerJoin(tree, NewScan("t5"),
			[]JoinCond{{Left: "t4.d", Right: "t5.e"}})
	}
	return tree
}

// exploredMemo 构建并探索到不动点的 Memo。
func exploredMemo(t *testing.T, tree *LogicalNode) *Memo {
	t.Helper()
	m := NewMemo(testSchema())
	if _, err := m.Build(tree); err != nil {
		t.Fatalf("build memo: %v", err)
	}
	m.Explore()
	return m
}

// countKinds 统计计划树中各物理算子出现次数。
func countKinds(p Plan) map[string]int {
	out := map[string]int{}
	var walk func(Plan)
	walk = func(x Plan) {
		if x == nil {
			return
		}
		out[x.Kind()]++
		for _, c := range x.Children() {
			walk(c)
		}
	}
	walk(p)
	return out
}

// assertNoAdjacentEnforcers 断言不存在同类 Enforcer 相邻嵌套。
func assertNoAdjacentEnforcers(t *testing.T, p Plan) {
	t.Helper()
	var walk func(Plan, string)
	walk = func(x Plan, parentKind string) {
		if x == nil {
			return
		}
		if (x.Kind() == PhysSort || x.Kind() == PhysExchange) && x.Kind() == parentKind {
			t.Fatalf("adjacent nested %s enforcers in plan: %s", x.Kind(), p.PlanKey())
		}
		for _, c := range x.Children() {
			walk(c, x.Kind())
		}
	}
	walk(p, "")
}

// testReqs 是验收使用的需求属性矩阵。
func testReqs() []PhysProps {
	return []PhysProps{
		{},
		{SortCols: []string{"t1.a"}},
		{SortCols: []string{"t2.b", "t3.c"}},
		{Distrib: Distrib{Singleton: true}},
		{Distrib: Distrib{Hash: true, Columns: []string{"t1.a"}}},
		{SortCols: []string{"t1.a"}, Distrib: Distrib{Hash: true, Columns: []string{"t1.a"}}},
		{SortCols: []string{"t3.c"}, Distrib: Distrib{Singleton: true}},
	}
}
