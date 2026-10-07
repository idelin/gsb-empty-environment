// Package plancmp compares an old, partially executed plan against a new
// plan and computes the minimal set of supplementary operations needed to
// reach the new goal. Already executed operations are irreversible, and the
// variables they committed cannot be silently reset; every proposed sequence
// is validated by replay.
//
// The state space is finite: at most MaxVars boolean variables, so a State
// is a bit mask and the full space holds at most 1<<MaxVars states.
package plancmp

import "fmt"

// MaxVars is the maximum number of boolean state variables.
const MaxVars = 12

// stateMask selects the valid variable bits.
const stateMask = State(1<<MaxVars - 1)

// State is the finite plan state: bit i holds boolean variable i.
type State uint16

// Op is a single operation with dependencies, preconditions and effects.
type Op struct {
	Name    string   // unique operation name
	Deps    []string // operations that must be applied before this one
	Require State    // bits that must be 1 before applying
	Forbid  State    // bits that must be 0 before applying
	Set     State    // bits set to 1 by applying
	Clear   State    // bits cleared to 0 by applying
}

// Plan is the new target plan: its operations plus the goal condition.
type Plan struct {
	Ops         []Op
	GoalRequire State // goal bits that must be 1
	GoalForbid  State // goal bits that must be 0
	RequireAll  bool  // goal additionally requires every op to be applied
}

// Input describes the comparison problem.
type Input struct {
	Initial   State    // current state after the executed operations
	Executed  []string // already executed operations (irreversible)
	Committed State    // variables locked to their Initial values
	New       *Plan    // the new plan to reach
}

// Result is the outcome of Diff.
type Result struct {
	Reachable bool     // whether the new goal is reachable at all
	Added     []string // minimal supplementary operations, in a valid order
	Final     State    // state after applying Added to Initial
	Verified  bool     // Added passed replay verification
	Explored  []State  // complete search range, set when unreachable
}

// validate checks masks, names and dependencies of the input.
func validate(in *Input) error {
	if in.New == nil {
		return fmt.Errorf("plancmp: nil new plan")
	}
	if Initial := in.Initial; Initial&^stateMask != 0 {
		return fmt.Errorf("plancmp: initial state %#x exceeds %d variables", Initial, MaxVars)
	}
	if in.Committed&^stateMask != 0 {
		return fmt.Errorf("plancmp: committed mask %#x exceeds %d variables", in.Committed, MaxVars)
	}
	if in.New.GoalRequire&^stateMask != 0 || in.New.GoalForbid&^stateMask != 0 {
		return fmt.Errorf("plancmp: goal masks exceed %d variables", MaxVars)
	}
	if len(in.New.Ops) > 64 {
		return fmt.Errorf("plancmp: at most 64 operations supported, got %d", len(in.New.Ops))
	}
	seen := make(map[string]bool, len(in.New.Ops))
	for _, op := range in.New.Ops {
		if op.Name == "" {
			return fmt.Errorf("plancmp: operation with empty name")
		}
		if seen[op.Name] {
			return fmt.Errorf("plancmp: duplicate operation %q", op.Name)
		}
		seen[op.Name] = true
		if op.Require&op.Forbid != 0 {
			return fmt.Errorf("plancmp: operation %q has contradictory preconditions", op.Name)
		}
		if op.Set&op.Clear != 0 {
			return fmt.Errorf("plancmp: operation %q sets and clears the same variable", op.Name)
		}
		if (op.Require|op.Forbid|op.Set|op.Clear)&^stateMask != 0 {
			return fmt.Errorf("plancmp: operation %q uses more than %d variables", op.Name, MaxVars)
		}
	}
	executed := make(map[string]bool, len(in.Executed))
	for _, name := range in.Executed {
		executed[name] = true
	}
	for _, op := range in.New.Ops {
		for _, dep := range op.Deps {
			if !seen[dep] && !executed[dep] {
				return fmt.Errorf("plancmp: operation %q depends on unknown operation %q", op.Name, dep)
			}
		}
	}
	return nil
}

// goal reports whether state satisfies the plan's goal condition.
func (p *Plan) goal(state State) bool {
	return state&p.GoalRequire == p.GoalRequire && state&p.GoalForbid == 0
}

// appliesTo reports whether op's preconditions hold in state.
func appliesTo(op *Op, state State) bool {
	return state&op.Require == op.Require && state&op.Forbid == 0
}

// apply returns the state after op's effects.
func apply(op *Op, state State) State {
	return state&^op.Clear | op.Set
}

// respectsCommitment reports whether applying op in a state where the
// committed variables hold their initial values would leave every committed
// variable untouched. lockedOn/lockedOff are the committed bits that are
// 1/0 in the initial state.
func respectsCommitment(op *Op, lockedOn, lockedOff State) bool {
	return op.Clear&lockedOn == 0 && op.Set&lockedOff == 0
}
