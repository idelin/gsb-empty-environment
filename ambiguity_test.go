package ambiguity

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Grammar {
	t.Helper()
	g, err := ParseGrammar(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseGrammar(%q): %v", src, err)
	}
	return g
}

// checkWitness verifies the two returned trees independently of the
// forest: both must be valid parse trees of g, both must yield the
// reported string, and they must differ.
func checkWitness(t *testing.T, g *Grammar, res Result, want string) {
	t.Helper()
	if !res.Found {
		t.Fatalf("no ambiguous string found, want %q", want)
	}
	if got := JoinSymbols(res.String); got != want {
		t.Fatalf("found %q, want %q", got, want)
	}
	if len(res.Trees) != 2 {
		t.Fatalf("got %d witness trees, want 2", len(res.Trees))
	}
	if res.Trees[0].String() == res.Trees[1].String() {
		t.Fatalf("witness trees are identical: %s", res.Trees[0])
	}
	for i, tr := range res.Trees {
		if err := ValidateTree(g, tr); err != nil {
			t.Errorf("tree %d invalid: %v", i+1, err)
		}
		if got := JoinSymbols(tr.Yield()); got != want {
			t.Errorf("tree %d yields %q, want %q", i+1, got, want)
		}
	}
	if res.States <= 0 {
		t.Errorf("States = %d, want > 0", res.States)
	}
}

func TestClassicExpression(t *testing.T) {
	g := mustParse(t, "E -> E + E | E * E | ( E ) | a")
	res := FindAmbiguous(g, 5, -1)
	checkWitness(t, g, res, "a + a + a")
}

func TestLeftRecursiveAmbiguous(t *testing.T) {
	g := mustParse(t, "S -> S S | a")
	res := FindAmbiguous(g, 4, -1)
	checkWitness(t, g, res, "a a a")
}

func TestLeftRecursiveUnambiguous(t *testing.T) {
	g := mustParse(t, "S -> S a | b")
	res := FindAmbiguous(g, 4, -1)
	if res.Found {
		t.Fatalf("false positive on %q with trees %s and %s",
			JoinSymbols(res.String), res.Trees[0], res.Trees[1])
	}
}

func TestDuplicateRulesNotAmbiguous(t *testing.T) {
	// The parser collapses the duplicate A -> a: it builds the same tree.
	g := mustParse(t, "S -> A A\nA -> a\nA -> a")
	if len(g.Productions) != 2 {
		t.Fatalf("duplicate rule not collapsed: %d productions", len(g.Productions))
	}
	res := FindAmbiguous(g, 3, -1)
	if res.Found {
		t.Fatalf("duplicate rules counted as ambiguity on %q", JoinSymbols(res.String))
	}
}

func TestDuplicateRulesInForest(t *testing.T) {
	// Even if duplicate productions reach the forest (bypassing the
	// parser's dedup), packed-node sharing must collapse them: the two
	// construction paths build one and the same tree.
	g := &Grammar{
		Start:        "S",
		Nonterminals: map[Symbol]bool{"S": true, "A": true},
		Terminals:    []Symbol{"a"},
		Productions: []Production{
			{LHS: "S", RHS: []Symbol{"A", "A"}},
			{LHS: "A", RHS: []Symbol{"a"}},
			{LHS: "A", RHS: []Symbol{"a"}},
		},
	}
	res := FindAmbiguous(g, 2, -1)
	if res.Found {
		t.Fatalf("duplicate productions counted as ambiguity on %q", JoinSymbols(res.String))
	}
}

func TestTerminalOrderTieBreak(t *testing.T) {
	// "a a" and "b b" are both ambiguous at length 2; b appears first in
	// the rules, so "b b" must win.
	g := mustParse(t, "S -> X | Z | Y | W\nX -> B B\nZ -> A A\nY -> C C\nW -> D D\nB -> b\nA -> a\nC -> b\nD -> a")
	res := FindAmbiguous(g, 3, -1)
	checkWitness(t, g, res, "b b")
}

func TestBudgetExhausted(t *testing.T) {
	g := mustParse(t, "E -> E + E | a")
	res := FindAmbiguous(g, 5, 0)
	if !res.Exhausted {
		t.Fatalf("budget 0: got %+v, want Exhausted", res)
	}
	if res.Found {
		t.Fatalf("budget 0 must not report a result")
	}
}

func TestInvalidGrammars(t *testing.T) {
	for _, src := range []string{
		"",                   // empty
		"S -> a |",           // empty alternative
		"S -> ε",             // epsilon symbol
		"S -> A | a\nA -> S", // unit-production cycle
		"S a",                // missing ->
		"S -> a\nS ->",       // empty production
	} {
		if _, err := ParseGrammar(strings.NewReader(src)); err == nil {
			t.Errorf("ParseGrammar(%q): expected error", src)
		}
	}
}

// --- Independent brute-force reference -----------------------------------
//
// deriveAll enumerates parse trees by breadth-first expansion of
// sentential forms, materializing every partial tree explicitly. It
// shares no code with the shared-parse-forest implementation, so the
// cross-check below catches dedup and counting bugs in either direction.

