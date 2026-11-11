package sanitsum

import "testing"

func TestValidateErrors(t *testing.T) {
	cases := map[string]*FuncGraph{
		"空类别": {
			Name: "NoCats", Params: []string{"s"}, Entry: "a",
			Blocks: []*Block{{ID: "a", Stmts: []Stmt{ReturnStmt{Ret: RetTrue}}}},
		},
		"后继缺失": {
			Name: "BadSucc", Categories: testCats, Params: []string{"s"}, Entry: "a",
			Blocks: []*Block{{ID: "a", Succs: []string{"missing"}}},
		},
		"返回块带后继": {
			Name: "RetSucc", Categories: testCats, Params: []string{"s"}, Entry: "a",
			Blocks: []*Block{
				{ID: "a", Stmts: []Stmt{ReturnStmt{Ret: RetTrue}}, Succs: []string{"b"}},
				{ID: "b", Stmts: []Stmt{ReturnStmt{Ret: RetFalse}}},
			},
		},
		"未知类别": {
			Name: "UnknownCat", Categories: testCats, Params: []string{"s"}, Entry: "a",
			Blocks: []*Block{
				{ID: "a", Stmts: []Stmt{SanitizeStmt{Target: Ref{Var: "s"}, Cat: "XSS"}},
					Succs: []string{"b"}},
				{ID: "b", Stmts: []Stmt{ReturnStmt{Ret: RetTrue}}},
			},
		},
	}
	for name, g := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Analyze(g); err == nil {
				t.Fatal("期望校验错误，实际通过")
			}
		})
	}
}

// TestResultTaint 验证命名结果变量：结果复用被净化的入参时，
// 其 ResultClean 继承对应类别的净化保证。
func TestResultTaint(t *testing.T) {
	g := &FuncGraph{
		Name:       "AliasResult",
		Categories: testCats,
		Params:     []string{"s"},
		Results:    []string{"out"},
		Entry:      "a",
		Blocks: []*Block{
			{
				ID: "a",
				Stmts: []Stmt{
					SanitizeStmt{Target: Ref{Var: "s"}, Cat: "SQL"},
					AssignStmt{Dst: Ref{Var: "out"}, RHS: RefOrConst{Ref: Ref{Var: "s"}}},
					ReturnStmt{Ret: RetTrue},
				},
			},
		},
	}
	m, err := Analyze(g)
	if err != nil {
		t.Fatalf("Analyze 错误: %v", err)
	}
	rs := m.Returns[RetTrue]
	assertCats(t, rs.ParamClean["s"], testCats, []Category{"SQL"}, "入参 s")
	assertCats(t, rs.ResultClean["out"], testCats, []Category{"SQL"}, "结果 out")
}

func TestCallerValidationErrors(t *testing.T) {
	m := guardSummary(t)
	t.Run("悬空分支序号", func(t *testing.T) {
		g := callerGraph(m, "correct")
		g.Blocks[0].Stmts[2] = BranchOnStmt{
			CallIndex: 9, Expect: RetTrue,
			Edges: map[RetVal]string{RetTrue: blkSafe, RetFalse: blkEnd},
		}
		if _, err := AnalyzeCaller(g); err == nil {
			t.Fatal("期望分支序号错误")
		}
	})
	t.Run("未知后继", func(t *testing.T) {
		g := callerGraph(m, "ignored")
		g.Blocks[0].Succs = []string{"ghost"}
		if _, err := AnalyzeCaller(g); err == nil {
			t.Fatal("期望未知后继错误")
		}
	})
	t.Run("类别不匹配", func(t *testing.T) {
		g := callerGraph(m, "correct")
		g.Categories = []Category{"SQL"}
		if _, err := AnalyzeCaller(g); err == nil {
			t.Fatal("期望类别布局错误")
		}
	})
}
