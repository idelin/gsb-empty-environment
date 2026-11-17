package netsec

import (
	"fmt"
)

// Status describes how a planning call finished.
type Status string

const (
	// StatusComplete means a shortest plan was found.
	StatusComplete Status = "complete"
	// StatusIncomplete means a budget stopped the search before it could
	// either find the target or prove that the target is unreachable.
	StatusIncomplete Status = "incomplete"
	// StatusUnreachable means the bounded search completed and no safe
	// sequence reaches the target.
	StatusUnreachable Status = "unreachable"

	// StatusImpossible is an alias for StatusUnreachable.
	StatusImpossible = StatusUnreachable
	// StatusBudgetExhausted is an alias for StatusIncomplete.
	StatusBudgetExhausted = StatusIncomplete
)

// PlanResult is an alias for Result.
type PlanResult = Result

// Step is one write in a plan.
type Step struct {
	// Op is the index of the operation in Problem.Ops.
	Op   int
	Name string
	From uint32
	To   uint32
}

// Result is returned by Plan.
//
// Steps, States and Plan are populated only when Status is StatusComplete.
// BestSteps, BestStates and BestPlan describe the most promising safe prefix
// seen before an incomplete search stopped.
type Result struct {
	Status Status

	Steps  []Step
	States []uint32
	Plan   []int

	BestState  uint32
	BestSteps  []Step
	BestStates []uint32
	BestPlan   []int

	Explored int
	Visited  int
	Reason   string

	// Truncated is true when a budget prevented a complete search.
	Truncated bool
}

// Complete reports whether r contains a complete shortest plan.
func (r Result) Complete() bool {
	return r.Status == StatusComplete
}

// Found is an alias for Complete, provided for readability at call sites.
func (r Result) Found() bool {
	return r.Complete()
}

// BudgetExhausted reports whether a budget prevented a complete search.
func (r Result) BudgetExhausted() bool {
	return r.Status == StatusIncomplete
}

// Len returns the number of writes in a complete plan.
func (r Result) Len() int {
	return len(r.Steps)
}

// Verify re-simulates a complete result against p.
func (r Result) Verify(p Problem) error {
	if !r.Complete() {
		return fmt.Errorf("%w: status is %s", ErrIncomplete, r.Status)
	}
	_, err := Simulate(p, r.Plan)
	return err
}

type parentLink struct {
	prev uint32
	op   int
}

