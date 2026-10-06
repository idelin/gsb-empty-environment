package dectree

import (
	"fmt"
	"math/big"
	"math/bits"
	"sort"
)

// objective is the quality measure of a strategy (sub)tree: the
// worst-case additional cost and the probability-weighted sum of
// additional costs over the candidate set it covers.
type objective struct {
	worst int64
	exp   *big.Rat
}

// Simulate plays the strategy against a known true candidate,
// returning the leaf reached and the total cost paid. The walk uses
// only the outcomes observed along the path to select the next test;
// the truth is consulted solely to evaluate test outcomes, mirroring
// a real execution where the truth is hidden. Because a Strategy is
// a plain tree, the test applied at any point is fully determined by
// previous outcomes — it can never depend on the unknown truth.
func Simulate(inst *Instance, root *Strategy, truth string) (*Strategy, int64, error) {
	byName := make(map[string]*Test, len(inst.Tests))
	for i := range inst.Tests {
		byName[inst.Tests[i].Name] = &inst.Tests[i]
	}
	node := root
	var cost int64
	for !node.Leaf {
		t, ok := byName[node.Test]
		if !ok {
			return nil, 0, fmt.Errorf("dectree: strategy uses unknown test %q", node.Test)
		}
		outcome, ok := t.Outcomes[truth]
		if !ok {
			return nil, 0, fmt.Errorf("dectree: test %q has no outcome for %q", t.Name, truth)
		}
		next, ok := node.Branches[outcome]
		if !ok {
			return nil, 0, fmt.Errorf("dectree: no branch for outcome %q of test %q", outcome, t.Name)
		}
		cost += t.Cost
		node = next
	}
	return node, cost, nil
}

// ExhaustiveVerify establishes that strat is a valid, optimal
// strategy for inst, in three stages:
//
//  1. Structural validity: every internal node applies an affordable
//     test that splits the candidate set consistent with the evidence
//     on its path; branches cover exactly the observable outcomes;
//     every leaf is justified (no affordable test could split its
//     candidate set further) and carries correct evidence.
//  2. Honesty: simulating each candidate as the hidden truth reaches
//     a leaf containing that candidate within budget. The strategy
//     being a tree, the test chosen at each step is a function of
//     observed outcomes only, never of the truth.
//  3. Optimality: all strategy trees for the instance are enumerated
//     recursively (as Pareto frontiers of achievable (worst, expected)
//     pairs per subproblem), and strat must attain the lexicographic
//     minimum of worst-case cost, then expected cost.
func ExhaustiveVerify(inst *Instance, root *Strategy) error {
	if err := inst.Validate(); err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("dectree: nil strategy")
	}
	s := newSolver(inst)
	full := uint8(1<<uint(len(inst.Candidates))) - 1

	if err := s.checkNode(root, full, inst.Budget, nil); err != nil {
		return fmt.Errorf("dectree: invalid strategy: %v", err)
	}

	for _, c := range inst.Candidates {
		leaf, cost, err := Simulate(inst, root, c.ID)
		if err != nil {
			return err
		}
		found := false
		for _, id := range leaf.Candidates {
			if id == c.ID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("dectree: truth %q reaches leaf %v which excludes it", c.ID, leaf.Candidates)
		}
		if cost > inst.Budget {
			return fmt.Errorf("dectree: truth %q pays %d, over budget %d", c.ID, cost, inst.Budget)
		}
	}

	opt := lexMin(s.frontier(full, inst.Budget))
	d := s.solve(full, inst.Budget)
	if d.worst != opt.worst || d.exp.Cmp(opt.exp) != 0 {
		return fmt.Errorf("dectree: internal solver disagrees with exhaustive search: solver (%d, %s), exhaustive (%d, %s)",
			d.worst, d.exp, opt.worst, opt.exp)
	}
	gotExp := new(big.Rat).Mul(root.Exp, s.pSum[full])
	if root.Worst != opt.worst || gotExp.Cmp(opt.exp) != 0 {
		return fmt.Errorf("dectree: strategy is not optimal: has (worst %d, exp %s), optimum is (worst %d, exp %s)",
			root.Worst, root.Exp, opt.worst, new(big.Rat).Quo(opt.exp, s.pSum[full]))
	}
	return nil
}

