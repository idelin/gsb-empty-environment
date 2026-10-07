package fparse

import (
	"strings"
	"testing"
)

func mustGrammar(t *testing.T, src string) *Grammar {
	t.Helper()
	g, err := ParseGrammar(src)
	if err != nil {
		t.Fatalf("ParseGrammar: %v", err)
	}
	return g
}

func mustForest(t *testing.T, g *Grammar, words ...string) *Forest {
	t.Helper()
	f, err := g.ParseForest(words, "S")
	if err != nil {
		t.Fatalf("ParseForest: %v", err)
	}
	return f
}

func TestSimpleParse(t *testing.T) {
	g := mustGrammar(t, `
S -> NP VP
NP -> Det N
Det -> "the"
N -> "dog"
VP -> "sleeps"
`)
	f := mustForest(t, g, "the", "dog", "sleeps")
	if len(f.Roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(f.Roots))
	}
	trees := f.Trees(0)
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	if got := strings.Join(trees[0].Yield(), " "); got != "the dog sleeps" {
		t.Fatalf("yield = %q", got)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestAgreementAndDuplicateKeyConflict(t *testing.T) {
	g := mustGrammar(t, `
S[num=N] -> NP[num=N] VP[num=N]
NP[num=sg] -> "dog"
NP[num=pl] -> "dogs"
VP[num=sg] -> "barks"
VP[num=pl] -> "bark"
`)
	// "dogs bark": num=pl unifies through the shared variable N.
	f := mustForest(t, g, "dogs", "bark")
	trees := f.Trees(0)
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	if got := trees[0].Features["num"]; got != ConstVal("pl") {
		t.Fatalf("root num = %v, want pl", got)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// "dogs bark": pl vs sg on the duplicate key num must fail.
	f = mustForest(t, g, "dogs", "barks")
	if len(f.Roots) != 0 || f.NumAnalyses(100) != 0 {
		t.Fatalf("conflicting agreement should not parse")
	}
}

func TestFreshVariablesPerExpansion(t *testing.T) {
	// The same lexical rule is used twice in one parse; each use must
	// rename its variable, otherwise sg and pl would clash.
	g := mustGrammar(t, `
S -> NP NP
NP[num=N] -> D N[num=N]
D -> "a"
N[num=sg] -> "dog"
N[num=pl] -> "cats"
`)
	f := mustForest(t, g, "a", "dog", "a", "cats")
	trees := f.Trees(0)
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	nps := trees[0].Children
	if nps[0].Features["num"] != ConstVal("sg") || nps[1].Features["num"] != ConstVal("pl") {
		t.Fatalf("NP features = %v %v, want [num=sg] [num=pl]", nps[0].Features, nps[1].Features)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestNoCrossBranchPollution(t *testing.T) {
	// "dog" is ambiguous between sg and pl. The failing pl branch must
	// not pollute the substitution of the succeeding sg branch.
	g := mustGrammar(t, `
S[num=N] -> NP[num=N] VP[num=N]
NP[num=N] -> D N[num=N]
D -> "a"
N[num=sg] -> "dog"
N[num=pl] -> "dog"
VP[num=sg] -> "barks"
`)
	f := mustForest(t, g, "a", "dog", "barks")
	trees := f.Trees(0)
	if len(trees) != 1 {
		t.Fatalf("want exactly 1 tree, got %d", len(trees))
	}
	if got := trees[0].Features["num"]; got != ConstVal("sg") {
		t.Fatalf("root num = %v, want sg", got)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSharedForestAmbiguity(t *testing.T) {
	g := mustGrammar(t, `
S -> NP VP
NP -> "I"
NP -> Det N
NP -> NP PP
VP -> V NP
VP -> VP PP
PP -> P NP
Det -> "the"
N -> "man"
N -> "telescope"
V -> "saw"
P -> "with"
`)
	f := mustForest(t, g, "I", "saw", "the", "man", "with", "the", "telescope")
	if len(f.Roots) != 1 {
		t.Fatalf("want 1 packed root, got %d", len(f.Roots))
	}
	if n := f.NumAnalyses(100); n != 2 {
		t.Fatalf("want 2 analyses (PP attachment), got %d", n)
	}
	trees := f.Trees(0)
	if len(trees) != 2 {
		t.Fatalf("want 2 trees, got %d", len(trees))
	}
	// The forest must share the PP subtree between both analyses:
	// exactly one PP node exists in the whole packed forest.
	ppCount := 0
	var ppNode *Node
	var walk func(n *Node)
	seen := map[*Node]bool{}
	walk = func(n *Node) {
		if seen[n] {
			return
		}
		seen[n] = true
		if n.Cat == "PP" {
			ppCount++
			ppNode = n
		}
		for _, a := range n.Alts {
			for _, c := range a.Children {
				walk(c)
			}
		}
	}
	walk(f.Roots[0])
	if ppCount != 1 || ppNode == nil {
		t.Fatalf("PP node should be shared (found %d)", ppCount)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRecursion(t *testing.T) {
	g := mustGrammar(t, `
S -> NP VP
NP -> Det N
NP -> NP PP
VP -> V NP
PP -> P NP
Det -> "the"
N -> "man"
N -> "cat"
N -> "dog"
V -> "saw"
P -> "with"
`)
	f := mustForest(t, g, "the", "man", "with", "the", "cat", "with", "the", "dog", "saw", "the", "cat")
	// Left-recursive NP -> NP PP must yield nested analyses.
	if n := f.NumAnalyses(1000); n < 2 {
		t.Fatalf("want multiple recursive analyses, got %d", n)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestEquivalentSubstitutionDedup(t *testing.T) {
	// Two different derivations of A whose substitutions differ only by
	// variable numbering must collapse into one packed node with two
	// alternatives.
	g := mustGrammar(t, `
S -> A
A[f=X] -> B[f=X]
A[f=Y] -> C[f=Y]
B[f=Z] -> "b"
C[g=W] -> "b"
`)
	f := mustForest(t, g, "b")
	if len(f.Roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(f.Roots))
	}
	aNode := f.Roots[0].Alts[0].Children[0]
	if aNode.Cat != "A" {
		t.Fatalf("child = %s, want A", aNode.Cat)
	}
	if len(aNode.Alts) != 2 {
		t.Fatalf("want 2 packed alternatives under one node, got %d", len(aNode.Alts))
	}
	if got := aNode.Features["f"]; !got.IsVar {
		t.Fatalf("A.f should stay a (canonical) variable, got %v", got)
	}
	if n := f.NumAnalyses(100); n != 2 {
		t.Fatalf("want 2 analyses, got %d", n)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestIdenticalTreesDedup(t *testing.T) {
	// Duplicate rules yield alpha-equivalent trees: expansion dedups them.
	g := mustGrammar(t, `
S -> A
A -> "x"
A -> "x"
`)
	f := mustForest(t, g, "x")
	if n := f.NumAnalyses(100); n != 2 {
		t.Fatalf("want 2 packed analyses, got %d", n)
	}
	if trees := f.Trees(0); len(trees) != 1 {
		t.Fatalf("equivalent trees should dedup to 1, got %d", len(trees))
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestUnaryChainFixpoint(t *testing.T) {
	g := mustGrammar(t, `
S -> A
A -> B
B -> C
C -> "x"
`)
	f := mustForest(t, g, "x")
	if len(f.Trees(0)) != 1 {
		t.Fatalf("unary chain should parse")
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestLimits(t *testing.T) {
	if _, err := ParseGrammar(`S ->`); err == nil {
		t.Fatal("epsilon production should be rejected")
	}
	var many strings.Builder
	for i := 0; i < MaxRules+1; i++ {
		many.WriteString("S -> \"x\"\n")
	}
	if _, err := ParseGrammar(many.String()); err == nil {
		t.Fatal("more than 20 rules should be rejected")
	}
	g := mustGrammar(t, `S -> "x"`)
	words := make([]string, MaxWords+1)
	for i := range words {
		words[i] = "x"
	}
	if _, err := g.ParseForest(words, "S"); err == nil {
		t.Fatal("more than 20 words should be rejected")
	}
	if _, err := g.ParseForest(nil, "S"); err == nil {
		t.Fatal("empty input should be rejected")
	}
}

func TestGrammarSyntaxErrors(t *testing.T) {
	for _, src := range []string{
		`S NP VP`,           // missing arrow
		`"s" -> "x"`,        // terminal LHS
		`S -> "x`,           // unterminated string
		`S[x=1] -> "x"`,     // value must be ident
		`S[x=a x=b] -> "x"`, // duplicate key in one map
	} {
		if _, err := ParseGrammar(src); err == nil {
			t.Fatalf("expected error for %q", src)
		}
	}
}

func TestFeaturePercolationEndToEnd(t *testing.T) {
	g := mustGrammar(t, `
S[mode=M num=N] -> NP[case=nom num=N] VP[mode=M num=N]
NP[case=C num=sg] -> "he"
NP[case=C num=pl] -> "they"
VP[mode=ind num=sg] -> "runs"
VP[mode=ind num=pl] -> "run"
`)
	f := mustForest(t, g, "they", "run")
	trees := f.Trees(0)
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	root := trees[0]
	if root.Features["mode"] != ConstVal("ind") || root.Features["num"] != ConstVal("pl") {
		t.Fatalf("root features = %v, want [mode=ind num=pl]", root.Features)
	}
	if err := f.Verify(0); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, bad := range [][]string{{"they", "runs"}, {"he", "run"}} {
		if f := mustForest(t, g, bad...); len(f.Roots) != 0 {
			t.Fatalf("%v should not parse", bad)
		}
	}
}
