// Package rewrite analyzes bounded string rewriting systems with control
// states and one-time resources. It searches the full reachable state graph
// for cycles, reports shortest entry paths with replayable evidence, and
// computes the longest derivation when no cycle exists.
package rewrite

import (
	"errors"
	"fmt"
	"strings"
)

// Rule replaces one occurrence of LHS with RHS per application. The rule
// fires only in control state From and moves the system to control state To.
// Delta is applied to the resource vector; every resulting component must
// stay within [0, MaxResource]. A nil Delta means no resource change.
type Rule struct {
	Name  string
	LHS   string
	RHS   string // may be empty (erasing rule)
	From  int
	To    int
	Delta []int
}

// State is a full system state. Cycle detection uses the whole State, so a
// repeated string with different control state or resources is not a cycle.
type State struct {
	Str       string
	Ctl       int
	Resources []int
}

// Step is one applied rule at one match position (byte offset). A slice of
// Steps is replayable evidence.
type Step struct {
	Rule     int
	Position int
}

// Config describes a bounded rewriting system.
type Config struct {
	Rules  []Rule
	Init   State
	MaxLen int // successors longer than MaxLen are discarded
	// MaxResource bounds each resource component. Required when any rule
	// has a positive Delta, otherwise the state space could be unbounded.
	MaxResource []int
}

func (s State) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d\x1f", s.Ctl)
	for i, r := range s.Resources {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", r)
	}
	b.WriteByte('\x1f')
	b.WriteString(s.Str)
	return b.String()
}

func (s State) equal(o State) bool {
	if s.Str != o.Str || s.Ctl != o.Ctl || len(s.Resources) != len(o.Resources) {
		return false
	}
	for i := range s.Resources {
		if s.Resources[i] != o.Resources[i] {
			return false
		}
	}
	return true
}

func (c *Config) validate() error {
	if c.MaxLen < 0 {
		return errors.New("rewrite: MaxLen must be non-negative")
	}
	if len(c.Init.Str) > c.MaxLen {
		return fmt.Errorf("rewrite: initial string length %d exceeds MaxLen %d", len(c.Init.Str), c.MaxLen)
	}
	dim := len(c.Init.Resources)
	if c.MaxResource != nil && len(c.MaxResource) != dim {
		return fmt.Errorf("rewrite: MaxResource dimension %d does not match resource dimension %d", len(c.MaxResource), dim)
	}
	for i, v := range c.Init.Resources {
		if v < 0 {
			return fmt.Errorf("rewrite: negative initial resource %d at index %d", v, i)
		}
	}
	for i, v := range c.MaxResource {
		if v < 0 {
			return fmt.Errorf("rewrite: negative MaxResource %d at index %d", v, i)
		}
		if c.Init.Resources[i] > v {
			return fmt.Errorf("rewrite: initial resource %d at index %d exceeds MaxResource %d", c.Init.Resources[i], i, v)
		}
	}
	produces := false
	for i := range c.Rules {
		r := &c.Rules[i]
		if r.LHS == "" {
			return fmt.Errorf("rewrite: rule %d (%q) has empty LHS", i, r.Name)
		}
		if len(r.Delta) != 0 && len(r.Delta) != dim {
			return fmt.Errorf("rewrite: rule %d (%q) Delta dimension %d does not match resource dimension %d", i, r.Name, len(r.Delta), dim)
		}
		for _, d := range r.Delta {
			if d > 0 {
				produces = true
			}
		}
	}
	if produces && c.MaxResource == nil {
		return errors.New("rewrite: rules produce resources but MaxResource is not set (state space would be unbounded)")
	}
	return nil
}

// applyAt applies rule ri at byte position pos in s. It reports whether the
// application is legal: control state matches, LHS occurs at pos, the result
// fits MaxLen, and all resources stay within bounds.
func (c *Config) applyAt(ri, pos int, s State) (State, bool) {
	r := &c.Rules[ri]
	if s.Ctl != r.From {
		return State{}, false
	}
	if pos < 0 || pos+len(r.LHS) > len(s.Str) {
		return State{}, false
	}
	if s.Str[pos:pos+len(r.LHS)] != r.LHS {
		return State{}, false
	}
	ns := s.Str[:pos] + r.RHS + s.Str[pos+len(r.LHS):]
	if len(ns) > c.MaxLen {
		return State{}, false
	}
	nr := make([]int, len(s.Resources))
	for i := range nr {
		d := 0
		if i < len(r.Delta) {
			d = r.Delta[i]
		}
		nr[i] = s.Resources[i] + d
		if nr[i] < 0 {
			return State{}, false
		}
		if c.MaxResource != nil && nr[i] > c.MaxResource[i] {
			return State{}, false
		}
	}
	return State{Str: ns, Ctl: r.To, Resources: nr}, true
}

// successors emits every state reachable from s by one rule application.
// Matches are found at every position, including overlapping ones.
func (c *Config) successors(s State, emit func(State, Step)) {
	for ri := range c.Rules {
		r := &c.Rules[ri]
		if r.From != s.Ctl {
			continue
		}
		for start := 0; start+len(r.LHS) <= len(s.Str); {
			off := strings.Index(s.Str[start:], r.LHS)
			if off < 0 {
				break
			}
			pos := start + off
			start = pos + 1 // step by one so overlapping matches are found
			if ns, ok := c.applyAt(ri, pos, s); ok {
				emit(ns, Step{Rule: ri, Position: pos})
			}
		}
	}
}
