package updatestrategy

import (
	"fmt"
	"math/rand"
)

// StepRecord is one executed step of the in-memory simulation.
type StepRecord struct {
	BeliefKey string
	Action    string
	Obs       Observation
	PowerLoss bool
}

// RunResult captures one concrete execution of a policy.
type RunResult struct {
	ReachedGoal bool
	Steps       []StepRecord
	Final       World
	// PowerLosses counts interruptions actually consumed; the contract is at
	// most one.
	PowerLosses int
}

// RunOnce drives policy against one concrete world. outcomeIndex selects which
// of the action's possible outcomes is taken at each step; pass a *rand.Rand
// index source via RunRandom for stochastic checks. The simulator never
// consults hidden world contents to choose actions: it only feeds public
// observations back into the policy nodes.
func RunOnce(policy *Policy, start World, pick func(action Action, choices []Outcome) int) (*RunResult, error) {
	node := policy.Root
	world := start
	res := &RunResult{Final: start}
	guard := 0
	for {
		if policy.Target.Reached(world) {
			res.ReachedGoal = true
			res.Final = world
			return res, nil
		}
		if node == nil || node.IsGoal {
			return res, fmt.Errorf("policy stopped before target: %s", world.ID())
		}
		if guard++; guard > 1<<16 {
			return res, fmt.Errorf("simulation did not terminate")
		}
		choices := node.Action.Apply(world)
		idx := 0
		if pick != nil {
			idx = pick(node.Action, choices)
		}
		if idx < 0 || idx >= len(choices) {
			return res, fmt.Errorf("outcome index %d out of range for %s", idx, node.Action.Name())
		}
		oc := choices[idx]
		if oc.Obs.Kind == ObsPowerLoss {
			res.PowerLosses++
		}
		res.Steps = append(res.Steps, StepRecord{
			BeliefKey: node.Belief.key,
			Action:    node.Action.Name(),
			Obs:       oc.Obs,
			PowerLoss: oc.Obs.Kind == ObsPowerLoss,
		})
		child, ok := node.Branches[oc.Obs]
		if !ok {
			return res, fmt.Errorf("policy has no branch for observation %s after %s", oc.Obs.Kind, node.Action.Name())
		}
		node = child
		world = oc.World
	}
}

// RunRandom executes n trials choosing outcomes uniformly at random.
func RunRandom(policy *Policy, start World, n int, rng *rand.Rand) ([]RunResult, error) {
	results := make([]RunResult, 0, n)
	for i := 0; i < n; i++ {
		res, err := RunOnce(policy, start, func(_ Action, choices []Outcome) int {
			return rng.Intn(len(choices))
		})
		if err != nil {
			return nil, err
		}
		results = append(results, *res)
	}
	return results, nil
}

// ExhaustiveRun explores every legal interruption path from start. Because the
// model allows at most one power loss, the search tree is finite: each action
// contributes a small fixed outcome set and the power-loss branch disappears
// once consumed. Every leaf must reach the target while remaining bootable.
func ExhaustiveRun(policy *Policy, start World) ([]RunResult, error) {
	var out []RunResult
	type frame struct {
		node  *PolicyNode
		world World
		res   RunResult
	}
	stack := []frame{{node: policy.Root, world: start, res: RunResult{Final: start}}}
	visited := map[string]bool{}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if policy.Target.Reached(f.world) {
			f.res.ReachedGoal = true
			f.res.Final = f.world
			out = append(out, f.res)
			continue
		}
		if f.node == nil || f.node.IsGoal {
			return nil, fmt.Errorf("policy stopped before target at world %s", f.world.ID())
		}
		key := f.node.Belief.key + "::" + f.world.ID()
		if visited[key] {
			continue
		}
		visited[key] = true

		for _, oc := range f.node.Action.Apply(f.world) {
			child, ok := f.node.Branches[oc.Obs]
			if !ok {
				return nil, fmt.Errorf("missing branch for observation %s after %s", oc.Obs.Kind, f.node.Action.Name())
			}
			nr := f.res
			nr.Steps = append(append([]StepRecord(nil), f.res.Steps...), StepRecord{
				BeliefKey: f.node.Belief.key,
				Action:    f.node.Action.Name(),
				Obs:       oc.Obs,
				PowerLoss: oc.Obs.Kind == ObsPowerLoss,
			})
			if oc.Obs.Kind == ObsPowerLoss {
				nr.PowerLosses++
				if nr.PowerLosses > 1 {
					return nil, fmt.Errorf("model emitted more than one power loss")
				}
			}
			nr.Final = oc.World
			stack = append(stack, frame{node: child, world: oc.World, res: nr})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no terminating execution found")
	}
	return out, nil
}

// WorstObserved returns the largest step count across exhaustive results,
// which must equal Policy.WorstOps when every path has been enumerated.
func WorstObserved(results []RunResult) int {
	worst := 0
	for _, r := range results {
		if len(r.Steps) > worst {
			worst = len(r.Steps)
		}
	}
	return worst
}
