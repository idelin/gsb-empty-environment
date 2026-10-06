package treeplan

import (
	"errors"
	"fmt"
)

// MaxEnumerateGroups bounds the subset enumeration of problem 1.
const MaxEnumerateGroups = 20

const inf = int64(1) << 62

// ExpandSetResult is the answer to problem 1.
type ExpandSetResult struct {
	Expanded    []string `json:"expanded"`    // groups to expand, in tree order
	ExtraLeaves []string `json:"extraLeaves"` // non-target leaves exposed as a side effect
	Cost        int64    `json:"cost"`        // expansion costs + exposure penalties
}

// maskClosed reports whether the expanded set is ancestor-closed: a group
// may be expanded only when its parent is expanded.
func (t *Tree) maskClosed(mask uint32) bool {
	for g := range t.Groups {
		if mask&(uint32(1)<<uint(g)) == 0 {
			continue
		}
		if p := t.Parent[g]; p >= 0 && mask&(uint32(1)<<uint(p)) == 0 {
			return false
		}
	}
	return true
}

// maskServesAll reports whether every target leaf is visible under mask.
func (t *Tree) maskServesAll(mask uint32) bool {
	for _, am := range t.TargetAnc {
		if mask&am != am {
			return false
		}
	}
	return true
}

// maskCost computes expansion costs plus exposure penalties for mask.
// Each expanded group is charged once no matter how many targets sit
// below it, which is what makes ancestor expansion costs shared.
func (t *Tree) maskCost(mask uint32) int64 {
	var cost int64
	for g, gr := range t.Groups {
		if mask&(uint32(1)<<uint(g)) != 0 {
			cost += gr.ExpandCost
		}
	}
	for li, lf := range t.Leaves {
		if t.IsTarget[li] {
			continue
		}
		if am := t.LeafAncMask[li]; mask&am == am {
			cost += lf.Penalty
		}
	}
	return cost
}

func (t *Tree) resultFromMask(mask uint32) *ExpandSetResult {
	res := &ExpandSetResult{Cost: t.maskCost(mask)}
	for g, gr := range t.Groups {
		if mask&(uint32(1)<<uint(g)) != 0 {
			res.Expanded = append(res.Expanded, gr.Name)
		}
	}
	for li, lf := range t.Leaves {
		if t.IsTarget[li] {
			continue
		}
		if am := t.LeafAncMask[li]; mask&am == am {
			res.ExtraLeaves = append(res.ExtraLeaves, lf.Name)
		}
	}
	return res
}

// SolveExpandSet enumerates all 2^n group subsets (n <= MaxEnumerateGroups),
// keeps the ancestor-closed ones that make every target visible, and returns
// the cheapest.
func (t *Tree) SolveExpandSet() (*ExpandSetResult, error) {
	n := len(t.Groups)
	if n > MaxEnumerateGroups {
		return nil, fmt.Errorf("subset enumeration supports at most %d groups, got %d", MaxEnumerateGroups, n)
	}
	var bestMask uint32
	bestCost := inf
	found := false
	lim := uint64(1) << uint(n)
	for m := uint64(0); m < lim; m++ {
		mask := uint32(m)
		if !t.maskClosed(mask) || !t.maskServesAll(mask) {
			continue
		}
		if cost := t.maskCost(mask); !found || cost < bestCost {
			found, bestCost, bestMask = true, cost, mask
		}
	}
	if !found {
		return nil, errors.New("no feasible expansion set")
	}
	return t.resultFromMask(bestMask), nil
}

// SolveExpandSetDP solves problem 1 exactly with a bottom-up tree DP. It is
// used as an independent cross-check for the subset enumeration.
func (t *Tree) SolveExpandSetDP() *ExpandSetResult {
	n := len(t.Groups)
	best := make([]int64, n) // min cost contributed by the subtree, given the parent is expanded
	take := make([]bool, n)  // expand this group when the parent is expanded
	subTarget := make([]bool, n)
	for g := n - 1; g >= 0; g-- {
		has := false
		for _, li := range t.GroupLeaves[g] {
			if t.IsTarget[li] {
				has = true
				break
			}
		}
		if !has {
			for _, c := range t.Children[g] {
				if subTarget[c] {
					has = true
					break
				}
			}
		}
		subTarget[g] = has
		expand := t.Groups[g].ExpandCost
		for _, li := range t.GroupLeaves[g] {
			if !t.IsTarget[li] {
				expand += t.Leaves[li].Penalty
			}
		}
		for _, c := range t.Children[g] {
			expand += best[c]
		}
		collapse := int64(0)
		if has {
			collapse = inf // a collapsed group hides every target below it
		}
		if expand <= collapse {
			best[g], take[g] = expand, true
		} else {
			best[g] = collapse
		}
	}
	var mask uint32
	var mark func(g int, active bool)
	mark = func(g int, active bool) {
		if !active || !take[g] {
			return
		}
		mask |= uint32(1) << uint(g)
		for _, c := range t.Children[g] {
			mark(c, true)
		}
	}
	mark(0, true)
	return t.resultFromMask(mask)
}

// VerifyExpandSet independently re-checks a result: ancestor closure, target
// visibility, and the cost, recomputed by walking the tree rather than by
// bitmask arithmetic.
func (t *Tree) VerifyExpandSet(res *ExpandSetResult) error {
	var mask uint32
	seen := map[string]bool{}
	for _, name := range res.Expanded {
		g, ok := t.GroupIndex[name]
		if !ok {
			return fmt.Errorf("unknown group %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate group %q", name)
		}
		seen[name] = true
		mask |= uint32(1) << uint(g)
	}
	if !t.maskClosed(mask) {
		return errors.New("expanded set violates ancestor closure")
	}
	var cost int64
	targetsVisible := 0
	extraSet := map[string]bool{}
	var walk func(g int)
	walk = func(g int) {
		if mask&(uint32(1)<<uint(g)) == 0 {
			return
		}
		cost += t.Groups[g].ExpandCost
		for _, li := range t.GroupLeaves[g] {
			if t.IsTarget[li] {
				targetsVisible++
			} else {
				cost += t.Leaves[li].Penalty
				extraSet[t.Leaves[li].Name] = true
			}
		}
		for _, c := range t.Children[g] {
			walk(c)
		}
	}
	walk(0)
	if targetsVisible != len(t.Targets) {
		return fmt.Errorf("only %d of %d targets visible", targetsVisible, len(t.Targets))
	}
	if cost != res.Cost {
		return fmt.Errorf("reported cost %d, recomputed %d", res.Cost, cost)
	}
	if len(extraSet) != len(res.ExtraLeaves) {
		return fmt.Errorf("reported %d extra leaves, recomputed %d", len(res.ExtraLeaves), len(extraSet))
	}
	for _, name := range res.ExtraLeaves {
		if !extraSet[name] {
			return fmt.Errorf("reported extra leaf %q is not exposed", name)
		}
	}
	return nil
}
