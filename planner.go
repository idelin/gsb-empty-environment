// Package netsec provides a finite-state planner for host hardening
// sequences. Settings are boolean flags packed into a bitmask; actions
// carry preconditions and atomic effects; safety invariants and the
// "at least one management channel" property must hold in every state
// of the plan, including all intermediate states.
package netsec

import "fmt"

// MaxSettings is the maximum number of boolean settings per problem.
const MaxSettings = 18

// State is a bitmask of boolean settings; bit i is setting i.
type State uint32

// Bit returns a State with only setting i set.
func Bit(i int) State { return State(1) << uint(i) }

// Action is a state transition with preconditions and atomic effects.
// All RequireAll bits must be 1 and all RequireNone bits must be 0 for
// the action to be enabled. When applied, Set bits become 1 and Clear
// bits become 0 in a single atomic step; Set and Clear must not overlap.
type Action struct {
	Name        string
	RequireAll  State
	RequireNone State
	Set         State
	Clear       State
}

// Enabled reports whether the action's preconditions hold in s.
func (a Action) Enabled(s State) bool {
	return s&a.RequireAll == a.RequireAll && s&a.RequireNone == 0
}

// Apply returns the successor state produced by the atomic effects.
func (a Action) Apply(s State) State { return (s | a.Set) &^ a.Clear }

// Invariant is a named safety predicate that must hold in every state
// of a plan: the initial state, every intermediate state, and the goal.
type Invariant struct {
	Name string
	Hold func(State) bool
}

// AtLeastOne builds an invariant requiring at least one of the given
// bits to be set. Use it for the "at least one management channel"
// property by passing the bits of every usable channel.
func AtLeastOne(name string, bits State) Invariant {
	return Invariant{Name: name, Hold: func(s State) bool { return s&bits != 0 }}
}

// NeverAll builds an invariant forbidding all of the given bits from
// being set simultaneously. Use it to ban dangerous combinations such
// as an exposed debug entry.
func NeverAll(name string, bits State) Invariant {
	return Invariant{Name: name, Hold: func(s State) bool { return s&bits != bits }}
}

// Problem is a complete finite-state planning problem.
type Problem struct {
	// NumSettings is the number of boolean settings, at most MaxSettings.
	NumSettings int
	Initial     State
	// GoalAll must all be 1 and GoalNone must all be 0 in a goal state.
	GoalAll  State
	GoalNone State
	Actions  []Action
	// Invariants must hold in every state the plan passes through.
	Invariants []Invariant
}

func (p *Problem) validMask() State {
	return State(1)<<uint(p.NumSettings) - 1
}

func (p *Problem) validate() error {
	if p.NumSettings < 1 || p.NumSettings > MaxSettings {
		return fmt.Errorf("netsec: NumSettings %d out of range [1,%d]", p.NumSettings, MaxSettings)
	}
	mask := p.validMask()
	if p.Initial&^mask != 0 || p.GoalAll&^mask != 0 || p.GoalNone&^mask != 0 {
		return fmt.Errorf("netsec: state bits outside %d settings", p.NumSettings)
	}
	for i, a := range p.Actions {
		if a.Name == "" {
			return fmt.Errorf("netsec: action %d has no name", i)
		}
		bits := a.RequireAll | a.RequireNone | a.Set | a.Clear
		if bits&^mask != 0 {
			return fmt.Errorf("netsec: action %q uses bits outside %d settings", a.Name, p.NumSettings)
		}
		if a.Set&a.Clear != 0 {
			return fmt.Errorf("netsec: action %q sets and clears the same bit", a.Name)
		}
	}
	return nil
}

func (p *Problem) invariantsHold(s State) (bool, string) {
	for _, inv := range p.Invariants {
		if !inv.Hold(s) {
			return false, inv.Name
		}
	}
	return true, ""
}

func (p *Problem) isGoal(s State) bool {
	return s&p.GoalAll == p.GoalAll && s&p.GoalNone == 0
}

// Step is one action of a plan plus the state it lands in.
type Step struct {
	Action string
	After  State
}

// Plan is a shortest sequence of actions from Initial to a goal state.
type Plan struct {
	Steps []Step
}

// Evidence explains unreachability when Solve cannot find a plan. It is
// derived from the complete set of states reachable from Initial through
// invariant-preserving transitions, so it constitutes a proof: no plan
// can exist that never violates an invariant.
type Evidence struct {
	// StatesExplored is the size of the fully explored reachable set.
	StatesExplored int
	// GoalAllMissing are goal bits required to be 1 that are 0 in every
	// reachable state.
	GoalAllMissing State
	// GoalNoneStuck are goal bits required to be 0 that are 1 in every
	// reachable state.
	GoalNoneStuck State
}

func (e *Evidence) String() string {
	return fmt.Sprintf("unreachable after exploring %d states: goal bits never set=%#05x, goal bits never cleared=%#05x",
		e.StatesExplored, uint32(e.GoalAllMissing), uint32(e.GoalNoneStuck))
}

