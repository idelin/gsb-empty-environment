// Package petrinet implements a reachability checker for integer-token
// Petri nets with per-place capacity bounds.
//
// Firing semantics: a transition first consumes tokens from its input
// places, then produces tokens into its output places. The transition is
// disabled if any input place lacks enough tokens, or if the resulting
// marking would exceed the capacity of any place. Self-loop arcs (a place
// that is both input and output of the same transition) and
// zero-consumption transitions are handled naturally by this rule.
package petrinet

import "fmt"

// Transition describes one transition of the net. Consume and Produce are
// indexed by place and may contain zeros; a place may appear in both
// (self-loop arc).
type Transition struct {
	Name    string
	Consume []int
	Produce []int
}

// Net is an integer-token Petri net with per-place capacity bounds.
type Net struct {
	Places      []string
	Capacities  []int
	Transitions []Transition
}

// Validate checks the structural consistency of the net.
func (n *Net) Validate() error {
	if len(n.Places) != len(n.Capacities) {
		return fmt.Errorf("petrinet: %d places but %d capacities", len(n.Places), len(n.Capacities))
	}
	for i, c := range n.Capacities {
		if c < 0 {
			return fmt.Errorf("petrinet: negative capacity %d for place %q", c, n.Places[i])
		}
	}
	for i, t := range n.Transitions {
		if len(t.Consume) != len(n.Places) || len(t.Produce) != len(n.Places) {
			return fmt.Errorf("petrinet: transition %d (%q) arc vectors do not match place count", i, t.Name)
		}
		for p := range n.Places {
			if t.Consume[p] < 0 || t.Produce[p] < 0 {
				return fmt.Errorf("petrinet: transition %q has negative arc weight on place %q", t.Name, n.Places[p])
			}
		}
	}
	return nil
}

// CheckMarking verifies that a marking is non-negative and within capacity.
func (n *Net) CheckMarking(m []int) error {
	if len(m) != len(n.Places) {
		return fmt.Errorf("petrinet: marking has %d entries, want %d", len(m), len(n.Places))
	}
	for p, v := range m {
		if v < 0 {
			return fmt.Errorf("petrinet: negative token count %d on place %q", v, n.Places[p])
		}
		if v > n.Capacities[p] {
			return fmt.Errorf("petrinet: marking exceeds capacity on place %q (%d > %d)", n.Places[p], v, n.Capacities[p])
		}
	}
	return nil
}

// Enabled reports whether transition t can fire at marking m: every input
// place must hold enough tokens, and the marking after consuming and then
// producing must stay within capacity.
func (n *Net) Enabled(m []int, t int) bool {
	_, ok := n.Fire(m, t)
	return ok
}

// Fire returns the successor marking of firing transition t at m, or
// ok == false if the transition is disabled. The input marking is not
// modified. Consumption happens before production; the capacity check is
// applied to the resulting marking.
func (n *Net) Fire(m []int, t int) (next []int, ok bool) {
	tr := n.Transitions[t]
	next = make([]int, len(m))
	for p := range m {
		if m[p] < tr.Consume[p] {
			return nil, false
		}
	}
	for p := range m {
		v := m[p] - tr.Consume[p] + tr.Produce[p]
		if v > n.Capacities[p] {
			return nil, false
		}
		next[p] = v
	}
	return next, true
}
