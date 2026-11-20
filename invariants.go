package updatestrategy

import (
	"fmt"
)

// InvariantViolation describes one independently detected policy defect. The
// verifier rebuilds every expected successor from the public transition model,
// so a policy hand-crafted outside Plan is checked on exactly the same terms.
type InvariantViolation struct {
	Code   InvariantCode
	Belief string
	Detail string
}

// InvariantCode names the independently checkable safety invariants.
type InvariantCode int

const (
	// InvGoalOnly: a leaf node may stop only when every world in its belief
	// satisfies the target.
	InvGoalOnly InvariantCode = iota
	// InvActionDeclared: every chosen action must be one of the host's declared
	// actions, never an invented one.
	InvActionDeclared
	// InvActionSafe: the chosen action must be safe in every world of the
	// belief (in particular it may never overwrite the only authenticated slot).
	InvActionSafe
	// InvBranchComplete: the policy must name a successor for every outcome the
	// action can produce in every world of the belief.
	InvBranchComplete
	// InvNoHiddenKnowledge: each successor belief must equal the exact set of
	// outcome worlds sharing that observation. A smaller set would mean the
	// host used slot contents it cannot observe.
	InvNoHiddenKnowledge
	// InvOfflineBootable: after every outcome the active slot must carry an
	// authenticated image, so the device always boots offline.
	InvOfflineBootable
	// InvCounterMonotonic: no edge may lower the rollback counter.
	InvCounterMonotonic
	// InvTerminating: the policy must be acyclic and its stored worst cost must
	// equal 1 + max(successor costs).
	InvTerminating
)

func (c InvariantCode) String() string {
	switch c {
	case InvGoalOnly:
		return "goal-only-termination"
	case InvActionDeclared:
		return "declared-action-only"
	case InvActionSafe:
		return "action-safe-in-all-worlds"
	case InvBranchComplete:
		return "complete-observation-branches"
	case InvNoHiddenKnowledge:
		return "no-hidden-knowledge"
	case InvOfflineBootable:
		return "offline-bootable-every-step"
	case InvCounterMonotonic:
		return "counter-monotonic"
	case InvTerminating:
		return "terminating-and-cost-consistent"
	default:
		return "unknown"
	}
}

func (v InvariantViolation) Error() string {
	return fmt.Sprintf("invariant %s violated at [%s]: %s", v.Code, v.Belief, v.Detail)
}

// VerifyPolicy independently re-derives every edge of policy and checks all
// safety invariants. actions is the exact host action set; an empty result
// means the policy is safe, complete and terminating under the model.
func VerifyPolicy(policy *Policy, actions []Action) []InvariantViolation {
	if policy == nil || policy.Root == nil {
		return []InvariantViolation{{Code: InvGoalOnly, Detail: "nil policy"}}
	}
	v := &verifier{
		actions: map[string]Action{},
		onStack: map[string]bool{},
		seen:    map[string]bool{},
	}
	for _, a := range actions {
		v.actions[a.Name()] = a
	}
	v.walk(policy.Root, policy.Target)
	return v.violations
}

type verifier struct {
	actions    map[string]Action
	onStack    map[string]bool
	seen       map[string]bool
	violations []InvariantViolation
}

func (v *verifier) add(node *PolicyNode, code InvariantCode, detail string) {
	belief := "<goal>"
	if node != nil {
		belief = node.Belief.key
	}
	v.violations = append(v.violations, InvariantViolation{Code: code, Belief: belief, Detail: detail})
}

func (v *verifier) walk(node *PolicyNode, target Target) {
	if node == nil {
		v.violations = append(v.violations, InvariantViolation{Code: InvGoalOnly, Detail: "nil policy node"})
		return
	}
	if v.seen[node.Belief.key] {
		// Re-entering a node from a second parent is fine for a DAG; only an
		// edge back into an active stack position forms a forbidden cycle.
		if v.onStack[node.Belief.key] {
			v.add(node, InvTerminating, "observation loop back into an active belief")
		}
		return
	}
	v.seen[node.Belief.key] = true
	v.onStack[node.Belief.key] = true
	defer delete(v.onStack, node.Belief.key)

	allGoal := true
	for _, w := range node.Belief.worlds {
		if !target.Reached(w) {
			allGoal = false
			break
		}
	}

	if allGoal {
		if node.Action != nil {
			v.add(node, InvGoalOnly, "goal belief must not schedule further actions")
		}
		if node.WorstCost != 0 || len(node.Branches) != 0 {
			v.add(node, InvGoalOnly, "goal node must have zero cost and no branches")
		}
		return
	}
	if node.Action == nil {
		v.add(node, InvGoalOnly, "non-goal belief has no action")
		return
	}

	declared, ok := v.actions[node.Action.Name()]
	if !ok {
		v.add(node, InvActionDeclared, "action "+node.Action.Name()+" is not in the host action set")
		return
	}
	safeAct, ok := declared.(safeAction)
	if !ok {
		v.add(node, InvActionDeclared, "action "+node.Action.Name()+" declares no safety rule")
		return
	}

	for _, w := range node.Belief.worlds {
		if err := safeAct.SafeIn(w); err != nil {
			v.add(node, InvActionSafe, fmt.Sprintf("world %s: %v", w.ID(), err))
		}
	}

	// Rebuild the expected observation partition straight from the model.
	expected := Branch(node.Belief, declared)
	if len(expected) != len(node.Branches) {
		v.add(node, InvBranchComplete, fmt.Sprintf("policy has %d branches, model requires %d", len(node.Branches), len(expected)))
	}

	maxCost := 0
	for _, g := range expected {
		child, ok := node.Branches[g.Obs]
		if !ok {
			v.add(node, InvBranchComplete, fmt.Sprintf("missing successor for observation %s", g.Obs.Kind))
			continue
		}
		if child.Belief.key != g.Belief.key {
			v.add(node, InvNoHiddenKnowledge, fmt.Sprintf("observation %s successor belief differs from the exact indistinguishable world set", g.Obs.Kind))
		}
		for _, oc := range g.Outcomes {
			if !oc.World.Bootable(oc.World.Active) {
				v.add(node, InvOfflineBootable, fmt.Sprintf("world %s leaves no authenticated active slot", oc.World.ID()))
			}
		}
		// Counter monotonicity along every concrete edge.
		for _, parent := range node.Belief.worlds {
			for _, oc := range declared.Apply(parent) {
				if oc.World.Counter < parent.Counter {
					v.add(node, InvCounterMonotonic, fmt.Sprintf("world %s -> %s lowers counter", parent.ID(), oc.World.ID()))
				}
			}
		}
		if child.WorstCost+1 > maxCost {
			maxCost = child.WorstCost + 1
		}
		v.walk(child, target)
	}
	if node.WorstCost != maxCost {
		v.add(node, InvTerminating, fmt.Sprintf("stored worst cost %d != recomputed %d", node.WorstCost, maxCost))
	}
}
