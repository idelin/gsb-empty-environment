package ambcheck

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// bruteForceMinAmbiguous is an independent, deliberately naive reference:
// it enumerates parse trees top-down by height (no shared forest, no
// dedup), collects up to two structurally distinct trees per derived
// string, and returns the shortest ambiguous string (ties broken by
// terminal order). It returns truncated=true when the tree explosion guard
// fired, in which case the comparison must be skipped.
func bruteForceMinAmbiguous(gr *Grammar, maxLen, maxHeight int) (str []string, found, truncated bool) {
	const treeCap = 200000
	byLHS := map[string][]Rule{}
	for _, ru := range gr.Rules {
		byLHS[ru.LHS] = append(byLHS[ru.LHS], ru)
	}
	term := map[string]bool{}
	for _, s := range gr.Terminals {
		term[s] = true
	}
	type key struct {
		lhs string
		h   int
	}
	memo := map[key][]*Tree{}
	var gen func(lhs string, h int) []*Tree
	gen = func(lhs string, h int) []*Tree {
		if h < 1 || truncated {
			return nil
		}
		k := key{lhs, h}
		if ts, ok := memo[k]; ok {
			return ts
		}
		var out []*Tree
		for _, ru := range byLHS[lhs] {
			allTerm := true
			for _, s := range ru.RHS {
				if !term[s] {
					allTerm = false
					break
				}
			}
			if allTerm {
				children := make([]*Tree, len(ru.RHS))
				for i, s := range ru.RHS {
					children[i] = Leaf(s)
				}
				out = append(out, Node(ru.LHS, ru.RHS, children...))
				continue
			}
			if h < 2 {
				continue
			}
			var rec func(pos int, children []*Tree)
			rec = func(pos int, children []*Tree) {
				if truncated {
					return
				}
				if pos == len(ru.RHS) {
					out = append(out, Node(ru.LHS, ru.RHS, append([]*Tree{}, children...)...))
					if len(out) > treeCap {
						truncated = true
					}
					return
				}
				sym := ru.RHS[pos]
				if term[sym] {
					rec(pos+1, append(children, Leaf(sym)))
					return
				}
				for _, sub := range gen(sym, h-1) {
					rec(pos+1, append(children, sub))
				}
			}
			rec(0, nil)
		}
		memo[k] = out
		return out
	}
	rank := gr.terminalRank()
	// string key -> (string, distinct canonical tree forms, capped at 2)
	type amb struct {
		str   []string
		trees map[string]bool
	}
	found2 := map[string]*amb{}
	for h := 1; h <= maxHeight; h++ {
		for _, t := range gen(gr.Start, h) {
			y := t.Yield()
			if len(y) > maxLen {
				continue
			}
			k := strKey(y)
			a := found2[k]
			if a == nil {
				a = &amb{str: y, trees: map[string]bool{}}
				found2[k] = a
			}
			a.trees[t.Canonical()] = true
		}
		if truncated {
			return nil, false, true
		}
	}
	var cands [][]string
	for _, a := range found2 {
		if len(a.trees) >= 2 {
			cands = append(cands, a.str)
		}
	}
	if len(cands) == 0 {
		return nil, false, false
	}
	sort.Slice(cands, func(i, j int) bool {
		if len(cands[i]) != len(cands[j]) {
			return len(cands[i]) < len(cands[j])
		}
		return lessByTerminalOrder(cands[i], cands[j], rank)
	})
	return cands[0], true, false
}

// crossCheck compares Check against the naive reference on one grammar.
func crossCheck(t *testing.T, gr *Grammar, maxLen int) {
	t.Helper()
	maxHeight := maxLen + len(gr.NonTerminals) + 2
	wantStr, wantFound, truncated := bruteForceMinAmbiguous(gr, maxLen, maxHeight)
	if truncated {
		t.Skip("reference enumeration truncated")
	}
	res, err := Check(gr, Options{MaxLength: maxLen, Budget: 20_000_000})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.BudgetExceeded {
		t.Fatalf("budget exceeded after %d states", res.StatesExpanded)
	}
	if res.Ambiguous != wantFound {
		t.Fatalf("Check found=%v (%q), reference found=%v (%q)",
			res.Ambiguous, res.String, wantFound, wantStr)
	}
	if !wantFound {
		return
	}
	if strKey(res.String) != strKey(wantStr) {
		t.Fatalf("Check string %q, reference %q", res.String, wantStr)
	}
	if err := res.Verify(gr); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}
}

