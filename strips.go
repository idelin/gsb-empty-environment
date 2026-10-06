// Package plango implements two classical planners over boolean capabilities:
//
//  1. A deterministic STRIPS-style planner (this file): up to MaxCaps boolean
//     capabilities, actions with preconditions and add/delete effects, forbidden
//     states, and BFS for provably shortest plans. An independent Replay
//     re-checks plans step by step, including non-monotonic delete effects.
//  2. A partially observable contingent planner (belief.go) over finitely many
//     worlds with nondeterministic actions and observations.
package plango

import (
	"fmt"
	"sort"
	"strings"
)

// MaxCaps is the maximum number of boolean capabilities (bits in a State).
const MaxCaps = 12

// State is a bitmask of boolean capabilities. Bit i set means capability i holds.
type State uint16

// Cap names a single capability bit.
func Cap(i uint) State {
	if i >= MaxCaps {
		panic(fmt.Sprintf("plango: capability %d out of range (max %d)", i, MaxCaps))
	}
	return 1 << i
}

// Caps packs capability indices into a State mask.
func Caps(idxs ...uint) State {
	var s State
	for _, i := range idxs {
		s |= Cap(i)
	}
	return s
}

// Has reports whether all capabilities in mask hold in s.
func (s State) Has(mask State) bool { return s&mask == mask }

// List returns the indices of the capabilities that hold in s.
func (s State) List() []uint {
	var out []uint
	for i := uint(0); i < MaxCaps; i++ {
		if s&Cap(i) != 0 {
			out = append(out, i)
		}
	}
	return out
}

func (s State) String() string {
	parts := make([]string, 0, MaxCaps)
	for _, i := range s.List() {
		parts = append(parts, fmt.Sprintf("c%d", i))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// Action is a deterministic STRIPS operator with add and delete effects.
// Delete effects are applied before add effects, so a capability listed in
// both ends up set; this keeps the semantics total and non-monotonic.
type Action struct {
	Name string
	Pre  State // capabilities that must all hold
	Add  State // capabilities set by the action
	Del  State // capabilities cleared by the action (non-monotonic)
}

// Applicable reports whether the action's preconditions hold in s.
func (a Action) Applicable(s State) bool { return s.Has(a.Pre) }

// Apply returns the successor state: deletes first, then adds.
func (a Action) Apply(s State) State { return s&^a.Del | a.Add }

// Problem is a deterministic planning instance.
type Problem struct {
	Actions []Action
	Initial State
	Goal    State // capabilities that must all hold
	// Forbidden lists masks; any state containing all bits of a mask is
	// forbidden and may never be entered (including the initial state).
	Forbidden []State
}

// Blocked reports whether s touches any forbidden mask.
func (p Problem) Blocked(s State) bool {
	for _, m := range p.Forbidden {
		if s.Has(m) {
			return true
		}
	}
	return false
}

// Plan is a step-by-step trajectory: States[0] is the initial state and
// States[i+1] == Actions[i].Apply(States[i]).
type Plan struct {
	Actions []Action
	States  []State
}

// Steps renders the plan as one line per step with the capability set.
func (pl Plan) Steps() []string {
	out := make([]string, 0, len(pl.States))
	out = append(out, fmt.Sprintf("step 0: %s (initial)", pl.States[0]))
	for i, a := range pl.Actions {
		out = append(out, fmt.Sprintf("step %d: %s --%s--> %s", i+1, pl.States[i], a.Name, pl.States[i+1]))
	}
	return out
}

// Solve finds a shortest action sequence from Initial to any state satisfying
// Goal without touching a forbidden state, using breadth-first search.
// If Initial already satisfies Goal it returns an empty plan (no actions).
// The boolean result reports whether a plan exists.
func Solve(p Problem) (Plan, bool) {
	if p.Blocked(p.Initial) {
		return Plan{}, false
	}
	if p.Initial.Has(p.Goal) {
		return Plan{States: []State{p.Initial}}, true
	}
	nodes := []node{{parent: -1, action: -1, state: p.Initial}}
	seen := map[State]bool{p.Initial: true}
	for head := 0; head < len(nodes); head++ {
		cur := nodes[head]
		for ai, a := range p.Actions {
			if !a.Applicable(cur.state) {
				continue
			}
			next := a.Apply(cur.state)
			if p.Blocked(next) || seen[next] {
				continue
			}
			seen[next] = true
			nodes = append(nodes, node{parent: head, action: ai, state: next})
			if next.Has(p.Goal) {
				return reconstruct(p, nodes, len(nodes)-1), true
			}
		}
	}
	return Plan{}, false
}

type node struct {
	parent int // index into nodes, -1 for root
	action int // action taken from parent, -1 for root
	state  State
}

func reconstruct(p Problem, nodes []node, goal int) Plan {
	var rev []int
	for at := goal; at != -1; at = nodes[at].parent {
		rev = append(rev, at)
	}
	plan := Plan{States: make([]State, 0, len(rev))}
	for i := len(rev) - 1; i >= 0; i-- {
		n := nodes[rev[i]]
		plan.States = append(plan.States, n.state)
		if n.action >= 0 {
			plan.Actions = append(plan.Actions, p.Actions[n.action])
		}
	}
	return plan
}

// Replay independently re-executes actions from p.Initial, checking
// applicability, forbidden states, delete/add semantics and the goal at
// every step. It is the cross-check for Solve and deliberately shares no
// search state with it, so non-monotonic deletions are verified twice.
func Replay(p Problem, actions []Action) (Plan, error) {
	s := p.Initial
	if p.Blocked(s) {
		return Plan{}, fmt.Errorf("initial state %s is forbidden", s)
	}
	plan := Plan{States: []State{s}}
	for i, a := range actions {
		if !a.Applicable(s) {
			return Plan{}, fmt.Errorf("step %d: action %q not applicable in %s (needs %s)", i, a.Name, s, a.Pre)
		}
		s = a.Apply(s)
		if p.Blocked(s) {
			return Plan{}, fmt.Errorf("step %d: action %q enters forbidden state %s", i, a.Name, s)
		}
		plan.States = append(plan.States, s)
	}
	plan.Actions = append(plan.Actions, actions...)
	if !s.Has(p.Goal) {
		return Plan{}, fmt.Errorf("goal %s not reached: final state is %s", p.Goal, s)
	}
	return plan, nil
}

// Verify solves p and replays the result, returning the validated plan.
func Verify(p Problem) (Plan, error) {
	plan, ok := Solve(p)
	if !ok {
		return Plan{}, fmt.Errorf("no plan exists")
	}
	replayed, err := Replay(p, plan.Actions)
	if err != nil {
		return Plan{}, fmt.Errorf("replay rejected BFS plan: %v", err)
	}
	if len(replayed.States) != len(plan.States) {
		return Plan{}, fmt.Errorf("replay length mismatch: %d vs %d states", len(replayed.States), len(plan.States))
	}
	for i := range plan.States {
		if plan.States[i] != replayed.States[i] {
			return Plan{}, fmt.Errorf("replay diverges at step %d: %s vs %s", i, plan.States[i], replayed.States[i])
		}
	}
	return plan, nil
}

// SortStates is a small helper for deterministic test output.
func SortStates(ss []State) {
	sort.Slice(ss, func(i, j int) bool { return ss[i] < ss[j] })
}