type pnode struct {
	sym      Symbol
	children []*pnode
}

func serializePnode(p *pnode) string {
	if len(p.children) == 0 {
		return string(p.sym)
	}
	parts := make([]string, len(p.children))
	for i, c := range p.children {
		parts[i] = serializePnode(c)
	}
	return "(" + string(p.sym) + " " + strings.Join(parts, " ") + ")"
}

func copyWithMap(p *pnode, m map[*pnode]*pnode) *pnode {
	cp := &pnode{sym: p.sym}
	m[p] = cp
	if p.children != nil {
		cp.children = make([]*pnode, len(p.children))
		for i, c := range p.children {
			cp.children[i] = copyWithMap(c, m)
		}
	}
	return cp
}

// deriveAll returns, for every derivable terminal string of length
// <= maxLen, the set of its distinct parse trees in serialized form.
func deriveAll(g *Grammar, maxLen int) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	root := &pnode{sym: g.Start}
	type state struct {
		root *pnode
		form []*pnode // current sentential form = fringe of root
	}
	keyOf := func(st state) string {
		// The whole partial tree identifies the state: two states with
		// the same fringe but different internal structure have
		// different futures and must not be merged.
		return serializePnode(st.root)
	}
	start := state{root: root, form: []*pnode{root}}
	queue := []state{start}
	seen := map[string]bool{keyOf(start): true}
	for len(queue) > 0 {
		st := queue[0]
		queue = queue[1:]
		pos := -1
		for i, e := range st.form {
			if g.Nonterminals[e.sym] {
				pos = i
				break
			}
		}
		if pos == -1 {
			syms := make([]string, len(st.form))
			for i, e := range st.form {
				syms[i] = string(e.sym)
			}
			y := strings.Join(syms, " ")
			if out[y] == nil {
				out[y] = map[string]bool{}
			}
			out[y][serializePnode(st.root)] = true
			continue
		}
		e := st.form[pos]
		for _, p := range g.Productions {
			if p.LHS != e.sym {
				continue
			}
			// No epsilon productions: every form symbol yields at least
			// one terminal, so a longer form can never fit in maxLen.
			if len(st.form)-1+len(p.RHS) > maxLen {
				continue
			}
			m := map[*pnode]*pnode{}
			nroot := copyWithMap(st.root, m)
			ne := m[e]
			ne.children = make([]*pnode, len(p.RHS))
			nform := make([]*pnode, 0, len(st.form)-1+len(p.RHS))
			for i, fe := range st.form {
				if i == pos {
					for k, s := range p.RHS {
						c := &pnode{sym: s}
						ne.children[k] = c
						nform = append(nform, c)
					}
				} else {
					nform = append(nform, m[fe])
				}
			}
			nst := state{root: nroot, form: nform}
			if k := keyOf(nst); !seen[k] {
				seen[k] = true
				queue = append(queue, nst)
			}
		}
	}
	return out
}

// bruteFirst finds the first ambiguous string by the same enumeration
// order as FindAmbiguous, using only deriveAll.
func bruteFirst(g *Grammar, maxLen int) ([]Symbol, bool) {
	all := deriveAll(g, maxLen)
	nTerm := len(g.Terminals)
	for length := 1; length <= maxLen; length++ {
		if nTerm == 0 {
			break
		}
		idx := make([]int, length)
		for {
			s := make([]Symbol, length)
			for m, ti := range idx {
				s[m] = g.Terminals[ti]
			}
			if len(all[JoinSymbols(s)]) >= 2 {
				return s, true
			}
			if !nextIndices(idx, nTerm) {
				break
			}
		}
	}
	return nil, false
}

func TestCrossCheckBruteForce(t *testing.T) {
	grammars := map[string]string{
		"expr":         "E -> E + E | E * E | ( E ) | a",
		"ss":           "S -> S S | a",
		"leftrec-ok":   "S -> S a | b",
		"dup-rules":    "S -> A A\nA -> a\nA -> a",
		"unit-chain":   "S -> A\nA -> B\nB -> b",
		"leftrec-amb":  "S -> A A\nA -> a | A a",
		"two-prods":    "S -> A B | A C\nA -> a\nB -> b\nC -> b",
		"ternary":      "S -> S S S | a | a a",
		"tie-break":    "S -> X | Z | Y | W\nX -> B B\nZ -> A A\nY -> C C\nW -> D D\nB -> b\nA -> a\nC -> b\nD -> a",
		"unamb-parens": "S -> ( S ) | a",
		"unamb-list":   "S -> a S | a",
	}
	const maxLen = 5
	for name, src := range grammars {
		t.Run(name, func(t *testing.T) {
			g := mustParse(t, src)
			res := FindAmbiguous(g, maxLen, -1)
			want, wantOK := bruteFirst(g, maxLen)
			if res.Found != wantOK {
				t.Fatalf("Found=%v, brute force says %v (states=%d)", res.Found, wantOK, res.States)
			}
			if !wantOK {
				return
			}
			checkWitness(t, g, res, JoinSymbols(want))
		})
	}
}
