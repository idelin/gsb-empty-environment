package uniparse

import (
	"testing"
)

const agreement = `
start S
S[num=N] -> NP[num=N] VP[num=N]
NP[num=N] -> Det N[num=N]
Det -> "the"
N[num=sg] -> "dog"
N[num=pl] -> "dogs"
VP[num=N] -> V[num=N]
V[num=sg] -> "barks"
V[num=pl] -> "bark"
`

func mustParse(t *testing.T, src string, words ...string) *Forest {
	t.Helper()
	f, err := ParseText(src, words)
	if err != nil {
		t.Fatalf("ParseText(%q): %v", words, err)
	}
	return f
}

func TestAgreement(t *testing.T) {
	f := mustParse(t, agreement, "the", "dog", "barks")
	if len(f.Roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(f.Roots))
	}
	if got := f.Roots[0].Sym.Feats["num"]; got != Const("sg") {
		t.Fatalf("root num = %v, want sg", got)
	}
	if n, err := f.Verify(10); err != nil || n != 1 {
		t.Fatalf("Verify = %d, %v; want 1, nil", n, err)
	}

	f = mustParse(t, agreement, "the", "dogs", "bark")
	if len(f.Roots) != 1 || f.Roots[0].Sym.Feats["num"] != Const("pl") {
		t.Fatalf("plural: roots=%d feats=%v", len(f.Roots), f.Roots[0].Sym.Feats)
	}

	for _, words := range [][]string{
		{"the", "dog", "bark"},
		{"the", "dogs", "barks"},
	} {
		if f := mustParse(t, agreement, words...); len(f.Roots) != 0 {
			t.Fatalf("%v: got %d roots, want 0 (agreement mismatch)", words, len(f.Roots))
		}
	}
}

func TestUnifyFeats(t *testing.T) {
	if _, ok := unifyFeats(subst{}, Feats{"a": Const("x")}, Feats{"a": Const("y")}); ok {
		t.Fatal("conflicting constants on duplicate key must fail")
	}
	if _, ok := unifyFeats(subst{}, Feats{"a": Const("x")}, Feats{"b": Const("y")}); !ok {
		t.Fatal("one-sided keys must be unconstrained")
	}
	s, ok := unifyValue(subst{}, Var("a"), Var("b"))
	if !ok {
		t.Fatal("var-var unification failed")
	}
	if s, ok = unifyValue(s, Var("b"), Var("c")); !ok {
		t.Fatal("var chain failed")
	}
	if s, ok = unifyValue(s, Var("c"), Const("k")); !ok {
		t.Fatal("var-const failed")
	}
	if got := s.resolve(Var("a")); got != Const("k") {
		t.Fatalf("chain resolves to %v, want k", got)
	}
}

func TestNoCrossBranchPollution(t *testing.T) {
	base := subst{}
	s1, ok1 := unifyValue(base, Var("v"), Const("one"))
	s2, ok2 := unifyValue(base, Var("v"), Const("two"))
	if !ok1 || !ok2 {
		t.Fatal("sibling unifications should both succeed")
	}
	if len(base) != 0 {
		t.Fatal("input substitution was mutated")
	}
	if s1.resolve(Var("v")) != Const("one") || s2.resolve(Var("v")) != Const("two") {
		t.Fatal("sibling substitutions polluted each other")
	}
}

func TestCanonicalSeq(t *testing.T) {
	a := canonicalSeq(subst{}, Feats{"p": Var("X"), "q": Var("X")})[0]
	b := canonicalSeq(subst{}, Feats{"p": Var("Y"), "q": Var("Y")})[0]
	if featsKey(a) != featsKey(b) {
		t.Fatalf("alpha-equivalent maps differ: %v vs %v", a, b)
	}
	c := canonicalSeq(subst{}, Feats{"p": Var("X"), "q": Var("Z")})[0]
	if featsKey(a) == featsKey(c) {
		t.Fatal("distinct aliasing patterns must differ")
	}
	s, _ := unifyValue(subst{}, Var("X"), Var("Y"))
	d := canonicalSeq(s, Feats{"p": Var("X"), "q": Var("Y")})[0]
	if featsKey(d) != featsKey(a) {
		t.Fatalf("subst-aliased vars canonicalize to %v, want %v", d, a)
	}
}

