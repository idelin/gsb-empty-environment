// Package errmerge implements diagnostic merging for parallel/backtracking
// parsers: failures from multiple branches are merged by farthest consumed
// position, expected-token sets are unioned at that position, and the
// shortest source chain is kept per distinct calling context.
package errmerge

import (
	"fmt"
	"sort"
	"strings"
)

// Frame is one entry in a source chain: the name of a rule invocation.
type Frame struct {
	Rule string
}

// Diag is a merged diagnostic anchored at a single farthest position.
type Diag struct {
	Pos      int
	Expected map[string]struct{}
	// Chains maps a calling-context key (innermost rule) to the shortest
	// known source chain (outermost -> innermost) for that context.
	Chains map[string][]Frame
}

// New builds a diagnostic expecting one token at pos.
func New(pos int, expected string) *Diag {
	return &Diag{
		Pos:      pos,
		Expected: map[string]struct{}{expected: {}},
		Chains:   map[string][]Frame{},
	}
}

// contextKey identifies a calling context by its innermost frame.
func contextKey(chain []Frame) string {
	if len(chain) == 0 {
		return ""
	}
	return chain[len(chain)-1].Rule
}

// addChain records chain, keeping only the shortest chain per context.
func (d *Diag) addChain(chain []Frame) {
	key := contextKey(chain)
	if cur, ok := d.Chains[key]; !ok || len(chain) < len(cur) {
		cp := make([]Frame, len(chain))
		copy(cp, chain)
		d.Chains[key] = cp
	}
}

// Merge combines two diagnostics. The one anchored farther into the input
// wins outright; at equal positions the expected sets are unioned and the
// shortest source chain is kept per distinct calling context. Nil-safe.
func Merge(a, b *Diag) *Diag {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Pos > b.Pos {
		return a
	}
	if b.Pos > a.Pos {
		return b
	}
	m := &Diag{Pos: a.Pos, Expected: map[string]struct{}{}, Chains: map[string][]Frame{}}
	for e := range a.Expected {
		m.Expected[e] = struct{}{}
	}
	for e := range b.Expected {
		m.Expected[e] = struct{}{}
	}
	for _, c := range a.Chains {
		m.addChain(c)
	}
	for _, c := range b.Chains {
		m.addChain(c)
	}
	return m
}

// PushFrame prepends an outer frame to every source chain in the diagnostic.
// Called by Named while a failure unwinds through a rule invocation.
func (d *Diag) PushFrame(rule string) {
	if len(d.Chains) == 0 {
		d.Chains[rule] = []Frame{{Rule: rule}}
		return
	}
	for key, chain := range d.Chains {
		grown := make([]Frame, 0, len(chain)+1)
		grown = append(grown, Frame{Rule: rule})
		grown = append(grown, chain...)
		delete(d.Chains, key)
		d.Chains[contextKey(grown)] = grown
	}
}

// SortedExpected returns the expected tokens in deterministic order.
func (d *Diag) SortedExpected() []string {
	out := make([]string, 0, len(d.Expected))
	for e := range d.Expected {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func (d *Diag) String() string {
	return fmt.Sprintf("parse error at offset %d: expected %s",
		d.Pos, strings.Join(d.SortedExpected(), ", "))
}
