package netsec

import "errors"

// MaxActions bounds every reset sequence. The device host accepts at most
// twenty reset actions.
const MaxActions = 20

// Sentinel errors reported by the planner and verifier. Callers should use
// errors.Is rather than comparing messages.
var (
	// ErrPrecondition means an action was attempted while some bit in its
	// RequireZero mask was not yet BitZero.
	ErrPrecondition = errors.New("reset action precondition not satisfied")

	// ErrUnclearable means the exhaustive state search exhausted the whole
	// reachable state space without ever reaching a fully cleared register.
	// This proves no legal reset sequence exists.
	ErrUnclearable = errors.New("secret bits cannot all be cleared by the available actions")

	// ErrLimitExceeded means every reaching path requires more than
	// MaxActions steps.
	ErrLimitExceeded = errors.New("reset sequence exceeds the maximum number of actions")

	// ErrInvalidState means an input abstract state or derived register width
	// is invalid.
	ErrInvalidState = errors.New("invalid abstract register state")

	// ErrNotCleared means a Plan trace does not finish with every secret bit
	// in BitZero.
	ErrNotCleared = errors.New("final register state is not fully cleared")

	// ErrTrace means the recorded per-step states are inconsistent with
	// replaying the actions.
	ErrTrace = errors.New("recorded step states do not match the actions")
)

// Action is one reset operation over the abstract register state.
//
//   - RequireZero is the precondition mask: the action is legal only when all
//     of these bits are already BitZero;
//   - Clear is the mask of bits the action deterministically drives to BitZero;
//   - Taint is the mask of bits that become BitUnknown again when the action
//     runs.
//
// When Clear and Taint name the same bit, Taint wins: the bit ends up
// BitUnknown because re-unknowning is the final effect of the action.
type Action struct {
	Name        string
	RequireZero Mask
	Clear       Mask
	Taint       Mask
}

// Step records one executed action together with the register state
// immediately before and after it.
type Step struct {
	// ActionIndex identifies the action in the table passed to the planner.
	ActionIndex int

	// Action is the operation executed at this step.
	Action Action

	// Before is the abstract register state on entry to the action.
	Before Mask

	// After is the abstract register state on exit from the action.
	After Mask

	// BeforeBits and AfterBits give the per-bit view of Before and After,
	// indexed by bit position.
	BeforeBits []BitState
	AfterBits  []BitState
}

// Plan is a legal reset sequence with the full per-step bit-state trace.
type Plan struct {
	// Width is the number of secret register bits that were planned over.
	Width int

	// Initial is the starting abstract register state.
	Initial Mask

	// Steps is the ordered action sequence. Its length never exceeds
	// MaxActions. A zero-length plan means the initial state was already
	// fully cleared.
	Steps []Step

	// Final is the end state; every bit below Width is guaranteed BitZero.
	Final Mask
}

// PlanReset searches for the shortest legal action sequence that clears every
// secret bit starting from the fully unknown state, the conservative reset
// assumption. It returns ErrUnclearable when no reaching state exists and
// ErrLimitExceeded when reaching the goal needs more than MaxActions steps.
func PlanReset(actions []Action) (*Plan, error) {
	return plan(actions, 0, deriveWidth(actions, 0))
}

// PlanResetFrom searches from an explicit per-bit initial state, which is
// useful when some bits are already known clean. It returns ErrInvalidState
// for an invalid bit value or when more than 64 secret bits are involved.
func PlanResetFrom(actions []Action, initial []BitState) (*Plan, error) {
	for _, bit := range initial {
		if bit != BitUnknown && bit != BitZero {
			return nil, ErrInvalidState
		}
	}
	state, err := Pack(initial)
	if err != nil {
		return nil, err
	}
	width := deriveWidth(actions, len(initial))
	return plan(actions, state, width)
}

// searchNode is one breadth-first-search vertex. Following parent links
// reconstructs the shortest legal path to the node.
type searchNode struct {
	state  Mask
	parent *searchNode
	action int
	depth  int
}