// Two expansions of the same rule must rename variables independently;
// the shared variable inside S forces agreement between the two X's.
func TestFreshRenamingSharedVar(t *testing.T) {
	const xx = `
start S
S[num=N] -> X[num=N] X[num=N]
X[num=sg] -> "a"
X[num=pl] -> "a"
`
	f := mustParse(t, xx, "a", "a")
	if len(f.Roots) != 2 {
		t.Fatalf("got %d roots, want 2 (sg and pl)", len(f.Roots))
	}
	seen := map[Value]bool{}
	for _, r := range f.Roots {
		seen[r.Sym.Feats["num"]] = true
	}
	if !seen[Const("sg")] || !seen[Const("pl")] {
		t.Fatalf("root feats = %v, want sg and pl", seen)
	}
	if n, err := f.Verify(10); err != nil || n != 2 {
		t.Fatalf("Verify = %d, %v; want 2, nil", n, err)
	}
}

// Sibling branches bind the same rule-local variable differently; with
// shared mutable state the second branch would corrupt the first.
func TestSiblingIsolation(t *testing.T) {
	const sib = `
start S
S -> A B
A -> X[num=N]
B -> X[num=M]
X[num=sg] -> "a"
X[num=pl] -> "a"
`
	f := mustParse(t, sib, "a", "a")
	if len(f.Roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(f.Roots))
	}
	if n, err := f.Verify(16); err != nil || n != 4 {
		t.Fatalf("Verify = %d, %v; want 4, nil", n, err)
	}
}

func TestRecursionCatalan(t *testing.T) {
	const cat = `
start S
S -> "a"
S -> S S
`
	f := mustParse(t, cat, "a", "a", "a", "a")
	if len(f.Roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(f.Roots))
	}
	if n, err := f.Verify(100); err != nil || n != 5 {
		t.Fatalf("Verify = %d, %v; want 5 (Catalan), nil", n, err)
	}
	if got := len(f.Nodes()); got != 10 {
		t.Fatalf("forest has %d nodes, want 10 (all spans shared)", got)
	}
}

func TestLeftRecursionAmbiguity(t *testing.T) {
	const pp = `
start S
S -> NP VP
NP -> "i"
NP -> Det N
NP -> NP PP
PP -> P NP
VP -> V NP
VP -> VP PP
Det -> "the"
N -> "dog"
N -> "park"
P -> "near"
V -> "saw"
`
	f := mustParse(t, pp, "i", "saw", "the", "dog", "near", "the", "park")
	if len(f.Roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(f.Roots))
	}
	n, err := f.Verify(16)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n != 2 {
		t.Fatalf("got %d trees, want 2 (PP attachment)", n)
	}
}

// Unit-rule cycles make the forest cyclic; expansion must still terminate
// and enumerate trees by increasing depth.
func TestUnitRuleCycle(t *testing.T) {
	const cyc = `
start S
S -> NP
NP -> S
S -> "a"
`
	f := mustParse(t, cyc, "a")
	if len(f.Roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(f.Roots))
	}
	trees := f.Trees(3)
	if len(trees) != 3 {
		t.Fatalf("got %d trees, want 3", len(trees))
	}
	for _, tr := range trees {
		if y := tr.Yield(); len(y) != 1 || y[0] != "a" {
			t.Fatalf("yield = %v, want [a]", y)
		}
	}
	if n, err := f.Verify(3); err != nil || n != 3 {
		t.Fatalf("Verify = %d, %v; want 3, nil", n, err)
	}
}

// Derivations whose features are alpha-equivalent share one node; the
// aliasing pattern (not variable names) determines identity.
func TestCanonicalDedup(t *testing.T) {
	const aliased = `
start S
S[p=A, q=A] -> NP[p=A, q=A]
NP[p=X, q=X] -> "a"
NP[p=Y, q=Y] -> "a"
`
	f := mustParse(t, aliased, "a")
	if len(f.Roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(f.Roots))
	}
	feats := f.Roots[0].Sym.Feats
	if feats["p"] != Var("v1") || feats["q"] != Var("v1") {
		t.Fatalf("root feats = %v, want {p:v1 q:v1}", feats)
	}
	var nps []*Node
	for _, n := range f.Nodes() {
		if n.Sym.Name == "NP" {
			nps = append(nps, n)
		}
	}
	if len(nps) != 1 || len(nps[0].Packings) != 2 {
		t.Fatalf("NP nodes = %d (packings %v), want 1 node with 2 packings", len(nps), nps)
	}
	if n, err := f.Verify(10); err != nil || n != 2 {
		t.Fatalf("Verify = %d, %v; want 2, nil", n, err)
	}
}

// A variable shared across two keys rejects constants that differ.
func TestAliasedKeyConflict(t *testing.T) {
	const clash = `
start S
S -> NP[p=A, q=A]
NP[p=sg, q=pl] -> "a"
`
	f := mustParse(t, clash, "a")
	if len(f.Roots) != 0 {
		t.Fatalf("got %d roots, want 0 (p and q cannot both be one variable)", len(f.Roots))
	}
}

