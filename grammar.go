package main

import "strings"

// Rule is a single context-free production: LHS -> RHS.
// An empty (nil) RHS denotes an epsilon production.
type Rule struct {
	LHS string
	RHS []string
}

func (r Rule) String() string {
	if len(r.RHS) == 0 {
		return r.LHS + " -> <epsilon>"
	}
	return r.LHS + " -> " + strings.Join(r.RHS, " ")
}

// Grammar is an arbitrary context-free grammar. Any symbol that appears
// on the left-hand side of some rule is a non-terminal; every other
// symbol on a right-hand side is a terminal.
type Grammar struct {
	Start string
	Rules []Rule

	byLHS       map[string][]int
	nonterminal map[string]bool

	// MatchTerminal reports whether grammar terminal t matches input
	// token tok. If nil, exact string equality is used.
	MatchTerminal func(t, tok string) bool
}

// NewGrammar builds a grammar from a start symbol and a list of rules.
func NewGrammar(start string, rules ...Rule) *Grammar {
	g := &Grammar{
		Start:       start,
		Rules:       rules,
		byLHS:       make(map[string][]int),
		nonterminal: make(map[string]bool),
	}
	for i, r := range rules {
		g.byLHS[r.LHS] = append(g.byLHS[r.LHS], i)
		g.nonterminal[r.LHS] = true
	}
	return g
}

// IsNonTerminal reports whether s is a non-terminal of the grammar.
func (g *Grammar) IsNonTerminal(s string) bool { return g.nonterminal[s] }

func (g *Grammar) terminalMatches(t, tok string) bool {
	if g.MatchTerminal != nil {
		return g.MatchTerminal(t, tok)
	}
	return t == tok
}
