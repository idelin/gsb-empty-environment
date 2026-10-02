package earley

import (
	"testing"
)

func arithmeticGrammar() *Grammar {
	return NewGrammar("E",
		R("E", "E", "+", "E"),
		R("E", "E", "*", "E"),
		R("E", "id"),
	)
}

func arithmeticTokens() []Token {
	return []Token{
		{Type: "id", Value: "a"},
		{Type: "+", Value: "+"},
		{Type: "id", Value: "b"},
		{Type: "*", Value: "*"},
		{Type: "id", Value: "c"},
	}
}

// Acceptance boundary: E -> E + E | E * E | id on "a + b * c" must
// produce exactly the two legal parse trees.
func TestArithmeticAmbiguity(t *testing.T) {
	chart := arithmeticGrammar().Parse(arithmeticTokens())
	if !chart.Accepted() {
		t.Fatal("input not accepted")
	}
	trees := chart.ParseTrees()
	if len(trees) != 2 {
		t.Fatalf("got %d trees, want 2", len(trees))
	}
	want := map[string]bool{
		"(E (E a) + (E (E b) * (E c)))": true, // a + (b * c)
		"(E (E (E a) + (E b)) * (E c))": true, // (a + b) * c
	}
	for _, tr := range trees {
		if !want[tr.String()] {
			t.Errorf("unexpected tree %s", tr.String())
		}
	}
}

// Disambiguation by precedence (* > +) and left associativity must keep
// exactly a + (b * c).
func TestArithmeticDisambiguation(t *testing.T) {
	trees := arithmeticGrammar().Parse(arithmeticTokens()).ParseTrees()
	kept := Disambiguate(trees, map[string]int{"+": 1, "*": 2})
	if len(kept) != 1 {
		t.Fatalf("got %d disambiguated trees, want 1", len(kept))
	}
	if got, want := kept[0].String(), "(E (E a) + (E (E b) * (E c)))"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// Left associativity: "a + b + c" must disambiguate to (a + b) + c.
func TestLeftAssociativity(t *testing.T) {
	tokens := []Token{
		{Type: "id", Value: "a"}, {Type: "+", Value: "+"},
		{Type: "id", Value: "b"}, {Type: "+", Value: "+"},
		{Type: "id", Value: "c"},
	}
	trees := arithmeticGrammar().Parse(tokens).ParseTrees()
	if len(trees) != 2 {
		t.Fatalf("got %d trees, want 2", len(trees))
	}
	kept := Disambiguate(trees, map[string]int{"+": 1, "*": 2})
	if len(kept) != 1 {
		t.Fatalf("got %d disambiguated trees, want 1", len(kept))
	}
	if got, want := kept[0].String(), "(E (E (E a) + (E b)) + (E c))"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// Epsilon productions: S -> A A, A -> "a" | ε. The input "a" has two
// parses (a ε and ε a); the empty input has exactly one.
func TestEpsilonProductions(t *testing.T) {
	g := NewGrammar("S",
		R("S", "A", "A"),
		R("A", "a"),
		R("A"), // epsilon
	)

	chart := g.Parse([]Token{{Type: "a", Value: "a"}})
	if !chart.Accepted() {
		t.Fatal(`"a" not accepted`)
	}
	if trees := chart.ParseTrees(); len(trees) != 2 {
		t.Fatalf(`"a": got %d trees, want 2`, len(trees))
	}

	empty := g.Parse(nil)
	if !empty.Accepted() {
		t.Fatal("empty input not accepted")
	}
	trees := empty.ParseTrees()
	if len(trees) != 1 {
		t.Fatalf("empty input: got %d trees, want 1", len(trees))
	}
	if got, want := trees[0].String(), "(S (A) (A))"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// Highly ambiguous left- and right-recursive grammar S -> S S | "a":
// "a a a" has the two Catalan parses.
func TestAmbiguousSS(t *testing.T) {
	g := NewGrammar("S",
		R("S", "S", "S"),
		R("S", "a"),
	)
	tokens := []Token{
		{Type: "a", Value: "a"}, {Type: "a", Value: "a"}, {Type: "a", Value: "a"},
	}
	chart := g.Parse(tokens)
	if !chart.Accepted() {
		t.Fatal("input not accepted")
	}
	trees := chart.ParseTrees()
	if len(trees) != 2 {
		t.Fatalf("got %d trees, want 2", len(trees))
	}
	want := map[string]bool{
		"(S (S (S a) (S a)) (S a))": true,
		"(S (S a) (S (S a) (S a)))": true,
	}
	for _, tr := range trees {
		if !want[tr.String()] {
			t.Errorf("unexpected tree %s", tr.String())
		}
	}
}

// A nullable non-terminal in the middle of a rule: S -> A "x" B,
// A -> "a" | ε, B -> "b" | ε.
func TestNullableInRuleBody(t *testing.T) {
	g := NewGrammar("S",
		R("S", "A", "x", "B"),
		R("A", "a"),
		R("A"),
		R("B", "b"),
		R("B"),
	)
	for _, input := range [][]Token{
		{{Type: "a", Value: "a"}, {Type: "x", Value: "x"}, {Type: "b", Value: "b"}},
		{{Type: "x", Value: "x"}},
		{{Type: "a", Value: "a"}, {Type: "x", Value: "x"}},
		{{Type: "x", Value: "x"}, {Type: "b", Value: "b"}},
	} {
		if !g.Parse(input).Accepted() {
			t.Errorf("input %v not accepted", input)
		}
	}
	if g.Parse([]Token{{Type: "a", Value: "a"}}).Accepted() {
		t.Error("input without x must be rejected")
	}
}

// The chart must be fully populated: every position exists and the seed
// item sits in position 0.
func TestChartContents(t *testing.T) {
	tokens := arithmeticTokens()
	chart := arithmeticGrammar().Parse(tokens)
	if len(chart.Positions) != len(tokens)+1 {
		t.Fatalf("got %d positions, want %d", len(chart.Positions), len(tokens)+1)
	}
	for i, pos := range chart.Positions {
		if len(pos.states) == 0 {
			t.Errorf("position %d is empty", i)
		}
	}
	first := chart.Positions[0].states[0]
	if first.rule != 0 || first.dot != 0 || first.origin != 0 {
		t.Errorf("unexpected seed item %v", first)
	}
}

// Rejection: a token the grammar cannot scan must fail.
func TestRejection(t *testing.T) {
	bad := []Token{
		{Type: "id", Value: "a"}, {Type: "+", Value: "+"}, {Type: "+", Value: "+"},
	}
	if arithmeticGrammar().Parse(bad).Accepted() {
		t.Error("invalid input accepted")
	}
	if trees := arithmeticGrammar().Parse(bad).ParseTrees(); len(trees) != 0 {
		t.Errorf("rejected input produced %d trees", len(trees))
	}
}