func TestLimits(t *testing.T) {
	g := &Grammar{}
	for i := 0; i < MaxRules+1; i++ {
		g.Rules = append(g.Rules, Rule{LHS: NT("A", nil), RHS: []Symbol{T("a")}})
	}
	if _, err := Parse(g, []string{"a"}); err == nil {
		t.Fatal("want error for > MaxRules rules")
	}
	if _, err := Parse(&Grammar{Rules: []Rule{{LHS: NT("A", nil)}}}, []string{"a"}); err == nil {
		t.Fatal("want error for epsilon production")
	}
	g3 := &Grammar{Rules: []Rule{{LHS: NT("A", nil), RHS: []Symbol{T("a")}}}}
	words := make([]string, MaxWords+1)
	for i := range words {
		words[i] = "a"
	}
	if _, err := Parse(g3, words); err == nil {
		t.Fatal("want error for > MaxWords words")
	}
	g4 := &Grammar{Rules: []Rule{{LHS: T("a"), RHS: []Symbol{T("a")}}}}
	if _, err := Parse(g4, []string{"a"}); err == nil {
		t.Fatal("want error for terminal LHS")
	}
	if _, err := Parse(g3, nil); err == nil {
		t.Fatal("want error for empty input")
	}
}

func TestTextFormat(t *testing.T) {
	f := mustParse(t, "start S\nS -> hello world\n", "hello", "world")
	if len(f.Roots) != 1 {
		t.Fatalf("bare words: got %d roots, want 1", len(f.Roots))
	}
	if n, err := f.Verify(4); err != nil || n != 1 {
		t.Fatalf("Verify = %d, %v; want 1, nil", n, err)
	}

	bad := []string{
		"S ->",                         // epsilon
		"S -> x[num=sg]",               // features on terminal
		"\"s\" -> \"a\"",               // terminal LHS
		"start X\nS -> \"a\"",          // start heads no rule
		"start S\nstart S\nS -> \"a\"", // duplicate start
		"S -> NP[num=]",                // missing value
		"S -> \"a",                     // unterminated string
	}
	for _, src := range bad {
		if _, err := ParseGrammar(src); err == nil {
			t.Fatalf("ParseGrammar(%q): want error", src)
		}
	}
}

func TestVerifyTampered(t *testing.T) {
	f := mustParse(t, agreement, "the", "dog", "barks")
	trees := f.Trees(1)
	if len(trees) != 1 {
		t.Fatalf("got %d trees, want 1", len(trees))
	}
	tree := trees[0]
	if err := VerifyTree(tree, f.Words); err != nil {
		t.Fatalf("honest tree rejected: %v", err)
	}
	if err := VerifyTree(tree, []string{"the", "dog", "bark"}); err == nil {
		t.Fatal("want yield mismatch error")
	}
	bad := *tree
	bad.Sym.Feats = Feats{"num": Const("pl")}
	if err := VerifyTree(&bad, f.Words); err == nil {
		t.Fatal("want feature inconsistency error for tampered root")
	}
	// Tamper with a leaf word.
	var leaves []*Tree
	var walk func(tr *Tree)
	walk = func(tr *Tree) {
		if tr.Rule == nil {
			leaves = append(leaves, tr)
			return
		}
		for _, c := range tr.Children {
			walk(c)
		}
	}
	walk(tree)
	leaves[1].Sym.Name = "cat"
	if err := VerifyTree(tree, f.Words); err == nil {
		t.Fatal("want error for tampered leaf")
	}
}

func TestNoParse(t *testing.T) {
	f := mustParse(t, agreement, "the", "cat", "barks")
	if len(f.Roots) != 0 {
		t.Fatalf("got %d roots, want 0 (unknown word)", len(f.Roots))
	}
	if n, err := f.Verify(4); err != nil || n != 0 {
		t.Fatalf("Verify = %d, %v; want 0, nil", n, err)
	}
}

// A recursive grammar with features must still terminate: canonical
// signatures keep the chart finite, and expansion stays bounded.
func TestRecursiveFeaturedGrammarTerminates(t *testing.T) {
	const rec = `
start S
S[num=N]  -> S[num=N] S[num=N]
S[num=sg] -> "a"
S[num=pl] -> "a"
`
	words := make([]string, MaxWords)
	for i := range words {
		words[i] = "a"
	}
	f := mustParse(t, rec, words...)
	if len(f.Roots) == 0 {
		t.Fatal("want roots")
	}
	n, err := f.Verify(8)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n == 0 {
		t.Fatal("want trees")
	}
}
