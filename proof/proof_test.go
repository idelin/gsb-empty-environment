package proof

import (
	"strings"
	"testing"
)

// testRules is a small public rule set:
//   - "assume": axiom schema, any conclusion, no premises.
//   - "and":    from A and B derive "A&B" (premise order matters).
//   - "mp":     from "A>B" and A derive B.
func testRules(t *testing.T) *RuleSet {
	t.Helper()
	rs, err := NewRuleSet(
		Rule{Name: "assume", Arity: 0, Check: func(string, []string) bool { return true }},
		Rule{Name: "and", Arity: 2, Check: func(concl string, prems []string) bool {
			return concl == prems[0]+"&"+prems[1]
		}},
		Rule{Name: "mp", Arity: 2, Check: func(concl string, prems []string) bool {
			return prems[0] == prems[1]+">"+concl
		}},
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	return rs
}

func mustApply(t *testing.T, b *Builder, rule, concl string, prems ...*Node) *Node {
	t.Helper()
	n, err := b.Apply(rule, concl, prems...)
	if err != nil {
		t.Fatalf("Apply(%q, %q): %v", rule, concl, err)
	}
	return n
}

func TestIdenticalDerivationsAreShared(t *testing.T) {
	b := NewBuilder(testRules(t))
	a1 := mustApply(t, b, "assume", "A")
	a2 := mustApply(t, b, "assume", "A")
	if a1 != a2 {
		t.Fatal("identical axioms must be the same node")
	}
	b1 := mustApply(t, b, "assume", "B")
	c1 := mustApply(t, b, "and", "A&B", a1, b1)
	c2 := mustApply(t, b, "and", "A&B", a2, b1)
	if c1 != c2 {
		t.Fatal("identical derivations must be shared")
	}
}

func TestSameConclusionDifferentPremisesNotMerged(t *testing.T) {
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	bb := mustApply(t, b, "assume", "B")
	left := mustApply(t, b, "and", "A&B", a, bb)
	// Same conclusion, but derived from premises in the other order.
	right := mustApply(t, b, "and", "A&B", bb, a)
	if left == right {
		t.Fatal("nodes with different premise order must not be merged")
	}
	// Same conclusion via a different rule must not merge either.
	other := mustApply(t, b, "assume", "A&B")
	if other == left || other == right {
		t.Fatal("nodes with different rules must not be merged")
	}
}

func TestPremiseOrderDeterminesIdentity(t *testing.T) {
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	bb := mustApply(t, b, "assume", "B")
	ab := mustApply(t, b, "and", "A&B", a, bb)
	ba := mustApply(t, b, "and", "B&A", bb, a)
	if ab == ba {
		t.Fatal("swapped premises must yield distinct nodes")
	}
	if Equal(ab, ba) {
		t.Fatal("expanded trees with swapped premises must differ")
	}
}

func TestVerifyAcceptsValidDerivation(t *testing.T) {
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	imp := mustApply(t, b, "assume", "A>B")
	res := mustApply(t, b, "mp", "B", imp, a)
	if err := b.Verify(res); err != nil {
		t.Fatalf("Verify rejected a valid derivation: %v", err)
	}
}

func TestVerifyRejectsUnderivableConclusion(t *testing.T) {
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	imp := mustApply(t, b, "assume", "A>B")
	// "C" does not follow from "A>B" and "A".
	bad := mustApply(t, b, "mp", "C", imp, a)
	err := b.Verify(bad)
	if err == nil || !strings.Contains(err.Error(), "not derivable") {
		t.Fatalf("expected underivable-conclusion error, got %v", err)
	}
}

func TestVerifyRejectsUnknownRuleAndBadArity(t *testing.T) {
	rs := testRules(t)
	b := NewBuilder(rs)
	a := mustApply(t, b, "assume", "A")
	unknown := mustApply(t, b, "magic", "A", a)
	if err := b.Verify(unknown); err == nil || !strings.Contains(err.Error(), "unknown rule") {
		t.Fatalf("expected unknown-rule error, got %v", err)
	}
	wrongArity := mustApply(t, b, "and", "A&A", a)
	if err := b.Verify(wrongArity); err == nil || !strings.Contains(err.Error(), "expects 2 premises") {
		t.Fatalf("expected arity error, got %v", err)
	}
}

func TestVerifyRejectsCycle(t *testing.T) {
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	bb := mustApply(t, b, "assume", "B")
	x := mustApply(t, b, "and", "A&B", a, bb)
	y := mustApply(t, b, "and", "B&A", bb, a)
	// Cycle injection: make x and y premises of each other. The builder
	// cannot produce this; corrupt the graph directly to prove Verify
	// rejects cyclic inputs instead of looping forever.
	x.premises = append(x.premises[:0], y)
	y.premises = append(y.premises[:0], x)
	err := b.Verify(x)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	// Self-loop must be rejected too.
	z := mustApply(t, b, "assume", "Z")
	z.premises = []*Node{z}
	if err := b.Verify(z); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected self-loop cycle error, got %v", err)
	}
}

