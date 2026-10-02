package llk

import (
	"errors"
	"strings"
	"testing"
)

// 验收文法：A -> 'a' 'b' 'c' | 'a' 'b' 'd'
func acceptanceGrammar() *Grammar {
	return &Grammar{
		Start: "A",
		Rules: []*Rule{
			{Name: "A", Alts: []Alternative{
				{T("a"), T("b"), T("c")},
				{T("a"), T("b"), T("d")},
			}},
		},
	}
}

// K=2 时两个候选式共享公共前缀 "a b"，无法区分，必须抛出文法超界异常。
func TestK2GrammarOutOfBounds(t *testing.T) {
	_, err := NewParser(acceptanceGrammar(), 2)
	if err == nil {
		t.Fatal("K=2 时期望构建失败，却成功了")
	}
	var oob *GrammarOutOfBoundsError
	if !errors.As(err, &oob) {
		t.Fatalf("期望 *GrammarOutOfBoundsError，实际为 %T: %v", err, err)
	}
	if oob.Rule != "A" || oob.K != 2 {
		t.Fatalf("异常信息不符: %+v", oob)
	}
	if got := strings.Join(oob.Lookahead, " "); got != "a b" {
		t.Fatalf("冲突前瞻应为 \"a b\"，实际为 %q", got)
	}
	t.Logf("超界异常信息: %v", err)
}

// K=3 时成功构建分支预测表，表项为 "a b c" -> 候选式0，"a b d" -> 候选式1。
func TestK3PredictionTable(t *testing.T) {
	p, err := NewParser(acceptanceGrammar(), 3)
	if err != nil {
		t.Fatalf("K=3 时构建应成功: %v", err)
	}
	table := p.Table("A")
	want := map[string]int{"a b c": 0, "a b d": 1}
	if len(table) != len(want) {
		t.Fatalf("预测表应为 %v，实际为 %v", want, table)
	}
	for la, alt := range want {
		if table[la] != alt {
			t.Fatalf("前瞻 %q 应映射到候选式 %d，实际为 %d", la, alt, table[la])
		}
	}
}

// K=3 下无回溯线性解析两种输入。
func TestK3ParseLinear(t *testing.T) {
	p, err := NewParser(acceptanceGrammar(), 3)
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	cases := []struct {
		input []string
		want  string
	}{
		{[]string{"a", "b", "c"}, "(A a b c)"},
		{[]string{"a", "b", "d"}, "(A a b d)"},
	}
	for _, c := range cases {
		tree, err := p.Parse(c.input)
		if err != nil {
			t.Fatalf("解析 %v 失败: %v", c.input, err)
		}
		if tree.String() != c.want {
			t.Fatalf("解析 %v：期望树 %s，实际 %s", c.input, c.want, tree)
		}
	}
}

// 非法输入必须报错而不是回溯重试。
func TestParseError(t *testing.T) {
	p, err := NewParser(acceptanceGrammar(), 3)
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	if _, err := p.Parse([]string{"a", "b", "x"}); err == nil {
		t.Fatal("输入 a b x 应报解析错误")
	} else {
		t.Logf("解析错误信息: %v", err)
	}
	if _, err := p.Parse([]string{"a", "b", "c", "d"}); err == nil {
		t.Fatal("输入 a b c d 应报多余输入错误")
	}
}

// 含非终结符嵌套的文法，验证 FIRST_k 不动点计算与递归下降。
// S -> A A；A -> 'a' 'b' 'c' | 'a' 'b' 'd'
func TestNestedGrammar(t *testing.T) {
	g := &Grammar{
		Start: "S",
		Rules: []*Rule{
			{Name: "S", Alts: []Alternative{{N("A"), N("A")}}},
			{Name: "A", Alts: []Alternative{
				{T("a"), T("b"), T("c")},
				{T("a"), T("b"), T("d")},
			}},
		},
	}
	if _, err := NewParser(g, 2); err == nil {
		t.Fatal("嵌套文法 K=2 同样应超界")
	}
	p, err := NewParser(g, 3)
	if err != nil {
		t.Fatalf("嵌套文法 K=3 构建失败: %v", err)
	}
	tree, err := p.Parse([]string{"a", "b", "c", "a", "b", "d"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if want := "(S (A a b c) (A a b d))"; tree.String() != want {
		t.Fatalf("期望 %s，实际 %s", want, tree)
	}
}

// 递归规则验证线性解析：L -> 'x' L | 'y'（LL(1)），n 个 Token 恰好消费 n 次。
func TestRecursiveRuleLinear(t *testing.T) {
	g := &Grammar{
		Start: "L",
		Rules: []*Rule{
			{Name: "L", Alts: []Alternative{
				{T("x"), N("L")},
				{T("y")},
			}},
		},
	}
	p, err := NewParser(g, 1)
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	tokens := append([]string{"x", "x", "x", "x", "x"}, "y")
	tree, err := p.Parse(tokens)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if want := "(L x (L x (L x (L x (L x (L y))))))"; tree.String() != want {
		t.Fatalf("期望 %s，实际 %s", want, tree)
	}
}
