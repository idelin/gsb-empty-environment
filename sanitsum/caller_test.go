package sanitsum

import "testing"

// guardSummary 复用库侧分析产出的摘要，作为调用方测试的输入契约。
func guardSummary(t *testing.T) *Summary {
	t.Helper()
	m, err := Analyze(guardGraph())
	if err != nil {
		t.Fatalf("构造库摘要失败: %v", err)
	}
	return m
}

const (
	blkCall   = "call"
	blkSafe   = "safe"
	blkUnsafe = "unsafe"
	blkEnd    = "end"
)

// callerBlocks 按策略构造三种调用方：
//   - correct：if ok { sink }，仅在 true 分支使用字符串；
//   - negated：if !ok { sink }，在 false 分支误用（取反条件）；
//   - ignored：完全忽略返回值，汇合后直接 sink。
func callerBlocks(m *Summary, mode string) []*CallerBlock {
	call := CallStmt{
		Callee:  m,
		Args:    map[string]Ref{"s": {Var: "input"}, "u": {Var: "user"}},
		RetRefs: []Ref{{Var: "ok"}},
	}
	end := &CallerBlock{
		ID:    blkEnd,
		Stmts: nil,
	}
	switch mode {
	case "correct":
		return []*CallerBlock{
			{
				ID: blkCall,
				Stmts: []CallerStmt{
					TaintStmt{Dst: Ref{Var: "input"}, Cat: "SQL"},
					call,
					BranchOnStmt{CallIndex: 0, Expect: RetTrue,
						Edges: map[RetVal]string{RetTrue: blkSafe, RetFalse: blkEnd}},
				},
				Succs: []string{blkSafe, blkEnd},
			},
			{
				ID:    blkSafe,
				Stmts: []CallerStmt{SinkStmt{Target: Ref{Var: "input"}, Cat: "SQL", Label: "query"}},
				Succs: []string{blkEnd},
			},
			end,
		}
	case "negated":
		return []*CallerBlock{
			{
				ID: blkCall,
				Stmts: []CallerStmt{
					TaintStmt{Dst: Ref{Var: "input"}, Cat: "SQL"},
					call,
					// 取反：当前块选择 false 返回值，错误地把未净化字符串送入 sink。
					BranchOnStmt{CallIndex: 0, Expect: RetFalse,
						Edges: map[RetVal]string{RetTrue: blkEnd, RetFalse: blkUnsafe}},
				},
				Succs: []string{blkEnd, blkUnsafe},
			},
			{
				ID:    blkUnsafe,
				Stmts: []CallerStmt{SinkStmt{Target: Ref{Var: "input"}, Cat: "SQL", Label: "query"}},
				Succs: []string{blkEnd},
			},
			end,
		}
	case "ignored":
		return []*CallerBlock{
			{
				ID: blkCall,
				Stmts: []CallerStmt{
					TaintStmt{Dst: Ref{Var: "input"}, Cat: "SQL"},
					// 不接 BranchOnStmt：条件净化永不生效，模拟忽略返回值。
					call,
				},
				Succs: []string{blkUnsafe},
			},
			{
				ID:    blkUnsafe,
				Stmts: []CallerStmt{SinkStmt{Target: Ref{Var: "input"}, Cat: "SQL", Label: "query"}},
				Succs: []string{blkEnd},
			},
			end,
		}
	}
	return nil
}

func callerGraph(m *Summary, mode string) *CallerGraph {
	return &CallerGraph{
		Name:       "caller-" + mode,
		Categories: testCats,
		Entry:      blkCall,
		Blocks:     callerBlocks(m, mode),
	}
}

