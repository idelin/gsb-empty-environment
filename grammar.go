// Package ambcheck finds the shortest ambiguous string of a context-free
// grammar without epsilon productions, using a shared parse forest with
// structural deduplication.
package ambcheck

import "fmt"

// Rule is a single production LHS -> RHS. RHS must be non-empty: the
// grammar must not contain epsilon productions.
type Rule struct {
	LHS string   `json:"lhs"`
	RHS []string `json:"rhs"`
}

// Grammar is a context-free grammar. Terminals and NonTerminals declare the
// symbol alphabets; the order of Terminals defines the lexicographic order
// used for tie-breaking between ambiguous strings of equal length.
type Grammar struct {
	Terminals    []string `json:"terminals"`
	NonTerminals []string `json:"nonterminals"`
	Start        string   `json:"start"`
	Rules        []Rule   `json:"rules"`
}

// Validate checks that the grammar is well formed: disjoint alphabets, a
// declared start symbol, no epsilon productions, and only declared symbols
// used in rules. Duplicate rules are allowed on purpose (they must not be
// reported as ambiguity).
func (g *Grammar) Validate() error {
	if len(g.Terminals) == 0 {
		return fmt.Errorf("grammar has no terminals")
	}
	term := make(map[string]bool, len(g.Terminals))
	for _, t := range g.Terminals {
		if t == "" {
			return fmt.Errorf("empty terminal symbol")
		}
		if term[t] {
			return fmt.Errorf("duplicate terminal %q", t)
		}
		term[t] = true
	}
	nonterm := make(map[string]bool, len(g.NonTerminals))
	for _, n := range g.NonTerminals {
		if n == "" {
			return fmt.Errorf("empty nonterminal symbol")
		}
		if term[n] {
			return fmt.Errorf("symbol %q declared both terminal and nonterminal", n)
		}
		if nonterm[n] {
			return fmt.Errorf("duplicate nonterminal %q", n)
		}
		nonterm[n] = true
	}
	if !nonterm[g.Start] {
		return fmt.Errorf("start symbol %q is not a declared nonterminal", g.Start)
	}
	if len(g.Rules) == 0 {
		return fmt.Errorf("grammar has no rules")
	}
	for i, r := range g.Rules {
		if !nonterm[r.LHS] {
			return fmt.Errorf("rule %d: LHS %q is not a declared nonterminal", i, r.LHS)
		}
		if len(r.RHS) == 0 {
			return fmt.Errorf("rule %d (%s): epsilon productions are not allowed", i, r.LHS)
		}
		for _, s := range r.RHS {
			if !term[s] && !nonterm[s] {
				return fmt.Errorf("rule %d (%s): undeclared symbol %q", i, r.LHS, s)
			}
		}
	}
	return nil
}

// terminalRank maps each terminal to its position in the declared order.
func (g *Grammar) terminalRank() map[string]int {
	rank := make(map[string]int, len(g.Terminals))
	for i, t := range g.Terminals {
		rank[t] = i
	}
	return rank
}

// lessByTerminalOrder reports whether string a sorts before string b under
// the grammar's terminal ordering (shorter prefix first).
func lessByTerminalOrder(a, b []string, rank map[string]int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if rank[a[i]] != rank[b[i]] {
			return rank[a[i]] < rank[b[i]]
		}
	}
	return len(a) < len(b)
}
