package netsec

import "testing"

func TestEvalMissingFactsAreUnknown(t *testing.T) {
	c := NewCompiler()
	spf := c.Fact("spf")
	dkim := c.Fact("dkim")
	dmarc := c.Fact("dmarc")

	// (spf OR dkim) AND NOT dmarc
	rule := c.And(c.Or(spf, dkim), c.Not(dmarc))

	if got := rule.Eval(nil); got != Unknown {
		t.Fatalf("all-missing eval = %v, want unknown", got)
	}
	if rule.Decide(map[string]Value{}) != Pending {
		t.Fatal("all-missing decision must be pending, not reject")
	}

	// spf 缺失、dkim 缺失、dmarc 失败 => (U OR U) AND NOT(F)= U AND P = U
	if got := rule.Eval(map[string]Value{"dmarc": Fail}); got != Unknown {
		t.Fatalf("eval = %v, want unknown", got)
	}

	// spf 通过、dmarc 失败 => (P OR U) AND NOT(F) = P AND P = P
	if got := rule.Eval(map[string]Value{"spf": Pass, "dmarc": Fail}); got != Pass {
		t.Fatalf("eval = %v, want pass", got)
	}

	// 两通道皆失败、dmarc 通过 => (F OR F) AND NOT(P) = F AND F = F
	got := rule.Eval(map[string]Value{"spf": Fail, "dkim": Fail, "dmarc": Pass})
	if got != Fail || rule.Decide(map[string]Value{"spf": Fail, "dkim": Fail, "dmarc": Pass}) != Reject {
		t.Fatalf("eval = %v, want fail/reject", got)
	}
}

func TestEvalSharedSubexpressionComputedOnce(t *testing.T) {
	c := NewCompiler()
	a := c.Fact("a")
	b := c.Fact("b")
	shared := c.And(a, b)
	root := c.Or(shared, c.Not(shared))

	facts := map[string]Value{"a": Pass, "b": Unknown}
	// shared = U；U OR NOT(U) = U OR U = U
	if got := root.Eval(facts); got != Unknown {
		t.Fatalf("shared eval = %v, want unknown", got)
	}

	facts = map[string]Value{"a": Pass, "b": Pass}
	// shared = P；P OR NOT(P) = P OR F = P
	if got := root.Eval(facts); got != Pass {
		t.Fatalf("shared eval = %v, want pass", got)
	}
}

func TestEvalIgnoresUndeclaredFacts(t *testing.T) {
	c := NewCompiler()
	rule := c.Fact("spf")
	got := rule.Eval(map[string]Value{"spf": Pass, "stranger": Fail})
	if got != Pass {
		t.Fatalf("eval = %v, want pass", got)
	}
}

func TestDoubleNegationAndNested(t *testing.T) {
	c := NewCompiler()
	a := c.Fact("a")
	rule := c.Not(c.Not(a))
	for _, v := range []Value{Pass, Fail, Unknown} {
		if got := rule.Eval(map[string]Value{"a": v}); got != v {
			t.Errorf("double negation of %v = %v", v, got)
		}
	}
}