func TestExpandedTreeComparison(t *testing.T) {
	build := func() *Node {
		b := NewBuilder(testRules(t))
		a := mustApply(t, b, "assume", "A")
		bb := mustApply(t, b, "assume", "B")
		conj := mustApply(t, b, "and", "A&B", a, bb)
		imp := mustApply(t, b, "assume", "A&B>R")
		return mustApply(t, b, "mp", "R", imp, conj)
	}
	// Two independent builders produce distinct graphs with no shared
	// pointers, yet their expanded trees must be identical.
	n1, n2 := build(), build()
	if n1 == n2 {
		t.Fatal("different builders must not share nodes")
	}
	if !Equal(n1, n2) {
		t.Fatal("expanded trees of identical derivations must be equal")
	}
	if !Expand(n1).Equal(Expand(n2)) {
		t.Fatal("Expand results must compare equal")
	}

	// A single differing leaf deep in the tree must change the expansion.
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	bb := mustApply(t, b, "assume", "B")
	conj := mustApply(t, b, "and", "A&B", a, bb)
	imp := mustApply(t, b, "assume", "A&B>Q")
	other := mustApply(t, b, "mp", "Q", imp, conj)
	if Equal(n1, other) {
		t.Fatal("expanded trees with different conclusions must differ")
	}
}

func TestExpandDuplicatesSharedSubgraphs(t *testing.T) {
	b := NewBuilder(testRules(t))
	a := mustApply(t, b, "assume", "A")
	dup := mustApply(t, b, "and", "A&A", a, a)
	if dup.premises[0] != dup.premises[1] {
		t.Fatal("graph must share the repeated premise")
	}
	tree := Expand(dup)
	if len(tree.Premises) != 2 {
		t.Fatalf("expanded tree must duplicate shared premises, got %d", len(tree.Premises))
	}
	if tree.Premises[0] == tree.Premises[1] {
		t.Fatal("expanded tree must not share pointers")
	}
	if !tree.Premises[0].Equal(tree.Premises[1]) {
		t.Fatal("duplicated expansions must be equal")
	}
}

func TestCrossBuilderPremiseRejected(t *testing.T) {
	rs := testRules(t)
	b1, b2 := NewBuilder(rs), NewBuilder(rs)
	a := mustApply(t, b1, "assume", "A")
	if _, err := b2.Apply("and", "A&A", a, a); err == nil {
		t.Fatal("premises from another builder must be rejected")
	}
}

func TestNewRuleSetValidation(t *testing.T) {
	if _, err := NewRuleSet(Rule{Name: "x", Check: func(string, []string) bool { return true }},
		Rule{Name: "x", Check: func(string, []string) bool { return true }}); err == nil {
		t.Fatal("duplicate rule names must be rejected")
	}
	if _, err := NewRuleSet(Rule{Name: "x"}); err == nil {
		t.Fatal("missing Check must be rejected")
	}
}
