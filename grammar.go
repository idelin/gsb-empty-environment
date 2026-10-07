package uniparse

import (
	"fmt"
	"strings"
)

// Hard input limits.
const (
	MaxRules = 20
	MaxWords = 20
)

// Symbol is a grammar symbol. Terminals are plain input words; nonterminals
// may carry a finite feature map.
type Symbol struct {
	Name     string
	Terminal bool
	Feats    Feats
}

// NT returns a nonterminal symbol with the given features (nil allowed).
func NT(name string, feats Feats) Symbol { return Symbol{Name: name, Feats: feats} }

// T returns a terminal symbol matching an input word.
func T(word string) Symbol { return Symbol{Name: word, Terminal: true} }

func (s Symbol) String() string {
	if s.Terminal {
		return fmt.Sprintf("%q", s.Name)
	}
	if len(s.Feats) == 0 {
		return s.Name
	}
	return s.Name + featsKey(s.Feats)
}

// Rule is an epsilon-free production. LHS must be a nonterminal.
type Rule struct {
	LHS Symbol
	RHS []Symbol
}

func (r *Rule) String() string {
	var b strings.Builder
	b.WriteString(r.LHS.String())
	b.WriteString(" ->")
	for _, s := range r.RHS {
		b.WriteByte(' ')
		b.WriteString(s.String())
	}
	return b.String()
}

// Grammar is a set of epsilon-free productions plus a start category.
// If Start.Name is empty, the LHS of the first rule is used.
type Grammar struct {
	Rules []Rule
	Start Symbol
}

func (g *Grammar) validate() error {
	if len(g.Rules) == 0 {
		return fmt.Errorf("uniparse: grammar has no rules")
	}
	if len(g.Rules) > MaxRules {
		return fmt.Errorf("uniparse: grammar has %d rules, max is %d", len(g.Rules), MaxRules)
	}
	for i := range g.Rules {
		r := &g.Rules[i]
		if r.LHS.Terminal {
			return fmt.Errorf("uniparse: rule %d: terminal %q on the left-hand side", i, r.LHS.Name)
		}
		if r.LHS.Name == "" {
			return fmt.Errorf("uniparse: rule %d: empty left-hand side", i)
		}
		if len(r.RHS) == 0 {
			return fmt.Errorf("uniparse: rule %d (%s): empty (epsilon) production is not allowed", i, r.LHS.Name)
		}
	}
	return nil
}

func (g *Grammar) start() Symbol {
	if g.Start.Name != "" {
		return g.Start
	}
	return NT(g.Rules[0].LHS.Name, nil)
}
