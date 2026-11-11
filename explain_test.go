package netsec

import (
	"fmt"
	"reflect"
	"testing"
)

func TestExplainRejectShortCircuit(t *testing.T) {
	c := NewCompiler()
	rule := c.And(c.Fact("a"), c.Fact("b"), c.Fact("c"))

	ex := rule.Explain(map[string]Value{"a": Fail, "b": Pass, "c": Pass})
	if ex.Decision != Reject || ex.Size != 1 {
		t.Fatalf("explanation = %+v, want reject size 1", ex)
	}
	if !reflect.DeepEqual(ex.Facts, []string{"a"}) || ex.Values[0] != Fail {
		t.Fatalf("explanation facts = %v values = %v", ex.Facts, ex.Values)
	}
}

func TestExplainTieBreakByID(t *testing.T) {
	c := NewCompiler()
	rule := c.Or(c.Fact("zeta"), c.Fact("alpha"))

	// 任一 pass 即 pass，两个单事实解释同规模，取字典序最小的 alpha。
	ex := rule.Explain(map[string]Value{"zeta": Pass, "alpha": Pass})
	if ex.Decision != Allow || ex.Size != 1 {
		t.Fatalf("explanation = %+v, want allow size 1", ex)
	}
	if !reflect.DeepEqual(ex.Facts, []string{"alpha"}) {
		t.Fatalf("tie break picked %v, want alpha", ex.Facts)
	}
}

func TestExplainTieBreakByLexicographicSet(t *testing.T) {
	c := NewCompiler()
	a, b, d := c.Fact("a"), c.Fact("b"), c.Fact("d")
	rule := c.And(c.Or(a, b), d)

	// pass 需要 d=pass 且 a/b 任一 pass；最小规模 2，候选 {a,d} 与 {b,d}，取前者。
	ex := rule.Explain(map[string]Value{"a": Pass, "b": Pass, "d": Pass})
	if ex.Size != 2 || !reflect.DeepEqual(ex.Facts, []string{"a", "d"}) {
		t.Fatalf("explanation = %+v, want [a d]", ex)
	}
}

func TestExplainUnselectedBecomeUnknown(t *testing.T) {
	c := NewCompiler()
	rule := c.And(c.Fact("a"), c.Fact("b"))

	// 完整赋值下 fail；仅 a=fail 时 AND 已 fail（b 变 unknown 不影响）。
	ex := rule.Explain(map[string]Value{"a": Fail, "b": Fail})
	if ex.Size != 1 || ex.Facts[0] != "a" {
		t.Fatalf("explanation = %+v, want only a", ex)
	}

	// 反过来：只有 b=fail 能解释拒收，a 必须留在 unknown。
	ex = rule.Explain(map[string]Value{"a": Unknown, "b": Fail})
	if ex.Size != 1 || ex.Facts[0] != "b" {
		t.Fatalf("explanation = %+v, want only b", ex)
	}
}

func TestExplainPendingRequiresNoFacts(t *testing.T) {
	c := NewCompiler()
	rule := c.And(c.Fact("a"), c.Fact("b"))

	// unknown 与 unknown 的合取仍是 unknown：全删光结论也不变。
	ex := rule.Explain(map[string]Value{"a": Unknown, "b": Unknown})
	if ex.Decision != Pending || ex.Size != 0 || len(ex.Facts) != 0 || len(ex.Values) != 0 {
		t.Fatalf("explanation = %+v, want empty pending explanation", ex)
	}

	// 即使存在已知事实，只要全 unknown 赋值即可保持待定，最小解释仍为空。
	ex = rule.Explain(map[string]Value{"a": Pass, "b": Unknown})
	if ex.Decision != Pending || ex.Size != 0 {
		t.Fatalf("explanation = %+v, want empty pending explanation", ex)
	}
}

func TestExplainSkipsUnknownAndUnreachable(t *testing.T) {
	c := NewCompiler()
	rule := c.And(c.Fact("a"), c.Fact("b"))

	ex := rule.Explain(map[string]Value{
		"a":         Fail,
		"b":         Unknown,
		"ghost":     Fail,
		"alsoGhost": Pass,
	})
	if ex.Size != 1 || ex.Facts[0] != "a" {
		t.Fatalf("explanation = %+v, want only reachable known fact a", ex)
	}
}

func TestExplainSharedSubexpression(t *testing.T) {
	c := NewCompiler()
	a, b := c.Fact("a"), c.Fact("b")
	shared := c.And(a, b)
	root := c.Or(shared, c.Not(shared))

	// shared=fail 时 root = fail OR pass = pass；只需让 shared 为 fail 的一个事实。
	ex := root.Explain(map[string]Value{"a": Fail, "b": Pass})
	if ex.Decision != Allow || ex.Size != 1 || ex.Facts[0] != "a" {
		t.Fatalf("explanation = %+v, want single fact a", ex)
	}

	// shared=unknown 时两侧皆 unknown，整体待定，最小解释为空。
	ex = root.Explain(map[string]Value{"a": Pass, "b": Unknown})
	if ex.Decision != Pending || ex.Size != 0 {
		t.Fatalf("explanation = %+v, want empty pending", ex)
	}
}

func TestExplainFullSetAlwaysSuffices(t *testing.T) {
	c := NewCompiler()
	rule := c.And(c.Fact("a"), c.Fact("b"), c.Fact("c"))
	ex := rule.Explain(map[string]Value{"a": Pass, "b": Pass, "c": Pass})
	if ex.Decision != Allow || ex.Size != 3 {
		t.Fatalf("explanation = %+v, need all three pass facts", ex)
	}
}

func ExampleRule_Explain() {
	c := NewCompiler()
	rule := c.And(c.Or(c.Fact("spf"), c.Fact("dkim")), c.Not(c.Fact("dmarc")))

	facts := map[string]Value{"spf": Pass, "dkim": Fail, "dmarc": Fail}
	ex := rule.Explain(facts)

	ruleValue := rule.Eval(facts)
	fmt.Println("rule value:", ruleValue.String())
	fmt.Println("decision:", ex.Decision.String())
	fmt.Println("minimum facts:", ex.Size)
	for i, id := range ex.Facts {
		fmt.Printf("  %s = %s\n", id, ex.Values[i].String())
	}
	// Output:
	// rule value: pass
	// decision: allow
	// minimum facts: 2
	//   dmarc = fail
	//   spf = pass
}