// Solve searches for a shortest plan from Initial to a goal state using
// breadth-first search over the finite state space. Every state entered
// satisfies all invariants, so the returned plan keeps safety properties
// and the management channel intact throughout. If no goal state is
// reachable, Solve returns nil plan and an Evidence proving
// unreachability over the full reachable state space.
// edge records how BFS first reached a state: its predecessor and the
// index of the action used, or -1 for the initial state.
type edge struct {
	prev   State
	action int
}

func Solve(p *Problem) (*Plan, *Evidence, error) {
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	if ok, name := p.invariantsHold(p.Initial); !ok {
		return nil, nil, fmt.Errorf("netsec: initial state violates invariant %q", name)
	}

	prev := map[State]edge{p.Initial: {prev: p.Initial, action: -1}}
	queue := []State{p.Initial}
	var union, intersection State = 0, ^State(0)

	for head := 0; head < len(queue); head++ {
		s := queue[head]
		union |= s
		intersection &= s
		if p.isGoal(s) {
			return buildPlan(p, prev, s), nil, nil
		}
		for i, a := range p.Actions {
			if !a.Enabled(s) {
				continue
			}
			next := a.Apply(s)
			if _, seen := prev[next]; seen {
				continue
			}
			if ok, _ := p.invariantsHold(next); !ok {
				continue
			}
			prev[next] = edge{prev: s, action: i}
			queue = append(queue, next)
		}
	}

	ev := &Evidence{
		StatesExplored: len(prev),
		GoalAllMissing: p.GoalAll &^ union,
		GoalNoneStuck:  p.GoalNone & intersection,
	}
	return nil, ev, nil
}

func buildPlan(p *Problem, prev map[State]edge, goal State) *Plan {
	var steps []Step
	for s := goal; ; {
		e := prev[s]
		if e.action < 0 {
			break
		}
		steps = append(steps, Step{Action: p.Actions[e.action].Name, After: s})
		s = e.prev
	}
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
	return &Plan{Steps: steps}
}

// CheckResult summarizes a full-state model check.
type CheckResult struct {
	// ReachableStates is the number of states reachable from Initial
	// through invariant-preserving transitions; all satisfy every
	// invariant.
	ReachableStates int
	// PrunedTransitions counts enabled action applications whose target
	// state would violate an invariant and is therefore unreachable.
	PrunedTransitions int
	// GoalReachable reports whether any goal state is reachable.
	GoalReachable bool
}

// Check exhaustively enumerates every state reachable from Initial via
// invariant-preserving transitions and verifies that all invariants hold
// in each of them. It is the full-state model-checking counterpart of
// Solve: because the state space has at most 2^MaxSettings states, the
// enumeration is exact, not sampled.
func Check(p *Problem) (*CheckResult, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if ok, name := p.invariantsHold(p.Initial); !ok {
		return nil, fmt.Errorf("netsec: initial state violates invariant %q", name)
	}
	res := &CheckResult{}
	seen := map[State]bool{p.Initial: true}
	queue := []State{p.Initial}
	for head := 0; head < len(queue); head++ {
		s := queue[head]
		res.ReachableStates++
		if ok, name := p.invariantsHold(s); !ok {
			return nil, fmt.Errorf("netsec: invariant %q violated in reachable state %#05x", name, uint32(s))
		}
		if p.isGoal(s) {
			res.GoalReachable = true
		}
		for _, a := range p.Actions {
			if !a.Enabled(s) {
				continue
			}
			next := a.Apply(s)
			if ok, _ := p.invariantsHold(next); !ok {
				res.PrunedTransitions++
				continue
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return res, nil
}

// VerifyPlan replays a plan against the problem, checking every
// precondition, atomic effect, invariant, and the final goal. It returns
// an error describing the first discrepancy.
func VerifyPlan(p *Problem, plan *Plan) error {
	if err := p.validate(); err != nil {
		return err
	}
	byName := make(map[string]Action, len(p.Actions))
	for _, a := range p.Actions {
		byName[a.Name] = a
	}
	s := p.Initial
	if ok, name := p.invariantsHold(s); !ok {
		return fmt.Errorf("netsec: initial state violates invariant %q", name)
	}
	for i, step := range plan.Steps {
		a, found := byName[step.Action]
		if !found {
			return fmt.Errorf("netsec: step %d: unknown action %q", i, step.Action)
		}
		if !a.Enabled(s) {
			return fmt.Errorf("netsec: step %d: preconditions of %q not met in state %#05x", i, a.Name, uint32(s))
		}
		s = a.Apply(s)
		if step.After != s {
			return fmt.Errorf("netsec: step %d: %q lands in %#05x, plan claims %#05x", i, a.Name, uint32(s), uint32(step.After))
		}
		if ok, name := p.invariantsHold(s); !ok {
			return fmt.Errorf("netsec: step %d: state after %q violates invariant %q", i, a.Name, name)
		}
	}
	if !p.isGoal(s) {
		return fmt.Errorf("netsec: final state %#05x is not a goal state", uint32(s))
	}
	return nil
}
