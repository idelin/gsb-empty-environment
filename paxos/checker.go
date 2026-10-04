package paxos

import (
	"fmt"
	"sort"
)

// Checker independently verifies Paxos safety from observed events. It does
// not trust any node's view: a value is chosen at a slot only when a
// majority of acceptors have durably accepted that value at that slot.
type Checker struct {
	votes      map[int]map[string]map[int]bool // slot -> value -> acceptors
	chosen     map[int]string                  // slot -> the single chosen value
	violations []string
}

func NewChecker() *Checker {
	return &Checker{
		votes:  map[int]map[string]map[int]bool{},
		chosen: map[int]string{},
	}
}

// RecordAccept is called when an acceptor durably accepts (b, v) at slot.
func (c *Checker) RecordAccept(node, slot int, b Ballot, v string) {
	vs := c.votes[slot]
	if vs == nil {
		vs = map[string]map[int]bool{}
		c.votes[slot] = vs
	}
	acc := vs[v]
	if acc == nil {
		acc = map[int]bool{}
		vs[v] = acc
	}
	acc[node] = true
	if len(acc) >= Majority {
		if cv, ok := c.chosen[slot]; ok && cv != v {
			c.violations = append(c.violations, fmt.Sprintf(
				"slot %d chosen with two values: %s and %s", slot, printable(cv), printable(v)))
			return
		}
		c.chosen[slot] = v
	}
}

// RecordLearn is called when a node learns that slot is chosen with v.
func (c *Checker) RecordLearn(node, slot int, v string) {
	cv, ok := c.chosen[slot]
	if !ok {
		c.violations = append(c.violations, fmt.Sprintf(
			"node %d learned slot %d = %s without majority-accept evidence", node, slot, printable(v)))
		return
	}
	if cv != v {
		c.violations = append(c.violations, fmt.Sprintf(
			"node %d learned slot %d = %s but %s is chosen", node, slot, printable(v), printable(cv)))
	}
}

// RecordApplied is called when a node applies v at slot.
func (c *Checker) RecordApplied(node, slot int, v string) {
	cv, ok := c.chosen[slot]
	if !ok || cv != v {
		c.violations = append(c.violations, fmt.Sprintf(
			"node %d applied slot %d = %s which is not the chosen value", node, slot, printable(v)))
	}
}

func (c *Checker) Violations() []string { return append([]string(nil), c.violations...) }

// Chosen returns the chosen value at slot, if any.
func (c *Checker) Chosen(slot int) (string, bool) {
	v, ok := c.chosen[slot]
	return v, ok
}

// ChosenSlots returns all slots known chosen, in order.
func (c *Checker) ChosenSlots() []int { return sortedKeys(c.chosen) }

// Evidence returns the acceptors whose accepts prove the chosen value at slot.
func (c *Checker) Evidence(slot int) (string, []int) {
	v, ok := c.chosen[slot]
	if !ok {
		return "", nil
	}
	var accs []int
	for a := range c.votes[slot][v] {
		accs = append(accs, a)
	}
	sort.Ints(accs)
	return v, accs
}
