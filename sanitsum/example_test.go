package sanitsum_test

import (
	"fmt"

	"example.org/netsec/q0387/sanitsum"
)

// ExampleAnalyze 展示宿主如何为“只在 true 时保证字符串安全”的库函数
// 推导摘要，并比较 DAG 分析与完整路径枚举的结果。
func ExampleAnalyze() {
	cats := []sanitsum.Category{"SQL", "HTML"}
	g := &sanitsum.FuncGraph{
		Name:       "EscapeIfSafe",
		Categories: cats,
		Params:     []string{"s"},
		Results:    []string{"ok"},
		Entry:      "entry",
		Blocks: []*sanitsum.Block{
			{ID: "entry", Stmts: []sanitsum.Stmt{sanitsum.BranchStmt{}},
				Succs: []string{"clean", "raw"}},
			{ID: "clean", Stmts: []sanitsum.Stmt{
				sanitsum.SanitizeStmt{Target: sanitsum.Ref{Var: "s"}, Cat: "SQL"},
				sanitsum.ReturnStmt{Ret: sanitsum.RetTrue},
			}},
			{ID: "raw", Stmts: []sanitsum.Stmt{
				sanitsum.ReturnStmt{Ret: sanitsum.RetFalse},
			}},
		},
	}

	dag, err := sanitsum.Analyze(g)
	if err != nil {
		panic(err)
	}
	enum, err := sanitsum.EnumeratePaths(g)
	if err != nil {
		panic(err)
	}
	fmt.Println("摘要一致:", dag.Equal(enum))
	fmt.Println("true 净化:", dag.Returns[sanitsum.RetTrue].ParamClean["s"].List(cats))
	fmt.Println("false 净化:", dag.Returns[sanitsum.RetFalse].ParamClean["s"].List(cats))
	// Output:
	// 摘要一致: true
	// true 净化: [SQL]
	// false 净化: []
}
