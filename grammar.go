// Package earley implements a general context-free grammar Earley parser.
//
// It supports arbitrary CFGs, including left recursion, epsilon
// productions and ambiguous grammars. The parser produces a full parse
// chart and can enumerate every valid parse tree when the grammar is
// ambiguous.
package earley

// Symbol is a grammar symbol. Symbols that never appear on the left-hand
// side of a rule are treated as terminals and matched against token types.
type Symbol string

// Rule is a single production LHS -> RHS. An empty RHS is an epsilon
// production.
type Rule struct {
	LHS Symbol
	RHS []Symbol
}

// R is a convenience constructor for a Rule.
func R(lhs string, rhs ...string) Rule {
	return Rule{LHS: Symbol(lhs), RHS: toSymbols(rhs)}
}

func toSymbols(ss []string) []Symbol {
	out := make([]Symbol, len(ss))
	for i, s := range ss {
		out[i] = Symbol(s)
	}
	return out
}

// Grammar is a context-free grammar in BNF-like form.
type Grammar struct {
	Start Symbol
	Rules []Rule // Rules[0] is the augmented start rule S' -> Start.

	byLHS     map[Symbol][]int // non-terminal -> indices into Rules
	terminals map[Symbol]bool
}

// NewGrammar builds a Grammar from the given start symbol and productions.
// An augmented start rule is prepended internally.
func NewGrammar(start string, rules ...Rule) *Grammar {
	g := &Grammar{
		Start:     Symbol(start),
		byLHS:     make(map[Symbol][]int),
		terminals: make(map[Symbol]bool),
	}
	aug := Symbol(start + "'")
	for _, r := range rules {
		if r.LHS == aug {
			aug += "'"
		}
	}
	g.Rules = append(g.Rules, Rule{LHS: aug, RHS: []Symbol{Symbol(start)}})
	g.Rules = append(g.Rules, rules...)

	onLHS := make(map[Symbol]bool)
	for i, r := range g.Rules {
		g.byLHS[r.LHS] = append(g.byLHS[r.LHS], i)
		onLHS[r.LHS] = true
	}
	for _, r := range g.Rules {
		for _, s := range r.RHS {
			if !onLHS[s] {
				g.terminals[s] = true
			}
		}
	}
	return g
}

func (g *Grammar) isTerminal(s Symbol) bool { return g.terminals[s] }

func (g *Grammar) rulesFor(lhs Symbol) []int { return g.byLHS[lhs] }