// plan performs the full-state breadth-first search. BFS expands states in
// non-decreasing depth and each transition is checked against the action
// precondition, so the first goal found uses the shortest legal sequence. The
// visited set bounds the search to the finite abstract state space, so an
// exhausted queue is a proof that no reaching sequence exists at all.
func plan(actions []Action, initial Mask, width int) (*Plan, error) {
	if width > 64 {
		return nil, ErrInvalidState
	}

	var goal Mask
	for index := 0; index < width; index++ {
		goal |= 1 << uint(index)
	}
	initial &= goal
	if initial == goal {
		return buildPlan(actions, initial, width, nil), nil
	}

	root := &searchNode{state: initial, action: -1}
	visited := map[Mask]struct{}{initial: {}}
	queue := []*searchNode{root}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for index, action := range actions {
			if !action.Legal(current.state) {
				continue
			}
			next := action.Apply(current.state) & goal
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			successor := &searchNode{
				state:  next,
				parent: current,
				action: index,
				depth:  current.depth + 1,
			}
			if next == goal {
				if successor.depth > MaxActions {
					return nil, ErrLimitExceeded
				}
				return buildPlan(actions, initial, width, successor), nil
			}
			queue = append(queue, successor)
		}
	}

	return nil, ErrUnclearable
}

// buildPlan replays the BFS chain to materialize the per-step bit-state trace.
// A nil goal node yields the zero-step plan for an already cleared register.
func buildPlan(actions []Action, initial Mask, width int, goalNode *searchNode) *Plan {
	var chain []*searchNode
	for node := goalNode; node != nil && node.action >= 0; node = node.parent {
		chain = append(chain, node)
	}

	steps := make([]Step, 0, len(chain))
	state := initial
	for index := len(chain) - 1; index >= 0; index-- {
		node := chain[index]
		actionIndex := node.action
		action := actions[actionIndex]
		after := node.state
		steps = append(steps, Step{
			ActionIndex: actionIndex,
			Action:      action,
			Before:      state,
			After:       after,
			BeforeBits:  States(state, width),
			AfterBits:   States(after, width),
		})
		state = after
	}

	return &Plan{
		Width:   width,
		Initial: initial,
		Steps:   steps,
		Final:   state,
	}
}

// deriveWidth determines how many secret bits must be planned over. It is the
// widest bit position referenced by any action mask or implied by the initial
// state, rounded up to a count of bits.
func deriveWidth(actions []Action, initialWidth int) int {
	width := initialWidth
	widen := func(mask Mask) {
		for bit := 63; bit >= 0; bit-- {
			if mask&(1<<uint(bit)) != 0 {
				if bit+1 > width {
					width = bit + 1
				}
				return
			}
		}
	}
	for _, action := range actions {
		widen(action.RequireZero)
		widen(action.Clear)
		widen(action.Taint)
	}
	return width
}

// Verify independently re-simulates the plan against the supplied action
// table and starting state, re-checking every precondition and each recorded
// transition, then confirms that all secret bits finish as BitZero. It is the
// host-side check of the produced proof.
func (p *Plan) Verify(actions []Action, initial Mask) error {
	if p == nil {
		return ErrTrace
	}
	state := initial
	for _, step := range p.Steps {
		if step.ActionIndex < 0 || step.ActionIndex >= len(actions) {
			return ErrTrace
		}
		action := actions[step.ActionIndex]
		if action != step.Action {
			return ErrTrace
		}
		if step.Before != state {
			return ErrTrace
		}
		next, err := action.Step(state)
		if err != nil {
			return err
		}
		if step.After != next {
			return ErrTrace
		}
		state = next
	}

	var goal Mask
	for index := 0; index < p.Width; index++ {
		goal |= 1 << uint(index)
	}
	if state&goal != goal {
		return ErrNotCleared
	}
	return nil
}