// Plan finds a shortest safe sequence of writes from Problem.Initial to the
// goal. It uses breadth-first search over register states, so the first goal
// state discovered has a minimum number of writes.
//
// Both MaxSteps and MaxStates are hard budgets. If either is exhausted before
// the search finishes, Plan returns StatusIncomplete and a best-effort safe
// prefix in BestSteps.
func Plan(p Problem) (Result, error) {
	n, err := p.normalize()
	if err != nil {
		return Result{}, err
	}
	if n.forbidden(n.problem.Initial) {
		return Result{
			Status:    StatusUnreachable,
			Reason:    "initial state is forbidden",
			BestState: n.problem.Initial,
		}, nil
	}
	if n.forbidden(n.problem.Target) && !n.partialGoal() {
		return Result{
			Status:    StatusUnreachable,
			Reason:    "target state is forbidden",
			BestState: n.problem.Initial,
		}, nil
	}
	if n.goal(n.problem.Initial) {
		return Result{
			Status:    StatusComplete,
			States:    []uint32{n.problem.Initial},
			BestState: n.problem.Initial,
		}, nil
	}

	initial := n.problem.Initial
	parents := map[uint32]parentLink{
		initial: {prev: initial, op: -1},
	}
	frontier := []uint32{initial}
	explored := 0
	depth := 0
	bestState := initial
	bestDistance := n.goalDistance(initial)
	bestDepth := 0

	incomplete := func(reason string) Result {
		bestSteps, bestStates, bestPlan := reconstruct(bestState, initial, parents, n.problem.Ops)
		return Result{
			Status:     StatusIncomplete,
			BestState:  bestState,
			BestSteps:  bestSteps,
			BestStates: bestStates,
			BestPlan:   bestPlan,
			Explored:   explored,
			Visited:    len(parents),
			Reason:     reason,
			Truncated:  true,
		}
	}

	for len(frontier) > 0 {
		if p.MaxSteps > 0 && depth >= p.MaxSteps {
			return incomplete(fmt.Sprintf("write budget %d exhausted", p.MaxSteps)), nil
		}

		nextFrontier := make([]uint32, 0, len(frontier))
		stateBudgetHit := false
		for _, state := range frontier {
			explored++

			for i, op := range n.problem.Ops {
				next, ok := applyOp(state, op, n.locks, n.problem.Locks, n.globalFreeze)
				if !ok || next == state {
					continue
				}
				if n.forbidden(next) {
					continue
				}
				if _, seen := parents[next]; seen {
					continue
				}

				// Check the goal before enforcing the memory budget: a host
				// that sets MaxStates exactly one below the number needed
				// should still get the final write rather than a false
				// incomplete result.
				link := parentLink{prev: state, op: i}
				if n.goal(next) {
					parents[next] = link
					steps, states, plan := reconstruct(next, initial, parents, n.problem.Ops)
					return Result{
						Status:    StatusComplete,
						Steps:     steps,
						States:    states,
						Plan:      plan,
						BestState: next,
						Explored:  explored,
						Visited:   len(parents),
					}, nil
				}

				if p.MaxStates > 0 && len(parents) >= p.MaxStates {
					// The state cannot be remembered, but another operation
					// from this same frontier may still reach the goal
					// directly. Keep checking before declaring incomplete.
					stateBudgetHit = true
					continue
				}
				parents[next] = link
				nextDepth := depth + 1
				if d := n.goalDistance(next); d < bestDistance || (d == bestDistance && nextDepth > bestDepth) {
					bestState = next
					bestDistance = d
					bestDepth = nextDepth
				}
				nextFrontier = append(nextFrontier, next)
			}
		}
		if stateBudgetHit {
			return incomplete(fmt.Sprintf("state budget %d exhausted", p.MaxStates)), nil
		}
		frontier = nextFrontier
		depth++
	}

	bestSteps, bestStates, bestPlan := reconstruct(bestState, initial, parents, n.problem.Ops)
	return Result{
		Status:     StatusUnreachable,
		BestState:  bestState,
		BestSteps:  bestSteps,
		BestStates: bestStates,
		BestPlan:   bestPlan,
		Explored:   explored,
		Visited:    len(parents),
		Reason:     "no safe sequence reaches the goal",
	}, nil
}

// Solve is an alias for Plan.
func Solve(p Problem) (Result, error) {
	return Plan(p)
}

// FindPlan is an alias for Plan.
func FindPlan(p Problem) (Result, error) {
	return Plan(p)
}

// ShortestPlan is an alias for Plan.
func ShortestPlan(p Problem) (Result, error) {
	return Plan(p)
}

func reconstruct(state, initial uint32, parents map[uint32]parentLink, ops []Operation) ([]Step, []uint32, []int) {
	if state == initial {
		return nil, []uint32{initial}, nil
	}

	var reverse []parentLink
	var reverseStates []uint32
	for current := state; current != initial; {
		link := parents[current]
		reverse = append(reverse, link)
		reverseStates = append(reverseStates, current)
		current = link.prev
	}

	steps := make([]Step, 0, len(reverse))
	states := make([]uint32, 0, len(reverse)+1)
	plan := make([]int, 0, len(reverse))
	states = append(states, initial)
	for i := len(reverse) - 1; i >= 0; i-- {
		link := reverse[i]
		next := reverseStates[i]
		name := ""
		if link.op >= 0 && link.op < len(ops) {
			name = ops[link.op].Name
		}
		steps = append(steps, Step{
			Op:   link.op,
			Name: name,
			From: link.prev,
			To:   next,
		})
		plan = append(plan, link.op)
		states = append(states, next)
	}
	return steps, states, plan
}
