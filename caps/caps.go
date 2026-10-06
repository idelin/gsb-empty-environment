// Package caps solves deterministic capability-planning problems.
//
// A state is a set of at most MaxCaps boolean capabilities, stored as a
// bitmask. Actions have preconditions (required and forbidden capabilities)
// and effects that grant (Add) or revoke (Del) capabilities. Revocation is
// non-monotonic: a capability gained earlier may be deleted later, and
// deletes win over adds within the same action.
//
// Solve finds a shortest action sequence (BFS) that reaches the goal without
// ever entering a forbidden state. If the initial state already satisfies
// the goal, Solve returns an empty action sequence. Replay is an independent
// verifier that re-executes a plan step by step and checks preconditions,
// forbidden states, revocation semantics, and the final goal.
package caps

import "fmt"

// MaxCaps is the maximum number of boolean capabilities (bits 0..11).
const MaxCaps = 12

// Set is a bitmask of capabilities.
type Set uint16

// Cap returns the singleton Set containing capability i (0-based).
func Cap(i int) Set {
	if i < 0 || i >= MaxCaps {
		panic(fmt.Sprintf("caps: capability index %d out of range [0,%d)", i, MaxCaps))
	}
	return 1 << uint(i)
}

// Has reports whether capability i is present.
func (s Set) Has(i int) bool { return s&Cap(i) != 0 }

// Contains reports whether every capability in other is present in s.
func (s Set) Contains(other Set) bool { return s&other == other }

// List returns the indices of the capabilities present, in increasing order.
func (s Set) List() []int {
	var out []int
	for i := 0; i < MaxCaps; i++ {
		if s.Has(i) {
			out = append(out, i)
		}
	}
	return out
}

func (s Set) String() string {
	l := s.List()
	if len(l) == 0 {
		return "{}"
	}
	buf := "{"
	for i, c := range l {
		if i > 0 {
			buf += ","
		}
		buf += fmt.Sprintf("c%d", c)
	}
	return buf + "}"
}

// Action grants and revokes capabilities when its preconditions hold.
type Action struct {
	Name    string
	Require Set // capabilities that must be present
	Forbid  Set // capabilities that must be absent
	Add     Set // capabilities granted by the action
	Del     Set // capabilities revoked by the action (wins over Add)
}

// Applicable reports whether the action's preconditions hold in s.
func (a Action) Applicable(s Set) bool {
	return s.Contains(a.Require) && s&a.Forbid == 0
}

// Apply returns the successor state: adds are applied, then deletes revoke.
func (a Action) Apply(s Set) Set {
	return (s | a.Add) &^ a.Del
}

// Problem is a deterministic capability-planning instance.
type Problem struct {
	Initial   Set
	Goal      Set   // every capability in Goal must be held
	Forbidden []Set // exact states that must never be entered
	Actions   []Action
}

// Solution is a plan plus the step-by-step capability sets it traverses.
// States has length len(Actions)+1 and States[0] is the initial state.
type Solution struct {
	Actions []Action
	States  []Set
}

func (p Problem) isForbidden(s Set) bool {
	for _, f := range p.Forbidden {
		if s == f {
			return true
		}
	}
	return false
}

// Solve breadth-first searches for a shortest action sequence from
// p.Initial to any state containing p.Goal, never entering a forbidden
// state. If the initial state already satisfies the goal it returns an
// empty plan. The second return value reports whether a plan exists.
func Solve(p Problem) (Solution, bool) {
	if p.isForbidden(p.Initial) {
		return Solution{}, false
	}
	if p.Initial.Contains(p.Goal) {
		return Solution{States: []Set{p.Initial}}, true
	}
	prev := map[Set]predecessor{p.Initial: {}}

	queue := []Set{p.Initial}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, a := range p.Actions {
			if !a.Applicable(s) {
				continue
			}
			ns := a.Apply(s)
			if p.isForbidden(ns) {
				continue
			}
			if _, seen := prev[ns]; seen {
				continue
			}
			prev[ns] = predecessor{s, a}
			if ns.Contains(p.Goal) {
				return reconstruct(p.Initial, ns, prev), true
			}
			queue = append(queue, ns)
		}
	}
	return Solution{}, false
}

type predecessor struct {
	from Set
	act  Action
}

func reconstruct(initial, goal Set, prev map[Set]predecessor) Solution {
	var revActs []Action
	revStates := []Set{goal}
	for s := goal; s != initial; {
		l := prev[s]
		revActs = append(revActs, l.act)
		s = l.from
		revStates = append(revStates, s)
	}
	sol := Solution{
		Actions: make([]Action, len(revActs)),
		States:  make([]Set, len(revStates)),
	}
	for i := range revActs {
		sol.Actions[len(revActs)-1-i] = revActs[i]
	}
	for i := range revStates {
		sol.States[len(revStates)-1-i] = revStates[i]
	}
	return sol
}

// Replay independently re-executes actions against p and verifies the plan:
// every action's preconditions must hold, no forbidden state may be entered
// (including the initial state), deletes must revoke (non-monotonic effects
// are applied exactly as specified), and the final state must contain the
// goal. On success it returns the step-by-step capability sets.
func Replay(p Problem, actions []Action) (Solution, error) {
	s := p.Initial
	if p.isForbidden(s) {
		return Solution{}, fmt.Errorf("caps: initial state %v is forbidden", s)
	}
	states := []Set{s}
	for i, a := range actions {
		if !a.Applicable(s) {
			return Solution{}, fmt.Errorf("caps: step %d: action %q not applicable in state %v", i, a.Name, s)
		}
		s = a.Apply(s)
		if p.isForbidden(s) {
			return Solution{}, fmt.Errorf("caps: step %d: action %q enters forbidden state %v", i, a.Name, s)
		}
		states = append(states, s)
	}
	if !s.Contains(p.Goal) {
		return Solution{}, fmt.Errorf("caps: plan ends in %v, goal %v not satisfied", s, p.Goal)
	}
	return Solution{Actions: append([]Action(nil), actions...), States: states}, nil
}

// IsMonotonicGrowth reports whether the capability sets only ever grow.
// A plan that is not monotonic relies on revocation: some capability is
// lost at least once along the way.
func IsMonotonicGrowth(states []Set) bool {
	for i := 1; i < len(states); i++ {
		if states[i-1]&^states[i] != 0 {
			return false
		}
	}
	return true
}
