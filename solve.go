package dectree

import (
	"math/big"
	"math/bits"
)

// stateKey identifies a subproblem: the candidates still possible and the
// budget still available on the current path.
type stateKey struct {
	set    uint16
	budget int
}

// option is one Pareto-optimal strategy for a subproblem. exp is the
// probability-weighted (unnormalized) expected remaining cost over the
// subproblem candidate set. children is used only while a parent node is
// being assembled.
type option struct {
	worst    int
	exp      *big.Rat
	node     *Node
	children []*Node
}

// less orders options lexicographically: worst cost first, then expected.
func less(a, b option) bool {
	if a.worst != b.worst {
		return a.worst < b.worst
	}
	return a.exp.Cmp(b.exp) < 0
}

// dominates reports whether a is at least as good as b on both criteria.
func dominates(a, b option) bool {
	return a.worst <= b.worst && a.exp.Cmp(b.exp) <= 0
}

// prune drops dominated options, keeping the first among equals so results
// are deterministic (earlier tests win ties).
func prune(opts []option) []option {
	kept := make([]option, 0, len(opts))
	for _, o := range opts {
		dominated := false
		for _, k := range kept {
			if dominates(k, o) {
				dominated = true
				break
			}
		}
		if dominated {
			continue
		}
		rest := kept[:0]
		for _, k := range kept {
			if !dominates(o, k) {
				rest = append(rest, k)
			}
		}
		kept = append(rest, o)
	}
	return kept
}

// combine builds the Pareto frontier of a test node from the frontiers of
// its branches (cartesian product, then prune).
func combine(groups [][]option) []option {
	acc := []option{{worst: 0, exp: new(big.Rat)}}
	for _, g := range groups {
		var next []option
		for _, a := range acc {
			for _, o := range g {
				w := a.worst
				if o.worst > w {
					w = o.worst
				}
				children := make([]*Node, 0, len(a.children)+1)
				children = append(children, a.children...)
				children = append(children, o.node)
				next = append(next, option{
					worst:    w,
					exp:      new(big.Rat).Add(a.exp, o.exp),
					children: children,
				})
			}
		}
		acc = prune(next)
	}
	return acc
}

type solver struct {
	p    *Problem
	memo map[stateKey][]option
}

// Solve returns the optimal strategy for p: among all decision trees whose
// every path stays within Budget, it minimizes the worst-case cost first and
// the expected cost second.
func Solve(p *Problem) (*Solution, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	s := &solver{p: p, memo: map[stateKey][]option{}}
	full := uint16(1<<uint(len(p.Candidates))) - 1
	frontier := s.solve(full, p.Budget)
	best := frontier[0]
	for _, o := range frontier[1:] {
		if less(o, best) {
			best = o
		}
	}
	sol := &Solution{
		Root:      best.node,
		WorstCost: best.worst,
		Expected:  new(big.Rat).Quo(best.exp, sumProb(p, full)),
		prob:      p,
	}
	sol.Ambiguous = collectAmbiguous(p, best.node)
	return sol, nil
}

// solve returns the Pareto frontier of strategies for a subproblem.
func (s *solver) solve(set uint16, budget int) []option {
	key := stateKey{set: set, budget: budget}
	if opts, ok := s.memo[key]; ok {
		return opts
	}
	var opts []option
	if bits.OnesCount16(set) <= 1 {
		opts = leafOption(set)
	} else {
		opts = s.testOptions(set, budget)
		if len(opts) == 0 {
			// No affordable test splits this set: the candidates are
			// indistinguishable within the remaining budget.
			opts = leafOption(set)
		}
	}
	s.memo[key] = opts
	return opts
}

func leafOption(set uint16) []option {
	return []option{{
		worst: 0,
		exp:   new(big.Rat),
		node:  &Node{Test: -1, Leaf: setMembers(set)},
	}}
}

// testOptions tries every affordable, informative test and merges the
// resulting Pareto frontiers.
func (s *solver) testOptions(set uint16, budget int) []option {
	var frontier []option
	for ti := range s.p.Tests {
		t := &s.p.Tests[ti]
		if t.Cost > budget {
			continue
		}
		outcomes, groups, ok := partition(s.p, set, ti)
		if !ok {
			continue
		}
		perGroup := make([][]option, len(groups))
		for i, g := range groups {
			perGroup[i] = s.solve(g, budget-t.Cost)
		}
		combined := combine(perGroup)
		weight := sumProb(s.p, set)
		fee := big.NewRat(int64(t.Cost), 1)
		for i := range combined {
			combined[i].worst += t.Cost
			combined[i].exp.Add(combined[i].exp, new(big.Rat).Mul(weight, fee))
			branches := make(map[string]*Node, len(groups))
			for j, o := range outcomes {
				branches[o] = combined[i].children[j]
			}
			combined[i].node = &Node{Test: ti, Branches: branches}
			combined[i].children = nil
		}
		frontier = append(frontier, combined...)
		frontier = prune(frontier)
	}
	return frontier
}

// collectAmbiguous walks the final tree and reports every leaf that still
// holds more than one candidate, with the observations leading to it.
func collectAmbiguous(p *Problem, root *Node) []AmbiguousGroup {
	var groups []AmbiguousGroup
	var walk func(n *Node, path []Observation, cost int)
	walk = func(n *Node, path []Observation, cost int) {
		if n.IsLeaf() {
			if len(n.Leaf) > 1 {
				groups = append(groups, AmbiguousGroup{
					Candidates:  append([]int(nil), n.Leaf...),
					Evidence:    append([]Observation(nil), path...),
					BudgetBound: separableWithin(p, bitSet(n.Leaf), 1<<30),
				})
			}
			return
		}
		for _, o := range sortedOutcomes(n) {
			walk(n.Branches[o],
				append(path, Observation{Test: n.Test, Outcome: o}),
				cost+p.Tests[n.Test].Cost)
		}
	}
	walk(root, nil, 0)
	return groups
}

// separableWithin reports whether some test costing at most budget splits
// the candidate set.
func separableWithin(p *Problem, set uint16, budget int) bool {
	for ti := range p.Tests {
		if p.Tests[ti].Cost > budget {
			continue
		}
		if !allSameOutcome(p, set, ti) {
			return true
		}
	}
	return false
}
