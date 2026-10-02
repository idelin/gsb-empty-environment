package earley

import "fmt"

// Token is one input symbol. Type is matched against terminal symbols of
// the grammar; Value is the surface form kept for display in parse trees.
type Token struct {
	Type  string
	Value string
}

// state is a single Earley item: a rule with a dot position and an origin
// (the input position where recognition of the rule began). backs records
// every way the item was derived, forming the packed parse forest.
type state struct {
	rule   int // index into Grammar.Rules
	dot    int
	origin int
	backs  []backpointer
}

// backpointer records one derivation step of a state: pred advanced over
// child (a completed state) or over a scanned terminal token.
type backpointer struct {
	pred  *state
	child *state // completed child state (completer); nil for a scanned token
	term  Token  // set when child == nil (scanner)
}

func (s *state) complete(g *Grammar) bool { return s.dot >= len(g.Rules[s.rule].RHS) }

func (s *state) next(g *Grammar) (Symbol, bool) {
	if s.complete(g) {
		return "", false
	}
	return g.Rules[s.rule].RHS[s.dot], true
}

type stateKey struct {
	rule, dot, origin int
}

// position is one chart column: all items valid after consuming i tokens.
type position struct {
	states []*state
	index  map[stateKey]*state
}

// Chart is the full parse table produced by the Earley algorithm.
// Positions[i] holds the items after consuming i input tokens.
type Chart struct {
	grammar   *Grammar
	tokens    []Token
	Positions []*position
}

// Parse runs the Earley algorithm over tokens and returns the full chart.
func (g *Grammar) Parse(tokens []Token) *Chart {
	n := len(tokens)
	c := &Chart{grammar: g, tokens: tokens, Positions: make([]*position, n+1)}
	for i := 0; i <= n; i++ {
		c.Positions[i] = &position{index: make(map[stateKey]*state)}
	}
	// Seed with the augmented start rule.
	c.add(0, &state{rule: 0, dot: 0, origin: 0}, backpointer{})

	for i := 0; i <= n; i++ {
		pos := c.Positions[i]
		// pos.states grows while we iterate; every new item is processed
		// exactly once, which drives the chart to a fixed point.
		for qi := 0; qi < len(pos.states); qi++ {
			st := pos.states[qi]
			if st.complete(g) {
				// Completer: advance every item at st.origin that waits
				// for the non-terminal st just recognized.
				lhs := g.Rules[st.rule].LHS
				for _, w := range c.Positions[st.origin].states {
					if sym, ok := w.next(g); ok && sym == lhs {
						c.add(i, &state{rule: w.rule, dot: w.dot + 1, origin: w.origin},
							backpointer{pred: w, child: st})
					}
				}
				continue
			}
			sym, _ := st.next(g)
			if g.isTerminal(sym) {
				// Scanner.
				if i < n && tokens[i].Type == string(sym) {
					c.add(i+1, &state{rule: st.rule, dot: st.dot + 1, origin: st.origin},
						backpointer{pred: st, term: tokens[i]})
				}
				continue
			}
			// Predictor.
			for _, ri := range g.rulesFor(sym) {
				c.add(i, &state{rule: ri, dot: 0, origin: i}, backpointer{})
			}
			// Nullable fix: completions of sym at position i may already
			// exist (e.g. epsilon items completed before st was added).
			for _, c2 := range pos.states {
				if c2.complete(g) && c2.origin == i && g.Rules[c2.rule].LHS == sym {
					c.add(i, &state{rule: st.rule, dot: st.dot + 1, origin: st.origin},
						backpointer{pred: st, child: c2})
				}
			}
		}
	}
	return c
}

// add inserts st (with its backpointer) into position i, merging with an
// identical existing item by appending the backpointer instead.
func (c *Chart) add(i int, st *state, back backpointer) {
	pos := c.Positions[i]
	key := stateKey{st.rule, st.dot, st.origin}
	if existing, ok := pos.index[key]; ok {
		if !back.empty() && !existing.hasBack(back) {
			existing.backs = append(existing.backs, back)
		}
		return
	}
	if !back.empty() {
		st.backs = []backpointer{back}
	}
	pos.index[key] = st
	pos.states = append(pos.states, st)
}

func (b backpointer) empty() bool { return b.pred == nil && b.child == nil }

func (s *state) hasBack(b backpointer) bool {
	for _, x := range s.backs {
		if x.pred == b.pred && x.child == b.child && x.term == b.term {
			return true
		}
	}
	return false
}

// Accepted reports whether the input is in the language: the augmented
// start rule completed at the final position with origin 0.
func (c *Chart) Accepted() bool {
	return c.finalState() != nil
}

func (c *Chart) finalState() *state {
	last := c.Positions[len(c.tokens)]
	for _, st := range last.states {
		if st.rule == 0 && st.origin == 0 && st.complete(c.grammar) {
			return st
		}
	}
	return nil
}

// String renders the whole chart, one section per position.
func (c *Chart) String() string {
	g := c.grammar
	out := ""
	for i, pos := range c.Positions {
		out += fmt.Sprintf("=== Chart[%d] ===\n", i)
		for _, st := range pos.states {
			out += "  " + formatState(g, st) + "\n"
		}
	}
	return out
}

func formatState(g *Grammar, st *state) string {
	r := g.Rules[st.rule]
	s := fmt.Sprintf("%s ->", r.LHS)
	for i, sym := range r.RHS {
		if i == st.dot {
			s += " •"
		}
		s += " " + string(sym)
	}
	if st.dot == len(r.RHS) {
		s += " •"
	}
	return fmt.Sprintf("%s  (%d)", s, st.origin)
}
