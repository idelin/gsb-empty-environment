// Package srs analyzes bounded string rewriting systems with control
// states and one-shot resources.
//
// A System rewrites a string one match at a time. Successors whose
// length exceeds MaxLen are discarded. Because a rule may also depend
// on and mutate a finite control state and a vector of resources, the
// search state is the triple (string, control state, resources): a
// cycle requires all three to repeat, not just the string.
package srs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Rule replaces one occurrence of Left with Right. Right may be empty,
// which deletes the matched text. The rule fires only in control state
// From and moves the machine to control state To. Need is consumed from
// and Gain is added to the resource vector; resources are one-shot, so
// firing requires Need[i] <= Res[i] for every i.
type Rule struct {
	Name  string
	Left  string
	Right string
	From  int
	To    int
	Need  []int
	Gain  []int
}

// System is a bounded string rewriting system.
type System struct {
	Rules  []Rule
	MaxLen int // maximum string length; longer successors are discarded
	NumRes int // resource vector length
}

// State is a full search state: string, control state, resources.
type State struct {
	Str  string
	Ctrl int
	Res  []int
}

// Step records one rule application: which rule fired and the byte
// offset of the replaced match.
type Step struct {
	Rule int
	Pos  int
}

// Successor pairs a successor state with the step that produced it.
type Successor struct {
	State State
	Step  Step
}

// ErrEmptyPattern is returned when a rule has an empty left side.
var ErrEmptyPattern = errors.New("srs: rule with empty left side")

// Validate checks the system for malformed rules.
func (sys *System) Validate() error {
	if sys.MaxLen < 0 {
		return fmt.Errorf("srs: negative MaxLen %d", sys.MaxLen)
	}
	if sys.NumRes < 0 {
		return fmt.Errorf("srs: negative NumRes %d", sys.NumRes)
	}
	for i, r := range sys.Rules {
		if r.Left == "" {
			return fmt.Errorf("srs: rule %d (%q): %v", i, r.Name, ErrEmptyPattern)
		}
		if len(r.Need) > sys.NumRes || len(r.Gain) > sys.NumRes {
			return fmt.Errorf("srs: rule %d (%q): resource vector longer than NumRes", i, r.Name)
		}
		for _, v := range r.Need {
			if v < 0 {
				return fmt.Errorf("srs: rule %d (%q): negative resource need", i, r.Name)
			}
		}
		for _, v := range r.Gain {
			if v < 0 {
				return fmt.Errorf("srs: rule %d (%q): negative resource gain", i, r.Name)
			}
		}
	}
	return nil
}

// matchPositions returns every byte offset where pat occurs in s,
// including overlapping occurrences.
func matchPositions(s, pat string) []int {
	var pos []int
	for i := 0; i+len(pat) <= len(s); i++ {
		if s[i:i+len(pat)] == pat {
			pos = append(pos, i)
		}
	}
	return pos
}

// Successors generates every successor of st in one rewriting step.
// Overlapping matches each yield a successor; successors longer than
// MaxLen are discarded. States are returned in deterministic order:
// rules in declaration order, match positions ascending.
func (sys *System) Successors(st State) []Successor {
	var out []Successor
	for ri, r := range sys.Rules {
		if st.Ctrl != r.From {
			continue
		}
		if !hasResources(st.Res, r.Need, sys.NumRes) {
			continue
		}
		for _, pos := range matchPositions(st.Str, r.Left) {
			next := st.Str[:pos] + r.Right + st.Str[pos+len(r.Left):]
			if len(next) > sys.MaxLen {
				continue
			}
			out = append(out, Successor{
				State: State{
					Str:  next,
					Ctrl: r.To,
					Res:  applyResources(st.Res, r.Need, r.Gain, sys.NumRes),
				},
				Step: Step{Rule: ri, Pos: pos},
			})
		}
	}
	return out
}

func hasResources(res, need []int, numRes int) bool {
	for i := 0; i < numRes && i < len(need); i++ {
		if i >= len(res) || res[i] < need[i] {
			return false
		}
	}
	return true
}

func applyResources(res, need, gain []int, numRes int) []int {
	next := make([]int, numRes)
	for i := 0; i < numRes; i++ {
		if i < len(res) {
			next[i] = res[i]
		}
		if i < len(need) {
			next[i] -= need[i]
		}
		if i < len(gain) {
			next[i] += gain[i]
		}
	}
	return next
}

// key encodes a full state for map use. The encoding is unambiguous
// even when the string contains separator characters.
func stateKey(st State) string {
	var b strings.Builder
	b.WriteString(strconv.Quote(st.Str))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(st.Ctrl))
	for _, v := range st.Res {
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(v))
	}
	return b.String()
}

// equalStates reports whether two full states are identical.
func equalStates(a, b State) bool {
	if a.Str != b.Str || a.Ctrl != b.Ctrl || len(a.Res) != len(b.Res) {
		return false
	}
	for i := range a.Res {
		if a.Res[i] != b.Res[i] {
			return false
		}
	}
	return true
}
