package main

import "testing"

func parseArithmetic(input ...string) *Chart {
	return Parse(arithmeticGrammar(), input)
}

// Acceptance criterion: E -> E + E | E * E | id on "a + b * c"
// must yield exactly 2 legal parse trees.
func TestArithmeticAmbiguity(t *testing.T) {
	chart := parseArithmetic("a", "+", "b", "*", "c")
	if !chart.Accepted() {
		t.Fatal("input should be accepted")
	}
	trees := chart.Trees()
	if len(trees) != 2 {
		t.Fatalf("want 2 parse trees, got %d", len(trees))
	}
	seen := map[string]bool{}
	for _, tr := range trees {
		seen[tr.String()] = true
	}
	for _, want := range []string{
		"(E (E id[a]) + (E (E id[b]) * (E id[c])))",
		"(E (E (E id[a]) + (E id[b])) * (E id[c]))",
	} {
		if !seen[want] {
			t.Errorf("missing expected tree %s", want)
		}
	}
}

// Acceptance criterion: precedence/associativity disambiguation keeps
// exactly the tree where '*' binds tighter than '+'.
func TestPrecedenceDisambiguation(t *testing.T) {
	trees := parseArithmetic("a", "+", "b", "*", "c").Trees()
	filtered := exprPrecedence.Filter(trees)
	if len(filtered) != 1 {
		t.Fatalf("want 1 disambiguated tree, got %d", len(filtered))
	}
	want := "(E (E id[a]) + (E (E id[b]) * (E id[c])))"
	if got := filtered[0].String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Left associativity: "a + b + c" must resolve to (a+b)+c.
func TestLeftAssociativity(t *testing.T) {
	trees := parseArithmetic("a", "+", "b", "+", "c").Trees()
	if len(trees) != 2 {
		t.Fatalf("want 2 parse trees, got %d", len(trees))
	}
	filtered := exprPrecedence.Filter(trees)
	if len(filtered) != 1 {
		t.Fatalf("want 1 disambiguated tree, got %d", len(filtered))
	}
	want := "(E (E (E id[a]) + (E id[b])) + (E id[c]))"
	if got := filtered[0].String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Ambiguity grows with the Catalan numbers: 4 operands give 5 trees.
func TestAmbiguityIsEnumeratedFully(t *testing.T) {
	trees := parseArithmetic("a", "+", "b", "*", "c", "+", "d").Trees()
	if len(trees) != 5 {
		t.Fatalf("want 5 parse trees, got %d", len(trees))
	}
}

// Epsilon productions: S -> A B with A -> 'a' | <epsilon>, B -> 'b'.
func TestEpsilonProductions(t *testing.T) {
	g := NewGrammar("S",
		Rule{LHS: "S", RHS: []string{"A", "B"}},
		Rule{LHS: "A", RHS: []string{"a"}},
		Rule{LHS: "A", RHS: nil}, // epsilon
		Rule{LHS: "B", RHS: []string{"b"}},
	)

	chart := Parse(g, []string{"b"})
	if !chart.Accepted() {
		t.Fatal(`"b" should be accepted via A -> epsilon`)
	}
	trees := chart.Trees()
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	if got, want := trees[0].String(), "(S A (B b))"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	if !Parse(g, []string{"a", "b"}).Accepted() {
		t.Fatal(`"a b" should be accepted`)
	}
	if Parse(g, []string{"a", "a"}).Accepted() {
		t.Fatal(`"a a" should be rejected`)
	}
}

// Nullable non-terminals in the middle of a rule, plus an empty input.
func TestNullableAndEmptyInput(t *testing.T) {
	g := NewGrammar("S",
		Rule{LHS: "S", RHS: []string{"A", "A"}},
		Rule{LHS: "A", RHS: []string{"a"}},
		Rule{LHS: "A", RHS: nil},
	)
	if !Parse(g, nil).Accepted() {
		t.Fatal("empty input should be accepted (both A's are epsilon)")
	}
	trees := Parse(g, []string{"a"}).Trees()
	if len(trees) != 2 { // a from the first A, or from the second
		t.Fatalf("want 2 trees for ambiguous nullable grammar, got %d", len(trees))
	}
}

// Right recursion and immediate left recursion in one grammar.
func TestRecursion(t *testing.T) {
	g := NewGrammar("L",
		Rule{LHS: "L", RHS: []string{"L", ",", "id"}}, // left recursive
		Rule{LHS: "L", RHS: []string{"id"}},
	)
	g.MatchTerminal = tokenMatcher
	chart := Parse(g, []string{"a", ",", "b", ",", "c"})
	if !chart.Accepted() {
		t.Fatal("left-recursive list should be accepted")
	}
	if n := len(chart.Trees()); n != 1 {
		t.Fatalf("want 1 tree, got %d", n)
	}

	r := NewGrammar("R",
		Rule{LHS: "R", RHS: []string{"id", ",", "R"}}, // right recursive
		Rule{LHS: "R", RHS: []string{"id"}},
	)
	r.MatchTerminal = tokenMatcher
	if !Parse(r, []string{"a", ",", "b"}).Accepted() {
		t.Fatal("right-recursive list should be accepted")
	}
}

// Unparsable input must be rejected and yield no trees.
func TestRejection(t *testing.T) {
	chart := parseArithmetic("a", "+")
	if chart.Accepted() {
		t.Fatal(`"a +" should be rejected`)
	}
	if trees := chart.Trees(); trees != nil {
		t.Fatalf("rejected input should have no trees, got %d", len(trees))
	}
	if parseArithmetic("a", "b").Accepted() {
		t.Fatal(`"a b" should be rejected`)
	}
}

// The chart (parse graph) must record items in every state set.
func TestChartIsComplete(t *testing.T) {
	chart := parseArithmetic("a", "+", "b")
	if len(chart.sets) != 4 {
		t.Fatalf("want 4 state sets, got %d", len(chart.sets))
	}
	for i, set := range chart.sets {
		if len(set.items) == 0 {
			t.Errorf("chart[%d] is empty", i)
		}
	}
}
