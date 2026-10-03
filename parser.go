package main

import (
	"fmt"
	"strings"
)

// augmentedStart is the LHS of the synthetic accept rule S' -> Start.
const augmentedStart = "S'"

// Item is an Earley item: a rule with a dot position and an origin
// (the chart index where recognition of this rule began).
type Item struct {
	Rule   int
	Dot    int
	Origin int
}

// backPointer records one way an item was derived, so that parse trees
// can be enumerated from the chart afterwards.
type backPointer struct {
	prev      Item
	prevSet   int
	completed Item
	terminal  string
	isScan    bool
}

// stateSet is one column of the Earley chart.
type stateSet struct {
	items []Item
	index map[Item]int
	backs map[Item][]backPointer
}

func newStateSet() *stateSet {
	return &stateSet{
		index: make(map[Item]int),
		backs: make(map[Item][]backPointer),
	}
}

// add inserts an item (with an optional back-pointer) into the set.
// Duplicate items are merged; duplicate back-pointers are dropped.
func (s *stateSet) add(it Item, bp *backPointer) {
	if _, ok := s.index[it]; ok {
		if bp != nil {
			for _, b := range s.backs[it] {
				if b == *bp {
					return
				}
			}
			s.backs[it] = append(s.backs[it], *bp)
		}
		return
	}
	s.index[it] = len(s.items)
	s.items = append(s.items, it)
	if bp != nil {
		s.backs[it] = append(s.backs[it], *bp)
	}
}

// Chart is the complete parse graph produced by Parse: n+1 state sets
// for an input of n tokens, with back-pointers between items.
type Chart struct {
	g      *Grammar
	toks   []string
	sets   []*stateSet
	accept Item
}

// Parse runs the Earley algorithm over tokens and returns the full
// chart. It handles left/right recursion, epsilon productions and
// ambiguous grammars.
func Parse(g *Grammar, tokens []string) *Chart {
	rules := make([]Rule, 0, len(g.Rules)+1)
	rules = append(rules, Rule{LHS: augmentedStart, RHS: []string{g.Start}})
	rules = append(rules, g.Rules...)
	ag := NewGrammar(augmentedStart, rules...)
	ag.MatchTerminal = g.MatchTerminal

	n := len(tokens)
	c := &Chart{
		g:      ag,
		toks:   tokens,
		sets:   make([]*stateSet, n+1),
		accept: Item{Rule: 0, Dot: 1, Origin: 0},
	}
	for i := range c.sets {
		c.sets[i] = newStateSet()
	}
	c.sets[0].add(Item{Rule: 0, Dot: 0, Origin: 0}, nil)

	for i := 0; i <= n; i++ {
		set := c.sets[i]
		for k := 0; k < len(set.items); k++ {
			it := set.items[k]
			rule := ag.Rules[it.Rule]
			if it.Dot == len(rule.RHS) {
				for _, cand := range c.sets[it.Origin].items {
					cr := ag.Rules[cand.Rule]
					if cand.Dot < len(cr.RHS) && cr.RHS[cand.Dot] == rule.LHS {
						set.add(Item{Rule: cand.Rule, Dot: cand.Dot + 1, Origin: cand.Origin},
							&backPointer{prev: cand, prevSet: it.Origin, completed: it})
					}
				}
			} else {
				next := rule.RHS[it.Dot]
				if ag.IsNonTerminal(next) {
					for _, ri := range ag.byLHS[next] {
						set.add(Item{Rule: ri, Dot: 0, Origin: i}, nil)
					}
					for _, comp := range set.items {
						cr := ag.Rules[comp.Rule]
						if comp.Origin == i && comp.Dot == len(cr.RHS) && cr.LHS == next {
							set.add(Item{Rule: it.Rule, Dot: it.Dot + 1, Origin: it.Origin},
								&backPointer{prev: it, prevSet: i, completed: comp})
						}
					}
				} else if i < n && ag.terminalMatches(next, tokens[i]) {
					c.sets[i+1].add(Item{Rule: it.Rule, Dot: it.Dot + 1, Origin: it.Origin},
						&backPointer{prev: it, prevSet: i, terminal: tokens[i], isScan: true})
				}
			}
		}
	}
	return c
}

// Accepted reports whether the whole input was recognized.
func (c *Chart) Accepted() bool {
	_, ok := c.sets[len(c.toks)].index[c.accept]
	return ok
}

// String renders the full chart (the parse graph) for inspection.
func (c *Chart) String() string {
	var b strings.Builder
	for i, set := range c.sets {
		fmt.Fprintf(&b, "=== Chart[%d] ===\n", i)
		for _, it := range set.items {
			fmt.Fprintf(&b, "  %s\n", c.g.itemString(it))
		}
	}
	return b.String()
}

func (g *Grammar) itemString(it Item) string {
	r := g.Rules[it.Rule]
	var b strings.Builder
	b.WriteString(r.LHS)
	b.WriteString(" ->")
	for i, s := range r.RHS {
		if i == it.Dot {
			b.WriteString(" *")
		}
		b.WriteString(" ")
		b.WriteString(s)
	}
	if it.Dot == len(r.RHS) {
		b.WriteString(" *")
	}
	fmt.Fprintf(&b, "  (%d)", it.Origin)
	return b.String()
}
