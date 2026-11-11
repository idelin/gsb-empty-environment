package netsec

import "sort"

// maxPatches is the hard bound on candidate patches stated by the problem.
const maxPatches = 20

// Solve finds a minimum-cost subset of patches that makes the target
// unreachable and returns its blocking proof with minimality witnesses.
//
// The search enumerates every patch subset exhaustively (up to 2^20) and
// evaluates each one with the least-fixpoint reachability engine, so shared
// preconditions are treated as a single closure fact rather than as
// independent paths. Ties on total cost are broken deterministically: among
// inclusion-minimal blocking subsets, the one whose sorted patch-ID list is
// lexicographically smallest wins.
func Solve(p *Problem) (*Solution, error) {
	c, err := compile(p)
	if err != nil {
		return nil, err
	}

	// Index-ordered patches give every bitmask a stable, deterministic meaning
	// and make ID-list tie breaks simple.
	sort.SliceStable(c.patches, func(i, j int) bool {
		return c.patches[i].id < c.patches[j].id
	})

	n := len(c.patches)
	disabled := newBitset(len(c.gate))

	// Fast path: with no patch applied the target is already unreachable, so
	// the empty set is the unique minimum-cost, inclusion-minimal answer.
	closed, _ := c.simulate(disabled, true)
	if !closed.has(c.target) {
		return c.buildSolution(0, 0, disabled)
	}

	// Phase 1: exhaustive DFS over all subsets. Cost pruning removes branches
	// already above the cheapest blocking cost found so far; every blocking
	// mask at the best cost is retained for the deterministic tie break.
	bestCost := 0
	foundBest := false
	var blockers []uint32

	var dfs func(pos int, mask uint32, cost int)
	dfs = func(pos int, mask uint32, cost int) {
		// Only strictly greater costs can be discarded. An equal-cost branch
		// may contain an optimal blocker needed for the ID tie break, and
		// zero-cost patches make bestCost 0 a valid value rather than "unset",
		// so the unset state is tracked separately with foundBest.
		if foundBest && cost > bestCost {
			return
		}
		if pos == n {
			// Rebuild the shared bitset from the mask: DFS backtracking does
			// not undo leaf mutations, so a stale inclusion would otherwise
			// leak into the exclude branch.
			c.applyMask(mask, disabled)
			if mask == 0 {
				return
			}
			reach, _ := c.simulate(disabled, true)
			if !reach.has(c.target) {
				if !foundBest || cost < bestCost {
					bestCost = cost
					foundBest = true
					blockers = blockers[:0]
				}
				if foundBest && cost == bestCost {
					blockers = append(blockers, mask)
				}
			}
			return
		}
		dfs(pos+1, mask, cost)
		dfs(pos+1, mask|(uint32(1)<<uint(pos)), cost+c.patches[pos].cost)
	}
	dfs(0, 0, 0)

	if !foundBest {
		// The empty set was handled above, so no non-empty subset blocks.
		return nil, ErrUnblockable
	}

	// Phase 2: turn masks into sorted ID lists and pick the lexicographically
	// smallest inclusion-minimal blocker at the minimum cost.
	type candidate struct {
		mask uint32
		ids  []string
	}
	candidates := make([]candidate, 0, len(blockers))
	for _, mask := range blockers {
		candidates = append(candidates, candidate{mask: mask, ids: c.maskIDs(mask)})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return lexLess(candidates[i].ids, candidates[j].ids)
	})

	for _, cand := range candidates {
		if c.isInclusionMinimal(cand.mask) {
			return c.buildSolution(cand.mask, bestCost, disabled)
		}
	}

	// Unreachable in practice: a minimum-cost blocker always contains an
	// inclusion-minimal blocker of no greater cost.
	return nil, ErrUnblockable
}

// applyMask marks every action disabled by the patches in mask.
func (c *compiled) applyMask(mask uint32, disabled bitset) {
	for i := range disabled {
		disabled[i] = 0
	}
	for i, pc := range c.patches {
		if mask&(uint32(1)<<uint(i)) != 0 {
			disabled.set(pc.action)
		}
	}
}

// maskIDs returns the sorted patch IDs contained in mask. Patches are kept in
// ascending ID order, so the result is already sorted.
func (c *compiled) maskIDs(mask uint32) []string {
	var ids []string
	for i, pc := range c.patches {
		if mask&(uint32(1)<<uint(i)) != 0 {
			ids = append(ids, pc.id)
		}
	}
	return ids
}

// isInclusionMinimal verifies that removing any single patch from mask makes
// the target reachable again.
func (c *compiled) isInclusionMinimal(mask uint32) bool {
	disabled := newBitset(len(c.gate))
	for i := range c.patches {
		bit := uint32(1) << uint(i)
		if mask&bit == 0 {
			continue
		}
		// Rebuild the closure with exactly this patch omitted. Clearing a
		// single action bit would be wrong when another selected patch
		// disables the same action, since that patch still keeps it disabled.
		c.applyMask(mask&^bit, disabled)
		reach, _ := c.simulate(disabled, true)
		if !reach.has(c.target) {
			return false
		}
	}
	return true
}

// buildSolution assembles the solution and the full blocking/minimality proof
// for a chosen mask.
func (c *compiled) buildSolution(mask uint32, cost int, scratch bitset) (*Solution, error) {
	c.applyMask(mask, scratch)

	closed, order := c.simulate(scratch, false)
	if closed.has(c.target) {
		return nil, ErrUnblockable
	}

	reason := Reason{
		DisabledActions:  c.disabledActions(scratch),
		ReachableClosure: c.orderIDs(order),
	}

	witnesses := make([]Witness, 0, len(c.patches))
	for i, pc := range c.patches {
		bit := uint32(1) << uint(i)
		if mask&bit == 0 {
			continue
		}
		// Rebuild the disabled set with exactly this patch omitted so the
		// witness stays correct even when another patch shares the action.
		c.applyMask(mask&^bit, scratch)
		omittedClosed, omittedOrder := c.simulate(scratch, false)
		activation := c.targetOrder(omittedClosed, omittedOrder)
		if activation == nil {
			// Another patch disables the same action, so dropping this one
			// alone cannot re-open it; such a mask is not minimal.
			c.applyMask(mask, scratch)
			return nil, ErrUnblockable
		}
		witnesses = append(witnesses, Witness{
			OmittedPatch:    pc.id,
			ActivationOrder: activation,
		})
	}
	c.applyMask(mask, scratch)

	return &Solution{
		Selected: c.maskIDs(mask),
		Cost:     cost,
		Proof: Proof{
			Target:     c.nodeID[c.target],
			Blocking:   reason,
			Minimality: witnesses,
		},
	}, nil
}

// disabledActions lists disabled node IDs in node-index order.
func (c *compiled) disabledActions(disabled bitset) []string {
	var ids []string
	for i := range c.gate {
		if disabled.has(i) {
			ids = append(ids, c.nodeID[i])
		}
	}
	return ids
}

// orderIDs translates an index activation order into node IDs.
func (c *compiled) orderIDs(order []int) []string {
	ids := make([]string, len(order))
	for i, idx := range order {
		ids[i] = c.nodeID[idx]
	}
	return ids
}

// clear removes one index from the bitset.
func (b bitset) clear(i int) {
	b[i/wordBits] &^= 1 << uint(i%wordBits)
}

// lexLess compares two sorted string lists lexicographically. A strict prefix
// is considered smaller.
func lexLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
