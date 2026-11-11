package sanitsum

import "testing"

var testCats = []Category{"SQL", "HTML", "Shell"}

// guardGraph 建模典型条件清洗器：
//   - true 路径：先净化 SQL，再返回 true；
//   - false 路径：原样返回 false，不提供任何保证；
//   - 同时对副作用字段 u.Email 只做 HTML 净化，验证字段单独建模。
func guardGraph() *FuncGraph {
	return &FuncGraph{
		Name:       "SanitizeIfSafe",
		Categories: testCats,
		Params:     []string{"s", "u"},
		Results:    []string{"ok"},
		Entry:      "entry",
		Blocks: []*Block{
			{
				ID:    "entry",
				Stmts: []Stmt{BranchStmt{}},
				Succs: []string{"clean", "raw"},
			},
			{
				ID: "clean",
				Stmts: []Stmt{
					SanitizeStmt{Target: Ref{Var: "s"}, Cat: "SQL"},
					SanitizeStmt{Target: Ref{Var: "u", Field: "Email"}, Cat: "HTML"},
					AssignStmt{Dst: Ref{Var: "ok"}, RHS: RefOrConst{Const: "true"}},
					ReturnStmt{Ret: RetTrue},
				},
			},
			{
				ID: "raw",
				Stmts: []Stmt{
					AssignStmt{Dst: Ref{Var: "ok"}, RHS: RefOrConst{Const: "false"}},
					ReturnStmt{Ret: RetFalse},
				},
			},
		},
	}
}

func TestAnalyzeGuardSummary(t *testing.T) {
	g := guardGraph()
	m, err := Analyze(g)
	if err != nil {
		t.Fatalf("Analyze 返回错误: %v", err)
	}

	trueRS := m.Returns[RetTrue]
	if trueRS == nil {
		t.Fatal("缺少 true 返回值摘要")
	}
	assertCats(t, trueRS.ParamClean["s"], g.Categories, []Category{"SQL"}, "true/s")
	assertCats(t, trueRS.ParamClean["u"], g.Categories, nil, "true/u 整体未净化")
	assertCats(t, trueRS.FieldClean[Ref{Var: "u", Field: "Email"}], g.Categories,
		[]Category{"HTML"}, "true/u.Email")
	assertCats(t, trueRS.ResultClean["ok"], g.Categories, testCats, "true/ok 为常量结果")

	falseRS := m.Returns[RetFalse]
	if falseRS == nil {
		t.Fatal("缺少 false 返回值摘要")
	}
	assertCats(t, falseRS.ParamClean["s"], g.Categories, nil, "false/s 无净化保证")
	assertCats(t, falseRS.FieldClean[Ref{Var: "u", Field: "Email"}], g.Categories,
		nil, "false/u.Email 无净化保证")
}

// TestEnumeratePathsMatchesDAG 用完整路径枚举重算摘要并逐槽位比较。
func TestEnumeratePathsMatchesDAG(t *testing.T) {
	for name, g := range map[string]*FuncGraph{
		"guard":   guardGraph(),
		"diamond": diamondGraph(),
	} {
		t.Run(name, func(t *testing.T) {
			dag, err := Analyze(g)
			if err != nil {
				t.Fatalf("Analyze 错误: %v", err)
			}
			enum, err := EnumeratePaths(g)
			if err != nil {
				t.Fatalf("EnumeratePaths 错误: %v", err)
			}
			if !dag.Equal(enum) {
				t.Fatalf("DAG 摘要与路径枚举摘要不一致\nDAG=%v\nENUM=%v", dag, enum)
			}
		})
	}
}

// diamondGraph 的 true 返回值有两条路径：一条只净化 SQL，一条只净化 HTML。
// 不确定分支取交集后，s 上不存在任何“必然”净化类别。
func diamondGraph() *FuncGraph {
	return &FuncGraph{
		Name:       "Diamond",
		Categories: testCats,
		Params:     []string{"s"},
		Results:    []string{"ok"},
		Entry:      "entry",
		Blocks: []*Block{
			{ID: "entry", Stmts: []Stmt{BranchStmt{}}, Succs: []string{"left", "right"}},
			{ID: "left", Stmts: []Stmt{
				SanitizeStmt{Target: Ref{Var: "s"}, Cat: "SQL"},
			}, Succs: []string{"join"}},
			{ID: "right", Stmts: []Stmt{
				SanitizeStmt{Target: Ref{Var: "s"}, Cat: "HTML"},
			}, Succs: []string{"join"}},
			{ID: "join", Stmts: []Stmt{ReturnStmt{Ret: RetTrue}}},
		},
	}
}

func TestDiamondIntersection(t *testing.T) {
	m, err := Analyze(diamondGraph())
	if err != nil {
		t.Fatalf("Analyze 错误: %v", err)
	}
	rs := m.Returns[RetTrue]
	assertCats(t, rs.ParamClean["s"], testCats, nil, "菱形汇合后无必然净化")
}

func TestRejectCycle(t *testing.T) {
	g := &FuncGraph{
		Name:       "Loopy",
		Categories: testCats,
		Params:     []string{"s"},
		Results:    []string{"ok"},
		Entry:      "a",
		Blocks: []*Block{
			{ID: "a", Stmts: []Stmt{BranchStmt{}}, Succs: []string{"b"}},
			{ID: "b", Succs: []string{"a"}},
		},
	}
	if _, err := Analyze(g); err == nil {
		t.Fatal("含循环图必须报错")
	}
	if _, err := EnumeratePaths(g); err == nil {
		t.Fatal("含循环图在路径枚举下也必须报错")
	}
}

func assertCats(t *testing.T, set CatSet, order, want []Category, label string) {
	t.Helper()
	got := set.List(order)
	if !sameStringSlice(catStrings(got), catStrings(want)) {
		t.Fatalf("%s: 净化类别 = %v, 期望 %v", label, got, want)
	}
}

func catStrings(cs []Category) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c)
	}
	return out
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