func TestCallerCorrectGuard(t *testing.T) {
	findings, err := AnalyzeCaller(callerGraph(guardSummary(t), "correct"))
	if err != nil {
		t.Fatalf("AnalyzeCaller 错误: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("正确守卫下不应有注入告警，得到 %v", findings)
	}
}

func TestCallerNegatedCondition(t *testing.T) {
	findings, err := AnalyzeCaller(callerGraph(guardSummary(t), "negated"))
	if err != nil {
		t.Fatalf("AnalyzeCaller 错误: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("取反条件应产生 1 条告警，得到 %v", findings)
	}
	if findings[0].Cat != "SQL" || findings[0].Block != blkUnsafe {
		t.Fatalf("告警定位错误: %+v", findings[0])
	}
}

func TestCallerIgnoredReturn(t *testing.T) {
	findings, err := AnalyzeCaller(callerGraph(guardSummary(t), "ignored"))
	if err != nil {
		t.Fatalf("AnalyzeCaller 错误: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("忽略返回值应产生 1 条告警，得到 %v", findings)
	}
	if findings[0].Reason == "" {
		t.Fatal("告警缺少原因说明")
	}
}

// TestCallerFieldSideEffect 验证副作用字段单独建模：
// true 分支保证 u.Email 的 HTML 已净化，但不影响变量 u 整体。
func TestCallerFieldSideEffect(t *testing.T) {
	m := guardSummary(t)
	g := &CallerGraph{
		Name:       "caller-field",
		Categories: testCats,
		Entry:      blkCall,
		Blocks: []*CallerBlock{
			{
				ID: blkCall,
				Stmts: []CallerStmt{
					TaintStmt{Dst: Ref{Var: "user", Field: "Email"}, Cat: "HTML"},
					TaintStmt{Dst: Ref{Var: "user", Field: "Email"}, Cat: "SQL"},
					CallStmt{
						Callee:  m,
						Args:    map[string]Ref{"s": {Var: "input"}, "u": {Var: "user"}},
						RetRefs: []Ref{{Var: "ok"}},
					},
					BranchOnStmt{CallIndex: 0, Expect: RetTrue,
						Edges: map[RetVal]string{RetTrue: blkSafe, RetFalse: blkEnd}},
				},
				Succs: []string{blkSafe, blkEnd},
			},
			{
				ID: blkSafe,
				Stmts: []CallerStmt{
					// HTML 已在 true 路径被净化，无告警。
					SinkStmt{Target: Ref{Var: "user", Field: "Email"}, Cat: "HTML", Label: "html"},
					// SQL 净化只作用于 s，不作用于字段，保留告警。
					SinkStmt{Target: Ref{Var: "user", Field: "Email"}, Cat: "SQL", Label: "sql"},
				},
				Succs: []string{blkEnd},
			},
			{ID: blkEnd},
		},
	}
	findings, err := AnalyzeCaller(g)
	if err != nil {
		t.Fatalf("AnalyzeCaller 错误: %v", err)
	}
	if len(findings) != 1 || findings[0].Cat != "SQL" {
		t.Fatalf("字段副作用期望仅 1 条 SQL 告警，得到 %v", findings)
	}
}

// TestCallerMultiCategory 验证多类别条件净化逐类生效：
// true 分支只清掉 SQL，HTML 污染仍然可以触发告警。
func TestCallerMultiCategory(t *testing.T) {
	m := guardSummary(t)
	g := &CallerGraph{
		Name:       "caller-multi",
		Categories: testCats,
		Entry:      blkCall,
		Blocks: []*CallerBlock{
			{
				ID: blkCall,
				Stmts: []CallerStmt{
					TaintStmt{Dst: Ref{Var: "input"}, Cat: "SQL"},
					TaintStmt{Dst: Ref{Var: "input"}, Cat: "HTML"},
					CallStmt{Callee: m,
						Args:    map[string]Ref{"s": {Var: "input"}, "u": {Var: "user"}},
						RetRefs: []Ref{{Var: "ok"}}},
					BranchOnStmt{CallIndex: 0, Expect: RetTrue,
						Edges: map[RetVal]string{RetTrue: blkSafe, RetFalse: blkEnd}},
				},
				Succs: []string{blkSafe, blkEnd},
			},
			{
				ID: blkSafe,
				Stmts: []CallerStmt{
					SinkStmt{Target: Ref{Var: "input"}, Cat: "SQL", Label: "sql"},
					SinkStmt{Target: Ref{Var: "input"}, Cat: "HTML", Label: "html"},
				},
				Succs: []string{blkEnd},
			},
			{ID: blkEnd},
		},
	}
	findings, err := AnalyzeCaller(g)
	if err != nil {
		t.Fatalf("AnalyzeCaller 错误: %v", err)
	}
	if len(findings) != 1 || findings[0].Cat != "HTML" {
		t.Fatalf("期望仅 HTML 告警，得到 %v", findings)
	}
}
