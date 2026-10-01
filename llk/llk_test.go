package llk

import (
	"errors"
	"strings"
	"testing"
)

// 验收文法：A -> 'a' 'b' 'c' | 'a' 'b' 'd'
func acceptanceGrammar() *Grammar {
	return NewGrammar("A",
		Production{LHS: "A", RHS: []Symbol{T("a"), T("b"), T("c")}},
		Production{LHS: "A", RHS: []Symbol{T("a"), T("b"), T("d")}},
	)
}

// K=2 时两个候选的 FIRST_2 都是 {a b}，无法区分，必须抛出文法超界异常。
func TestBuildTable_K2_ThrowsLLKError(t *testing.T) {
	_, err := BuildTable(acceptanceGrammar(), 2)
	if err == nil {
		t.Fatal("expected LLKError for K=2, got nil")
	}
	var llkErr *LLKError
	if !errors.As(err, &llkErr) {
		t.Fatalf("expected *LLKError, got %T: %v", err, err)
	}
	if llkErr.K != 2 || llkErr.NonTerminal != "A" {
		t.Fatalf("unexpected error fields: %+v", llkErr)
	}
	if got := strings.Join(llkErr.Lookahead, " "); got != "a b" {
		t.Fatalf("conflicting lookahead = %q, want %q", got, "a b")
	}
	t.Logf("LLKError: %v", llkErr)
}

// K=3 时 FIRST_3 分别为 {a b c} 与 {a b d}，可区分，预测表构建成功。
func TestBuildTable_K3_Succeeds(t *testing.T) {
	table, err := BuildTable(acceptanceGrammar(), 3)
	if err != nil {
		t.Fatalf("BuildTable(K=3) failed: %v", err)
	}
	if got := len(table.entries["A"]); got != 2 {
		t.Fatalf("prediction table for A has %d entries, want 2", got)
	}
	if p := table.lookup("A", TokenSeq{"a", "b", "c"}); p == nil || p.RHS[2].Name != "c" {
		t.Fatalf("lookup [a b c] = %v, want production ending in 'c'", p)
	}
	if p := table.lookup("A", TokenSeq{"a", "b", "d"}); p == nil || p.RHS[2].Name != "d" {
		t.Fatalf("lookup [a b d] = %v, want production ending in 'd'", p)
	}
	t.Logf("\n%s", table.String())
}

// K=3 下无回溯线性解析两种输入。
func TestParse_K3_Linear(t *testing.T) {
	table, err := BuildTable(acceptanceGrammar(), 3)
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	for _, input := range [][]string{
		{"a", "b", "c"},
		{"a", "b", "d"},
	} {
		p := NewParser(table)
		if err := p.Parse(input); err != nil {
			t.Fatalf("Parse(%v) failed: %v", input, err)
		}
		// 单非终结符文法：恰好 1 次预测表查询，无回溯。
		if p.Steps != 1 {
			t.Fatalf("Parse(%v) used %d prediction steps, want 1", input, p.Steps)
		}
	}
}

// 非法输入必须报解析错误而非误接受。
func TestParse_K3_RejectsInvalid(t *testing.T) {
	table, err := BuildTable(acceptanceGrammar(), 3)
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	for _, input := range [][]string{
		{"a", "b", "x"},      // 前瞻不在表中
		{"a", "b"},           // 输入过短
		{"a", "b", "c", "c"}, // 多余 Token
	} {
		p := NewParser(table)
		err := p.Parse(input)
		if err == nil {
			t.Fatalf("Parse(%v) succeeded, want error", input)
		}
		var parseErr *ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("Parse(%v) error = %T, want *ParseError", input, err)
		}
	}
}

// 含非终结符嵌套与 ε 的文法，验证 FIRST_k 不动点计算与递归下降。
// S -> A 'x' | A 'y' ; A -> 'a' | ε
// K=2: FIRST_2(A x)={a x, x}, FIRST_2(A y)={a y, y}，可区分。
func TestParse_NestedWithEpsilon(t *testing.T) {
	g := NewGrammar("S",
		Production{LHS: "S", RHS: []Symbol{N("A"), T("x")}},
		Production{LHS: "S", RHS: []Symbol{N("A"), T("y")}},
		Production{LHS: "A", RHS: []Symbol{T("a")}},
		Production{LHS: "A", RHS: []Symbol{Eps}},
	)
	table, err := BuildTable(g, 2)
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	valid := [][]string{{"a", "x"}, {"a", "y"}, {"x"}, {"y"}}
	for _, input := range valid {
		if err := NewParser(table).Parse(input); err != nil {
			t.Fatalf("Parse(%v) failed: %v", input, err)
		}
	}
	if err := NewParser(table).Parse([]string{"a", "z"}); err == nil {
		t.Fatal("Parse([a z]) succeeded, want error")
	}
}

// 需要任意深前瞻的非 LL(k) 结构：A -> 'a'^n 'b' | 'a'^n 'c'（n 超过 K）。
// 对任意固定 K 都必须抛出文法超界异常。
func TestBuildTable_UnboundedLookahead(t *testing.T) {
	mk := func(n int) *Grammar {
		mkRHS := func(last string) []Symbol {
			rhs := make([]Symbol, 0, n+1)
			for i := 0; i < n; i++ {
				rhs = append(rhs, T("a"))
			}
			return append(rhs, T(last))
		}
		return NewGrammar("A",
			Production{LHS: "A", RHS: mkRHS("b")},
			Production{LHS: "A", RHS: mkRHS("c")},
		)
	}
	for _, k := range []int{1, 2, 3, 4} {
		// 公共前缀长度 4，K<=4 时必然超界。
		if _, err := BuildTable(mk(4), k); err == nil {
			t.Fatalf("K=%d: expected LLKError, got nil", k)
		} else {
			var llkErr *LLKError
			if !errors.As(err, &llkErr) {
				t.Fatalf("K=%d: error = %T, want *LLKError", k, err)
			}
		}
	}
	// K=5 可以区分 'a'^4 'b' 与 'a'^4 'c'。
	if _, err := BuildTable(mk(4), 5); err != nil {
		t.Fatalf("K=5: unexpected error: %v", err)
	}
}
