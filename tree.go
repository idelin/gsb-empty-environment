package ambiguity

import (
	"fmt"
	"strings"
)

// Tree is one materialized parse tree, returned so callers can verify the
// ambiguity claim independently of the forest machinery.
type Tree struct {
	Sym      Symbol
	Children []*Tree // nil for a terminal leaf
}

// String renders the tree in bracket notation, e.g. (S (A a) (A b)).
func (t *Tree) String() string {
	if len(t.Children) == 0 {
		return string(t.Sym)
	}
	parts := make([]string, len(t.Children))
	for i, c := range t.Children {
		parts[i] = c.String()
	}
	return "(" + string(t.Sym) + " " + strings.Join(parts, " ") + ")"
}

// Yield returns the terminal string at the leaves, left to right.
func (t *Tree) Yield() []Symbol {
	if len(t.Children) == 0 {
		return []Symbol{t.Sym}
	}
	var out []Symbol
	for _, c := range t.Children {
		out = append(out, c.Yield()...)
	}
	return out
}

// ValidateTree independently checks that t is a well-formed parse tree of
// g: every internal node expands by a production of g and every leaf is a
// terminal. It does not use any forest state.
func ValidateTree(g *Grammar, t *Tree) error {
	if len(t.Children) == 0 {
		if g.Nonterminals[t.Sym] {
			return fmt.Errorf("leaf labeled with nonterminal %q", t.Sym)
		}
		return nil
	}
	rhs := make([]Symbol, len(t.Children))
	for i, c := range t.Children {
		rhs[i] = c.Sym
	}
	ok := false
	for _, p := range g.Productions {
		if p.LHS != t.Sym || len(p.RHS) != len(rhs) {
			continue
		}
		match := true
		for i := range rhs {
			if p.RHS[i] != rhs[i] {
				match = false
				break
			}
		}
		if match {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("no production %s -> %s", t.Sym, JoinSymbols(rhs))
	}
	for _, c := range t.Children {
		if err := ValidateTree(g, c); err != nil {
			return err
		}
	}
	return nil
}

// JoinSymbols renders a symbol sequence with single spaces.
func JoinSymbols(s []Symbol) string {
	parts := make([]string, len(s))
	for i, sym := range s {
		parts[i] = string(sym)
	}
	return strings.Join(parts, " ")
}
