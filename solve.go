package policytree

import (
	"fmt"
	"math/big"
	"math/bits"
	"sort"
)

// Cost is the objective value of a (sub)policy: the worst-case total loss
// over all worlds consistent with the observation history, with the expected
// total loss as a tie-breaker. Costs of a subtree exclude probe costs already
// paid on the path leading to it.
type Cost struct {
	Worst int64
	Exp   *big.Rat
}

func (c Cost) String() string {
	return fmt.Sprintf("worst=%d exp=%s", c.Worst, c.Exp.RatString())
}

// lessCost reports whether a is strictly preferred over b: lower worst-case
// loss first, then lower expected loss.
func lessCost(a, b Cost) bool {
	if a.Worst != b.Worst {
		return a.Worst < b.Worst
	}
	return a.Exp.Cmp(b.Exp) < 0
}

// NodeKind distinguishes probe nodes from terminal action nodes.
type NodeKind int

const (
	// ActionNode is a terminal node that takes an action.
	ActionNode NodeKind = iota
	// ProbeNode is an internal node that performs a probe and branches on
	// the observed outcome.
	ProbeNode
)

// Node is a policy tree node. A policy tree is a function of the observation
// history by construction: two worlds that produce the same probe outcomes
// traverse the same path and therefore receive the same action
// (non-anticipation).
type Node struct {
	Kind   NodeKind
	Action int // valid when Kind == ActionNode
	Probe  int // valid when Kind == ProbeNode
	// Outcomes lists the distinct probe outcomes possible at this node in
	// sorted order; Children maps each to the corresponding subtree.
	Outcomes []string
	Children map[string]*Node
}

type solveKey struct {
	info uint64
	left int
}

type solveEntry struct {
	cost Cost
	node *Node
}

// Solver computes optimal policy trees for a Problem.
type Solver struct {
	p    *Problem
	memo map[solveKey]solveEntry
}

// Solve returns the optimal cost and policy tree for p. The tree branches
// only on observable probe outcomes, so identical observation histories
// always lead to identical actions.
func Solve(p *Problem) (Cost, *Node, error) {
	if err := p.Validate(); err != nil {
		return Cost{}, nil, err
	}
	s := &Solver{p: p, memo: make(map[solveKey]solveEntry)}
	full := uint64(1)<<uint(p.NumWorlds()) - 1
	cost, node := s.solve(full, p.MaxProbes)
	return cost, node, nil
}

// solve returns the optimal cost and subtree for the given information set
// (bitmask of worlds consistent with the observation history) with left
// probes remaining. Past probe costs are sunk and shift every leaf below by
// the same constant, so the optimal subtree depends only on (info, left).
func (s *Solver) solve(info uint64, left int) (Cost, *Node) {
	key := solveKey{info: info, left: left}
	if e, ok := s.memo[key]; ok {
		return e.cost, e.node
	}

	var bestCost Cost
	var bestNode *Node

	// Terminal choice: take an action now. Actions are considered first so
	// that ties resolve toward simpler trees and lower indices.
	for a := range s.p.Actions {
		cost := s.actionCost(info, a)
		node := &Node{Kind: ActionNode, Action: a}
		if bestNode == nil || lessCost(cost, bestCost) {
			bestCost, bestNode = cost, node
		}
	}

	// Non-terminal choice: perform a probe and branch on its outcome.
	if left > 0 {
		for pr := range s.p.Probes {
			cost, node := s.probeCost(info, left, pr)
			if lessCost(cost, bestCost) {
				bestCost, bestNode = cost, node
			}
		}
	}

	s.memo[key] = solveEntry{cost: bestCost, node: bestNode}
	return bestCost, bestNode
}

// actionCost evaluates taking action a immediately in information set info.
func (s *Solver) actionCost(info uint64, a int) Cost {
	action := s.p.Actions[a]
	worst := int64(0)
	first := true
	exp := new(big.Rat)
	denom := new(big.Rat)
	eachWorld(info, func(w int) {
		loss := action.Losses[w]
		if first || loss > worst {
			worst = loss
		}
		first = false
		exp.Add(exp, new(big.Rat).Mul(s.p.Weights[w], new(big.Rat).SetInt64(loss)))
		denom.Add(denom, s.p.Weights[w])
	})
	exp.Quo(exp, denom)
	return Cost{Worst: worst, Exp: exp}
}

// probeCost evaluates probing with probe pr in information set info and then
// continuing optimally with left-1 probes remaining.
func (s *Solver) probeCost(info uint64, left, pr int) (Cost, *Node) {
	probe := s.p.Probes[pr]
	groups := partition(info, probe.Outcomes)

	outcomes := make([]string, 0, len(groups))
	for outcome := range groups {
		outcomes = append(outcomes, outcome)
	}
	sort.Strings(outcomes)

	node := &Node{Kind: ProbeNode, Probe: pr, Outcomes: outcomes, Children: make(map[string]*Node, len(outcomes))}
	worst := int64(0)
	first := true
	exp := new(big.Rat).SetInt64(probe.Cost)
	denom := weightOf(s.p, info)
	for _, outcome := range outcomes {
		sub := groups[outcome]
		childCost, childNode := s.solve(sub, left-1)
		node.Children[outcome] = childNode
		if first || childCost.Worst > worst {
			worst = childCost.Worst
		}
		first = false
		share := new(big.Rat).Quo(weightOf(s.p, sub), denom)
		exp.Add(exp, share.Mul(share, childCost.Exp))
	}
	worst += probe.Cost
	return Cost{Worst: worst, Exp: exp}, node
}

// partition splits the worlds in info by their probe outcome.
func partition(info uint64, outcomes []string) map[string]uint64 {
	groups := make(map[string]uint64)
	eachWorld(info, func(w int) {
		groups[outcomes[w]] |= 1 << uint(w)
	})
	return groups
}

// weightOf sums the world weights in an information set.
func weightOf(p *Problem, info uint64) *big.Rat {
	total := new(big.Rat)
	eachWorld(info, func(w int) {
		total.Add(total, p.Weights[w])
	})
	return total
}

// eachWorld calls f for every world index set in the bitmask.
func eachWorld(info uint64, f func(w int)) {
	for m := info; m != 0; m &= m - 1 {
		f(bits.TrailingZeros64(m))
	}
}
