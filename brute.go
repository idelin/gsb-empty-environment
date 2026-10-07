package policytree

import (
	"fmt"
	"math/big"
)

// BruteForce enumerates every valid policy tree for the problem and returns
// the best cost found. Unlike Solve it makes no optimal-substructure
// assumption: each tree is generated explicitly and scored independently by
// EvalTree, so it serves as a ground-truth cross-check of the dynamic
// program. It is only feasible for small instances (few worlds, actions and
// probes), because the number of trees grows extremely fast.
func BruteForce(p *Problem) (Cost, error) {
	if err := p.Validate(); err != nil {
		return Cost{}, err
	}
	full := uint64(1)<<uint(p.NumWorlds()) - 1
	var best Cost
	haveBest := false
	var err error
	genTrees(p, full, p.MaxProbes, func(tree *Node) bool {
		cost, evalErr := EvalTree(p, full, tree)
		if evalErr != nil {
			err = evalErr
			return false
		}
		if !haveBest || lessCost(cost, best) {
			best, haveBest = cost, true
		}
		return true
	})
	if err != nil {
		return Cost{}, err
	}
	if !haveBest {
		return Cost{}, fmt.Errorf("no policy trees generated")
	}
	return best, nil
}

// genTrees emits every policy tree rooted at information set info with left
// probes remaining. emit should return false to stop the enumeration early.
func genTrees(p *Problem, info uint64, left int, emit func(*Node) bool) bool {
	// Every action is a candidate terminal tree.
	for a := range p.Actions {
		if !emit(&Node{Kind: ActionNode, Action: a}) {
			return false
		}
	}
	if left == 0 {
		return true
	}
	// Every probe, combined with every combination of subtrees per outcome.
	for pr := range p.Probes {
		groups := partition(info, p.Probes[pr].Outcomes)
		outcomes := make([]string, 0, len(groups))
		for outcome := range groups {
			outcomes = append(outcomes, outcome)
		}
		// sort for determinism
		for i := 1; i < len(outcomes); i++ {
			for j := i; j > 0 && outcomes[j] < outcomes[j-1]; j-- {
				outcomes[j], outcomes[j-1] = outcomes[j-1], outcomes[j]
			}
		}
		children := make(map[string]*Node, len(outcomes))
		var combine func(i int) bool
		combine = func(i int) bool {
			if i == len(outcomes) {
				copied := make(map[string]*Node, len(children))
				for k, v := range children {
					copied[k] = v
				}
				return emit(&Node{
					Kind:     ProbeNode,
					Probe:    pr,
					Outcomes: append([]string(nil), outcomes...),
					Children: copied,
				})
			}
			outcome := outcomes[i]
			return genTrees(p, groups[outcome], left-1, func(child *Node) bool {
				children[outcome] = child
				return combine(i + 1)
			})
		}
		if !combine(0) {
			return false
		}
	}
	return true
}

// EvalTree scores a policy tree from scratch over the worlds in info,
// without any memoization or sharing assumptions.
func EvalTree(p *Problem, info uint64, tree *Node) (Cost, error) {
	switch tree.Kind {
	case ActionNode:
		if tree.Action < 0 || tree.Action >= len(p.Actions) {
			return Cost{}, fmt.Errorf("invalid action index %d", tree.Action)
		}
		s := &Solver{p: p}
		return s.actionCost(info, tree.Action), nil
	case ProbeNode:
		if tree.Probe < 0 || tree.Probe >= len(p.Probes) {
			return Cost{}, fmt.Errorf("invalid probe index %d", tree.Probe)
		}
		probe := p.Probes[tree.Probe]
		groups := partition(info, probe.Outcomes)
		worst := int64(0)
		first := true
		exp := new(big.Rat).SetInt64(probe.Cost)
		denom := weightOf(p, info)
		for outcome, sub := range groups {
			child, ok := tree.Children[outcome]
			if !ok {
				return Cost{}, fmt.Errorf("probe %s: missing subtree for outcome %q", probe.Name, outcome)
			}
			childCost, err := EvalTree(p, sub, child)
			if err != nil {
				return Cost{}, err
			}
			if first || childCost.Worst > worst {
				worst = childCost.Worst
			}
			first = false
			share := new(big.Rat).Quo(weightOf(p, sub), denom)
			exp.Add(exp, share.Mul(share, childCost.Exp))
		}
		worst += probe.Cost
		return Cost{Worst: worst, Exp: exp}, nil
	}
	return Cost{}, fmt.Errorf("unknown node kind %d", tree.Kind)
}
