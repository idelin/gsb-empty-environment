// Package petri implements reachability analysis for integer token Petri
// nets with per-place capacity bounds.
//
// Firing semantics: a transition first consumes tokens from its input
// places, then produces tokens into its output places. A transition is
// disabled when the marking does not provide enough tokens to consume, or
// when the resulting marking would exceed any place capacity. Self-loop
// arcs (a place that is both input and output of the same transition) and
// zero-consumption transitions (no input arcs) are fully supported.
package petri

import "fmt"

// Marking maps each place index to its token count.
type Marking []int

// Transition describes one transition of the net. Consume[p] is the number
// of tokens removed from place p when the transition fires, Produce[p] is
// the number of tokens added to place p afterwards. Both slices must have
// exactly one entry per place; use 0 for places without an arc.
type Transition struct {
	Name    string
	Consume []int
	Produce []int
}

// Net is a capacity-bounded integer token net. Capacity[p] is the maximum
// number of tokens place p may hold; every place must have a finite,
// non-negative capacity so the state space is finite.
type Net struct {
	Capacity    []int
	Transitions []Transition
}

// Places returns the number of places in the net.
func (n Net) Places() int { return len(n.Capacity) }

// Validate checks the structural consistency of the net.
func (n Net) Validate() error {
	if len(n.Capacity) == 0 {
		return fmt.Errorf("petri: net must have at least one place")
	}
	for p, c := range n.Capacity {
		if c < 0 {
			return fmt.Errorf("petri: capacity of place %d is negative (%d)", p, c)
		}
	}
	for t, tr := range n.Transitions {
		if len(tr.Consume) != len(n.Capacity) || len(tr.Produce) != len(n.Capacity) {
			return fmt.Errorf("petri: transition %d (%q) arc vectors must have %d entries", t, tr.Name, len(n.Capacity))
		}
		for p := range n.Capacity {
			if tr.Consume[p] < 0 || tr.Produce[p] < 0 {
				return fmt.Errorf("petri: transition %d (%q) has negative arc weight on place %d", t, tr.Name, p)
			}
		}
	}
	return nil
}

// CheckMarking verifies that m is a legal marking of the net: correct
// length, non-negative token counts, and within capacity bounds.
func (n Net) CheckMarking(m Marking) error {
	if len(m) != len(n.Capacity) {
		return fmt.Errorf("petri: marking has %d places, net has %d", len(m), len(n.Capacity))
	}
	for p, tok := range m {
		if tok < 0 {
			return fmt.Errorf("petri: marking has negative token count on place %d", p)
		}
		if tok > n.Capacity[p] {
			return fmt.Errorf("petri: marking exceeds capacity of place %d (%d > %d)", p, tok, n.Capacity[p])
		}
	}
	return nil
}

// Fire is the independent single-step executor. It applies transition t to
// marking m using consume-then-produce semantics and returns the successor
// marking. It returns ok=false without modifying m when the transition is
// disabled (insufficient tokens to consume, or the result would exceed a
// place capacity).
func (n Net) Fire(m Marking, t int) (next Marking, ok bool) {
	tr := n.Transitions[t]
	next = make(Marking, len(m))
	for p := range m {
		if m[p] < tr.Consume[p] {
			return nil, false // not enough tokens to consume
		}
	}
	for p := range m {
		next[p] = m[p] - tr.Consume[p] + tr.Produce[p]
		if next[p] > n.Capacity[p] {
			return nil, false // capacity of place p would be exceeded
		}
	}
	return next, true
}

// Replay is the independent step-by-step verifier: it starts from the
// initial marking and fires the given transition sequence one step at a
// time, returning the marking reached. It fails if any step is disabled.
func (n Net) Replay(initial Marking, seq []int) (Marking, error) {
	if err := n.CheckMarking(initial); err != nil {
		return nil, err
	}
	cur := make(Marking, len(initial))
	copy(cur, initial)
	for i, t := range seq {
		if t < 0 || t >= len(n.Transitions) {
			return nil, fmt.Errorf("petri: step %d: invalid transition index %d", i, t)
		}
		next, ok := n.Fire(cur, t)
		if !ok {
			name := n.Transitions[t].Name
			return nil, fmt.Errorf("petri: step %d: transition %d (%q) is disabled", i, t, name)
		}
		cur = next
	}
	return cur, nil
}
