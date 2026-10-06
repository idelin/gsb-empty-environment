// Package editfa computes minimum-cost edit scripts that transform an
// input string into a string accepted by a deterministic finite
// automaton, using insert, delete and replace operations with positive
// integer costs.
package editfa

import "sort"

// DFA is a deterministic finite automaton over byte symbols.
//
// Trans holds one transition map per state; a missing symbol means the
// transition is undefined (the string is rejected). Accept must have at
// most one entry per state; states without an entry are non-accepting.
type DFA struct {
	Start  int
	Accept []bool
	Trans  []map[byte]int
}

// NumStates returns the number of states of the automaton.
func (d *DFA) NumStates() int { return len(d.Trans) }

// step follows the transition for symbol c from state q.
func (d *DFA) step(q int, c byte) (int, bool) {
	if q < 0 || q >= len(d.Trans) {
		return 0, false
	}
	nq, ok := d.Trans[q][c]
	if !ok || nq < 0 || nq >= len(d.Trans) {
		return 0, false
	}
	return nq, true
}

// acceptsState reports whether q is an accepting state.
func (d *DFA) acceptsState(q int) bool {
	return q >= 0 && q < len(d.Accept) && d.Accept[q]
}

// Accepts reports whether the automaton accepts s.
func (d *DFA) Accepts(s string) bool {
	q := d.Start
	for i := 0; i < len(s); i++ {
		nq, ok := d.step(q, s[i])
		if !ok {
			return false
		}
		q = nq
	}
	return d.acceptsState(q)
}

// Alphabet returns the sorted set of symbols used by any transition.
func (d *DFA) Alphabet() []byte {
	seen := make(map[byte]bool)
	for _, row := range d.Trans {
		for c := range row {
			seen[c] = true
		}
	}
	out := make([]byte, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
