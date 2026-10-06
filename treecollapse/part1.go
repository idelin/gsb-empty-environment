package main

import "fmt"

// Part1Result is the outcome of the subset enumeration over at most 20
// groups: the cheapest ancestor-closed expanded set that exposes every
// target, with the cost split into shared expansion cost and the penalty
// for extra exposed leaves.
type Part1Result struct {
	Mask          uint64
	Cost          int
	ExpandCost    int
	LeafPenalty   int
	FeasibleSets  int
	ExposedLeaves []string
}

// evalMask prices one expanded set: shared expansion cost (each expanded
// group paid once, no matter how many targets sit beneath it) plus the
// penalty of every exposed leaf that is not a target.
func (inst *Instance) evalMask(mask uint64) (expand, penalty int, exposed []string) {
	for g := range inst.Groups {
		if mask&(1<<uint(g)) == 0 {
			continue
		}
		expand += inst.Groups[g].ExpandCost
		for l, leaf := range inst.Groups[g].Leaves {
			if inst.isTargetLeaf(g, l) {
				continue
			}
			penalty += leaf.Penalty
			exposed = append(exposed, fmt.Sprintf("%s/%s", inst.Groups[g].Name, leaf.Name))
		}
	}
	return expand, penalty, exposed
}

// SolvePart1 enumerates all 2^n expanded sets (n <= 20), keeps the
// ancestor-closed ones that make every target visible, and returns the
// cheapest.
func (inst *Instance) SolvePart1() Part1Result {
	n := len(inst.Groups)
	if n > 20 {
		panic("SolvePart1 supports at most 20 groups")
	}
	best := Part1Result{Cost: -1}
	feasible := 0
	var mask uint64
	for mask = 0; mask < 1<<uint(n); mask++ {
		if !inst.closedMask(mask) || !inst.allVisible(mask) {
			continue
		}
		feasible++
		expand, penalty, exposed := inst.evalMask(mask)
		cost := expand + penalty
		if best.Cost == -1 || cost < best.Cost {
			best = Part1Result{
				Mask:          mask,
				Cost:          cost,
				ExpandCost:    expand,
				LeafPenalty:   penalty,
				ExposedLeaves: exposed,
			}
		}
	}
	best.FeasibleSets = feasible
	return best
}

// RequiredMask builds the structural optimum directly: the union of the
// ancestor chains of all targets (plus the parent group of each leaf
// target). Expansion costs and penalties are non-negative, so any
// superset of this set can only cost more; used to cross-check the
// brute-force result.
func (inst *Instance) RequiredMask() uint64 {
	var mask uint64
	for _, t := range inst.Targets {
		if t.Leaf >= 0 {
			mask |= 1 << uint(t.Group)
		}
		for _, a := range inst.ancestorsOf(t.Group) {
			mask |= 1 << uint(a)
		}
	}
	return mask
}

// MaskNames renders an expanded set as group names for display.
func (inst *Instance) MaskNames(mask uint64) []string {
	var names []string
	for g := range inst.Groups {
		if mask&(1<<uint(g)) != 0 {
			names = append(names, inst.Groups[g].Name)
		}
	}
	return names
}