func TestCrossCheckCurated(t *testing.T) {
	grammars := map[string]*Grammar{
		"expr":            g("a +", "E", "E", r("E", "E + E"), r("E", "a")),
		"right-rec":       g("a b", "S", "S", r("S", "a S"), r("S", "b")),
		"left-rec":        g("a b", "A", "A", r("A", "A a"), r("A", "b")),
		"dup-rules":       g("a", "S", "S", r("S", "a"), r("S", "a")),
		"dup-chain":       g("a", "S A", "S", r("S", "A"), r("S", "A"), r("A", "a"), r("A", "a")),
		"two-nonterms":    g("a", "S A B", "S", r("S", "A"), r("S", "B"), r("A", "a"), r("B", "a")),
		"unit-cycle":      g("a", "S", "S", r("S", "S"), r("S", "a")),
		"mutual-cycle":    g("a", "S A", "S", r("S", "A"), r("A", "S"), r("A", "a")),
		"binary":          g("a", "S", "S", r("S", "S S"), r("S", "a")),
		"mixed":           g("a b", "S A", "S", r("S", "A S"), r("S", "a"), r("A", "b A"), r("A", "b")),
		"hidden-amb":      g("a b", "S A B", "S", r("S", "A B"), r("S", "B A"), r("A", "a"), r("B", "a")),
		"palindrome":      g("a b", "S", "S", r("S", "a S a"), r("S", "b S b"), r("S", "a"), r("S", "b")),
		"left-rec-amb":    g("a", "S A", "S", r("S", "A A"), r("A", "A a"), r("A", "a")),
		"dup-left-rec":    g("a", "S", "S", r("S", "S a"), r("S", "S a"), r("S", "a")),
		"unit-and-binary": g("a", "S A", "S", r("S", "A"), r("A", "A A"), r("A", "a")),
	}
	for name, gr := range grammars {
		t.Run(name, func(t *testing.T) { crossCheck(t, gr, 5) })
	}
}

// TestCrossCheckRandom fuzzes small grammars (including left-recursive and
// duplicated rules) and cross-checks against the naive reference.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	nonterms := []string{"S", "A", "B"}
	terms := []string{"a", "b"}
	skipped := 0
	for i := 0; i < 400; i++ {
		nNT := 1 + rng.Intn(3)
		nts := nonterms[:nNT]
		symbols := append(append([]string{}, nts...), terms...)
		nRules := 1 + rng.Intn(5)
		var rules []Rule
		for j := 0; j < nRules; j++ {
			lhs := nts[rng.Intn(nNT)]
			rhsLen := 1 + rng.Intn(3)
			rhs := make([]string, rhsLen)
			for k := range rhs {
				rhs[k] = symbols[rng.Intn(len(symbols))]
			}
			rules = append(rules, Rule{LHS: lhs, RHS: rhs})
		}
		// Frequently inject duplicates and left recursion, the tricky cases.
		if rng.Intn(2) == 0 && len(rules) > 0 {
			rules = append(rules, rules[rng.Intn(len(rules))])
		}
		if rng.Intn(2) == 0 {
			lhs := nts[rng.Intn(nNT)]
			rules = append(rules, Rule{LHS: lhs, RHS: []string{lhs, terms[rng.Intn(2)]}})
		}
		gr := &Grammar{Terminals: terms, NonTerminals: nts, Start: "S", Rules: rules}
		t.Run(fmt.Sprintf("grammar-%d", i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on grammar %+v: %v", gr.Rules, r)
				}
			}()
			maxHeight := 4 + len(gr.NonTerminals) + 2
			_, _, truncated := bruteForceMinAmbiguous(gr, 4, maxHeight)
			if truncated {
				skipped++
				t.Skip("reference truncated")
			}
			crossCheck(t, gr, 4)
		})
	}
	t.Logf("skipped %d/400 grammars due to reference truncation", skipped)
}