// checkNode validates the subtree against the candidate set
// consistent with the evidence gathered so far.
func (s *solver) checkNode(node *Strategy, mask uint8, budget int64, evidence []Step) error {
	if node == nil {
		return fmt.Errorf("nil node")
	}
	if !equalStrings(node.Candidates, s.idsOf(mask)) {
		return fmt.Errorf("node claims candidates %v, evidence implies %v", node.Candidates, s.idsOf(mask))
	}
	if !equalEvidence(node.Evidence, evidence) {
		return fmt.Errorf("node claims evidence %v, path gives %v", node.Evidence, evidence)
	}
	if node.Leaf {
		if node.Test != "" || len(node.Branches) != 0 {
			return fmt.Errorf("leaf %v carries test or branches", node.Candidates)
		}
		for ti := range s.inst.Tests {
			t := &s.inst.Tests[ti]
			if t.Cost > budget {
				continue
			}
			if _, ok := s.partition(mask, ti); ok {
				return fmt.Errorf("leaf %v is unjustified: affordable test %q still splits it",
					node.Candidates, t.Name)
			}
		}
		return nil
	}
	ti, ok := s.testIndex[node.Test]
	if !ok {
		return fmt.Errorf("unknown test %q", node.Test)
	}
	t := &s.inst.Tests[ti]
	if t.Cost > budget {
		return fmt.Errorf("test %q costs %d, only %d left in budget", t.Name, t.Cost, budget)
	}
	groups, ok := s.partition(mask, ti)
	if !ok {
		return fmt.Errorf("test %q does not split %v", t.Name, s.idsOf(mask))
	}
	if len(node.Branches) != len(groups) {
		return fmt.Errorf("test %q has %d observable outcomes, node has %d branches",
			t.Name, len(groups), len(node.Branches))
	}
	for outcome, g := range groups {
		child, ok := node.Branches[outcome]
		if !ok {
			return fmt.Errorf("missing branch for outcome %q of test %q", outcome, t.Name)
		}
		ev := append(append([]Step(nil), evidence...), Step{Test: t.Name, Outcome: outcome})
		if err := s.checkNode(child, g, budget-t.Cost, ev); err != nil {
			return err
		}
	}
	return nil
}

// frontier enumerates every strategy tree for the subproblem
// (mask, budget) and returns the Pareto frontier of achievable
// objectives, memoised per subproblem.
func (s *solver) frontier(mask uint8, budget int64) []objective {
	if bits.OnesCount8(mask) <= 1 {
		return []objective{{worst: 0, exp: new(big.Rat)}}
	}
	st := state{mask: mask, budget: budget}
	if f, ok := s.fmemo[st]; ok {
		return f
	}
	var objs []objective
	for ti := range s.inst.Tests {
		t := &s.inst.Tests[ti]
		if t.Cost > budget {
			continue
		}
		groups, ok := s.partition(mask, ti)
		if !ok {
			continue
		}
		acc := []objective{{worst: 0, exp: new(big.Rat)}}
		for _, g := range groups {
			sub := s.frontier(g, budget-t.Cost)
			var next []objective
			for _, a := range acc {
				for _, b := range sub {
					w := a.worst
					if b.worst > w {
						w = b.worst
					}
					next = append(next, objective{worst: w, exp: new(big.Rat).Add(a.exp, b.exp)})
				}
			}
			acc = next
		}
		fee := new(big.Rat).Mul(s.pSum[mask], big.NewRat(t.Cost, 1))
		for _, a := range acc {
			objs = append(objs, objective{
				worst: a.worst + t.Cost,
				exp:   new(big.Rat).Add(a.exp, fee),
			})
		}
	}
	if len(objs) == 0 {
		// No affordable test splits the set: the only legal tree stops here.
		objs = []objective{{worst: 0, exp: new(big.Rat)}}
	}
	objs = prune(objs)
	s.fmemo[st] = objs
	return objs
}

// prune keeps only Pareto-optimal objectives, sorted by ascending
// worst-case cost and strictly decreasing expected cost.
func prune(objs []objective) []objective {
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].worst != objs[j].worst {
			return objs[i].worst < objs[j].worst
		}
		return objs[i].exp.Cmp(objs[j].exp) < 0
	})
	var out []objective
	for _, o := range objs {
		if len(out) == 0 || o.exp.Cmp(out[len(out)-1].exp) < 0 {
			out = append(out, o)
		}
	}
	return out
}

func lexMin(objs []objective) objective {
	best := objs[0]
	for _, o := range objs[1:] {
		if o.worst < best.worst ||
			(o.worst == best.worst && o.exp.Cmp(best.exp) < 0) {
			best = o
		}
	}
	return best
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalEvidence(a, b []Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
