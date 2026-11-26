package hvsched

import (
	"math/big"
)

type boxClass int

const (
	boxUnknown boxClass = iota
	boxAll
	boxNone
)

type abstractState struct {
	vtime []branchSet
	last  []branchSet
}

func (c *compiled) newAbstractState() *abstractState {
	st := &abstractState{
		vtime: make([]branchSet, len(c.p.Nodes)),
		last:  make([]branchSet, len(c.p.Nodes)),
	}
	for i := range c.p.Nodes {
		z := branchSet{zeroBranch(c.nVar)}
		st.vtime[i] = z
		st.last[i] = branchSet{zeroBranch(c.nVar)}
	}
	return st
}

// classify reports whether every point in dom reproduces the prefix, no point
// can reproduce it, or the box must be split further.
func (c *compiled) classify(dom *box, prefix int) boxClass {
	st := c.newAbstractState()
	allWin := true

	for t := 0; t < prefix; t++ {
		step := c.steps[t]
		if step.placeholder || step.obsLeaf < 0 {
			return boxNone
		}
		if step.noPacket {
			return boxNone
		}
		active := c.activeNodes(step.heads)
		obsLeaf := step.obsLeaf

		for _, edge := range c.pathEdges[obsLeaf] {
			p := edge.scheduler
			a := edge.child

			start := pruneBranches(unionBranches(st.vtime[p], st.last[a]), dom)
			fa := finishSetFor(start, step.obsLength, c.weight[a], dom)

			for _, b := range c.children[p] {
				if b == a || !active[b] {
					continue
				}
				alternatives := c.alternativeFinishes(b, dom, st, active, step.heads)
				siblingAlwaysWins := len(alternatives) > 0
				for _, fb := range alternatives {
					if !pathAlwaysWins(fa, fb, c.tie[a], c.tie[b], dom) {
						allWin = false
					}
					if !pathAlwaysLoses(fa, fb, c.tie[a], c.tie[b], dom) {
						siblingAlwaysWins = false
					}
				}
				if siblingAlwaysWins {
					return boxNone
				}
			}

			st.last[a] = fa
			if c.p.Clock == ClockFinishTag {
				st.vtime[p] = fa
			} else {
				st.vtime[p] = start
			}
		}

		obs := c.p.Observations[t]
		if finish := obs.completionConstraint(); finish != nil {
			node := obs.FinishNode
			if node == "" {
				node = obs.Leaf
			}
			idx := c.id[node]
			tag := st.last[idx]
			expected := branchSet{&branch{c: new(big.Rat).Set(finish), coef: zeroCoefficients(c.nVar)}}
			equalAlways := relationAlwaysTrue(tag, expected, false, dom) &&
				relationAlwaysTrue(expected, tag, false, dom)
			equalNever := relationAlwaysFalse(tag, expected, false, dom) ||
				relationAlwaysFalse(expected, tag, false, dom)
			if equalNever {
				return boxNone
			}
			if !equalAlways {
				allWin = false
			}
		}
	}

	if allWin {
		return boxAll
	}
	return boxUnknown
}

func zeroCoefficients(n int) []*big.Rat {
	out := make([]*big.Rat, n)
	for i := range out {
		out[i] = new(big.Rat)
	}
	return out
}

func (c *compiled) alternativeFinishes(
	child int,
	dom *box,
	st *abstractState,
	active []bool,
	heads map[int]uint64,
) []branchSet {
	parent := c.parent[child]
	lengths := c.possibleLengths(child, dom, st, active, heads, true)
	out := make([]branchSet, 0, len(lengths))
	seen := make(map[uint64]struct{})
	for _, length := range lengths {
		if _, ok := seen[length]; ok {
			continue
		}
		seen[length] = struct{}{}
		start := pruneBranches(unionBranches(st.vtime[parent], st.last[child]), dom)
		out = append(out, finishSetFor(start, length, c.weight[child], dom))
	}
	return out
}

// possibleLengths returns a guaranteed singleton when allowDefinite and the
// box has one winner; otherwise it returns all active descendant head lengths
// as a safe over-approximation.
func (c *compiled) possibleLengths(
	node int,
	dom *box,
	st *abstractState,
	active []bool,
	heads map[int]uint64,
	allowDefinite bool,
) []uint64 {
	if len(c.children[node]) == 0 {
		if length, ok := heads[node]; ok {
			return []uint64{length}
		}
		return nil
	}

	if allowDefinite {
		if length, ok := c.definiteLength(node, dom, st, active, heads); ok {
			return []uint64{length}
		}
	}

	seen := make(map[uint64]struct{})
	var out []uint64
	for _, leaf := range c.descLeaf[node] {
		if length, ok := heads[leaf]; ok {
			if _, exists := seen[length]; !exists {
				seen[length] = struct{}{}
				out = append(out, length)
			}
		}
	}
	return out
}

type abstractCandidate struct {
	child        int
	fixed        bool
	length       uint64
	finish       branchSet
	alternatives []branchSet
}

func (c *compiled) definiteLength(
	scheduler int,
	dom *box,
	st *abstractState,
	active []bool,
	heads map[int]uint64,
) (uint64, bool) {
	var candidates []abstractCandidate
	for _, child := range c.children[scheduler] {
		if !active[child] {
			continue
		}
		cand := abstractCandidate{child: child}
		if len(c.children[child]) == 0 {
			length, ok := heads[child]
			if !ok {
				continue
			}
			cand.fixed = true
			cand.length = length
			start := pruneBranches(unionBranches(st.vtime[scheduler], st.last[child]), dom)
			cand.finish = finishSetFor(start, length, c.weight[child], dom)
		} else if length, ok := c.definiteLength(child, dom, st, active, heads); ok {
			cand.fixed = true
			cand.length = length
			start := pruneBranches(unionBranches(st.vtime[scheduler], st.last[child]), dom)
			cand.finish = finishSetFor(start, length, c.weight[child], dom)
		} else {
			lengths := c.possibleLengths(child, dom, st, active, heads, false)
			parent := scheduler
			for _, length := range lengths {
				start := pruneBranches(unionBranches(st.vtime[parent], st.last[child]), dom)
				cand.alternatives = append(cand.alternatives, finishSetFor(start, length, c.weight[child], dom))
			}
		}
		candidates = append(candidates, cand)
	}

	winner := -1
	for i := range candidates {
		ci := &candidates[i]
		if !ci.fixed {
			continue
		}
		winsAll := true
		for j := range candidates {
			if i == j {
				continue
			}
			cj := &candidates[j]
			alternatives := cj.alternatives
			if cj.fixed {
				alternatives = []branchSet{cj.finish}
			}
			for _, fj := range alternatives {
				if !pathAlwaysWins(ci.finish, fj, c.tie[ci.child], c.tie[cj.child], dom) {
					winsAll = false
					break
				}
			}
			if !winsAll {
				break
			}
		}
		if winsAll {
			if winner != -1 {
				return 0, false
			}
			winner = i
		}
	}
	if winner < 0 {
		return 0, false
	}
	return candidates[winner].length, true
}
