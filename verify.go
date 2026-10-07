package dectree

import (
	"fmt"
	"math/big"
	"math/bits"
)

// Simulate walks candidate cand through the strategy tree rooted at root and
// returns the total fee paid and the leaf reached. The test applied at each
// step is dictated solely by the tree (a function of past observations);
// only the test outcome is read from the candidate identity. A strategy that
// could only proceed by knowing the true object in advance has no tree
// representation and is rejected here as a missing branch.
func Simulate(p *Problem, root *Node, cand int) (cost int, leaf []int, err error) {
	if cand < 0 || cand >= len(p.Candidates) {
		return 0, nil, fmt.Errorf("dectree: candidate index %d out of range", cand)
	}
	node := root
	for {
		if node == nil {
			return 0, nil, fmt.Errorf("dectree: nil node reached")
		}
		if node.IsLeaf() {
			return cost, node.Leaf, nil
		}
		if node.Test >= len(p.Tests) {
			return 0, nil, fmt.Errorf("dectree: invalid test index %d", node.Test)
		}
		t := p.Tests[node.Test]
		cost += t.Cost
		outcome := t.Outcome[cand]
		next := node.Branches[outcome]
		if next == nil {
			return 0, nil, fmt.Errorf("dectree: candidate %d produced outcome %q of test %q with no branch; a strategy must be defined for every truth consistent with its observations",
				cand, outcome, t.Name)
		}
		node = next
	}
}

// Verify checks that sol is a valid and optimal strategy for p:
//
//  1. Structure: every internal node applies one test to every candidate
//     that reaches it, so test choice depends only on past observations,
//     never on the unknown true object.
//  2. Budget: every root-to-leaf path spends at most p.Budget.
//  3. Identification: every leaf is a singleton, or a group of candidates
//     that no test affordable within the remaining path budget can tell
//     apart.
//  4. Honesty: sol.WorstCost and sol.Expected match a fresh simulation of
//     every candidate.
//  5. Optimality: the costs equal the optimum found by exhaustive
//     enumeration of all strategy trees.
func Verify(p *Problem, sol *Solution) error {
	if err := p.validate(); err != nil {
		return err
	}
	if sol == nil || sol.Root == nil {
		return fmt.Errorf("dectree: nil solution")
	}
	n := len(p.Candidates)
	full := uint16(1<<uint(n)) - 1

	if err := checkStructure(p, sol.Root, full); err != nil {
		return err
	}

	worst := 0
	exp := new(big.Rat)
	for i := 0; i < n; i++ {
		cost, _, err := Simulate(p, sol.Root, i)
		if err != nil {
			return err
		}
		if cost > worst {
			worst = cost
		}
		exp.Add(exp, new(big.Rat).Mul(p.Candidates[i].Prob, big.NewRat(int64(cost), 1)))
	}
	exp.Quo(exp, sumProb(p, full))
	if worst != sol.WorstCost {
		return fmt.Errorf("dectree: claimed worst cost %d, simulation gives %d", sol.WorstCost, worst)
	}
	if sol.Expected == nil || exp.Cmp(sol.Expected) != 0 {
		return fmt.Errorf("dectree: claimed expected cost %v, simulation gives %v", sol.Expected, exp)
	}

	optWorst, optExp := exhaustiveOptimum(p, full, p.Budget)
	if worst > optWorst || (worst == optWorst && exp.Cmp(optExp) > 0) {
		return fmt.Errorf("dectree: strategy (worst=%d, expected=%s) is not optimal; exhaustive enumeration of all strategy trees gives (worst=%d, expected=%s)",
			worst, exp.RatString(), optWorst, optExp.RatString())
	}
	return nil
}

// checkStructure walks the tree, replaying every candidate set consistent
// with the observations, and enforces structure, budget, and leaf validity.
func checkStructure(p *Problem, root *Node, full uint16) error {
	seen := map[*Node]uint16{}
	type frame struct {
		node *Node
		set  uint16
		cost int
	}
	stack := []frame{{node: root, set: full, cost: 0}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.node == nil {
			return fmt.Errorf("dectree: nil node in tree")
		}
		if prev, ok := seen[f.node]; ok {
			if prev != f.set {
				return fmt.Errorf("dectree: same node reached with different candidate sets")
			}
			continue
		}
		seen[f.node] = f.set

		if f.node.IsLeaf() {
			if len(f.node.Branches) != 0 {
				return fmt.Errorf("dectree: leaf node carries branches")
			}
			if bitSet(f.node.Leaf) != f.set {
				return fmt.Errorf("dectree: leaf reports candidates %v, simulation arrives with %v",
					f.node.Leaf, setMembers(f.set))
			}
			if bits.OnesCount16(f.set) > 1 {
				if separableWithin(p, f.set, p.Budget-f.cost) {
					return fmt.Errorf("dectree: leaf %v is separable within the remaining budget",
						f.node.Leaf)
				}
			}
			continue
		}

		if len(f.node.Leaf) != 0 {
			return fmt.Errorf("dectree: internal node carries leaf candidates")
		}
		ti := f.node.Test
		if ti < 0 || ti >= len(p.Tests) {
			return fmt.Errorf("dectree: invalid test index %d", ti)
		}
		cost := f.cost + p.Tests[ti].Cost
		if cost > p.Budget {
			return fmt.Errorf("dectree: path cost %d exceeds budget %d at test %q",
				cost, p.Budget, p.Tests[ti].Name)
		}
		outcomes, groups, ok := partition(p, f.set, ti)
		if !ok {
			return fmt.Errorf("dectree: test %q is not informative for candidates %v",
				p.Tests[ti].Name, setMembers(f.set))
		}
		if len(f.node.Branches) != len(outcomes) {
			return fmt.Errorf("dectree: node for test %q has %d branches, but %d outcomes occur",
				p.Tests[ti].Name, len(f.node.Branches), len(outcomes))
		}
		for i, o := range outcomes {
			child := f.node.Branches[o]
			if child == nil {
				return fmt.Errorf("dectree: no branch for outcome %q of test %q", o, p.Tests[ti].Name)
			}
			stack = append(stack, frame{node: child, set: groups[i], cost: cost})
		}
	}
	return nil
}
