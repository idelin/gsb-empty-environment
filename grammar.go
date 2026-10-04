// Package ambiguity finds the shortest string on which a context-free
// grammar is ambiguous (has two distinct parse trees).
//
// The grammar must not contain epsilon productions or unit-production
// cycles. Ambiguity is decided over a shared parse forest (SPPF) so that
// two different construction paths of the same tree are never counted as
// ambiguity; only genuinely different trees are.
package ambiguity

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Symbol is a grammar symbol. A symbol is a nonterminal iff it appears on
// the left-hand side of some production; every other symbol is a terminal.
type Symbol string

// Production is a single rule LHS -> RHS.
type Production struct {
	LHS Symbol
	RHS []Symbol
}

// Grammar is a parsed, deduplicated context-free grammar.
type Grammar struct {
	Start        Symbol
	Productions  []Production // duplicate rules removed, order preserved
	Nonterminals map[Symbol]bool
	// Terminals lists every terminal in order of first appearance in the
	// rules. This order defines the tie-break between ambiguous strings of
	// equal length.
	Terminals []Symbol
}

// IsNonterminal reports whether s is a nonterminal.
func (g *Grammar) IsNonterminal(s Symbol) bool { return g.Nonterminals[s] }

// ParseGrammar reads a grammar from r.
//
// Format: one rule per line, "LHS -> alt1 | alt2 | ...", tokens separated
// by whitespace. '#' starts a comment. Several lines may share the same
// LHS. The LHS of the first rule is the start symbol. Epsilon productions
// and unit-production cycles are rejected. Duplicate alternatives are
// collapsed: they describe the same tree, not ambiguity.
func ParseGrammar(r io.Reader) (*Grammar, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty grammar")
	}

	type rawRule struct {
		lhs string
		rhs []string
	}
	var rules []rawRule
	lhsSet := map[string]bool{}
	for _, line := range lines {
		parts := strings.SplitN(line, "->", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("rule %q: missing ->", line)
		}
		lhs := strings.TrimSpace(parts[0])
		if lhs == "" || len(strings.Fields(lhs)) != 1 {
			return nil, fmt.Errorf("rule %q: bad left-hand side", line)
		}
		for _, alt := range strings.Split(parts[1], "|") {
			toks := strings.Fields(alt)
			if len(toks) == 0 {
				return nil, fmt.Errorf("rule %q: empty (epsilon) production not allowed", line)
			}
			for _, t := range toks {
				if t == "ε" || t == "epsilon" || t == "EPS" || t == "''" {
					return nil, fmt.Errorf("rule %q: epsilon production not allowed", line)
				}
			}
			rules = append(rules, rawRule{lhs, toks})
		}
		lhsSet[lhs] = true
	}

	g := &Grammar{
		Start:        Symbol(rules[0].lhs),
		Nonterminals: map[Symbol]bool{},
	}
	for lhs := range lhsSet {
		g.Nonterminals[Symbol(lhs)] = true
	}

	seen := map[string]bool{}
	termSeen := map[Symbol]bool{}
	for _, r := range rules {
		key := r.lhs + " -> " + strings.Join(r.rhs, " ")
		if seen[key] {
			continue // duplicate rule: same tree, drop it
		}
		seen[key] = true
		rhs := make([]Symbol, len(r.rhs))
		for i, t := range r.rhs {
			rhs[i] = Symbol(t)
		}
		g.Productions = append(g.Productions, Production{LHS: Symbol(r.lhs), RHS: rhs})
		for _, t := range rhs {
			if !g.Nonterminals[t] && !termSeen[t] {
				termSeen[t] = true
				g.Terminals = append(g.Terminals, t)
			}
		}
	}
	if err := g.checkUnitCycles(); err != nil {
		return nil, err
	}
	return g, nil
}

// checkUnitCycles rejects grammars whose unit productions (A -> B) form a
// cycle. Such a cycle yields infinitely many parse trees for a single
// string and would make tree counting diverge.
func (g *Grammar) checkUnitCycles() error {
	adj := map[Symbol][]Symbol{}
	for _, p := range g.Productions {
		if len(p.RHS) == 1 && g.Nonterminals[p.RHS[0]] {
			adj[p.LHS] = append(adj[p.LHS], p.RHS[0])
		}
	}
	const (
		white = iota
		gray
		black
	)
	color := map[Symbol]int{}
	var visit func(s Symbol) error
	visit = func(s Symbol) error {
		color[s] = gray
		for _, t := range adj[s] {
			switch color[t] {
			case gray:
				return fmt.Errorf("unit-production cycle involving %q", t)
			case white:
				if err := visit(t); err != nil {
					return err
				}
			}
		}
		color[s] = black
		return nil
	}
	for nt := range g.Nonterminals {
		if color[nt] == white {
			if err := visit(nt); err != nil {
				return err
			}
		}
	}
	return nil
}
