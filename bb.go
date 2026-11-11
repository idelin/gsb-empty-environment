package netsec

import "math/bits"

// bbState holds the mutable branch-and-bound search state for one fixed
// enabled-domain subset.
type bbState struct {
	s *solver

	mask int

	// remaining[d][p] tracks capacity not yet consumed.
	remaining [][]int
	// assignment[i] is the chosen domain or -1 while unassigned.
	assignment []int
	// occupancy[d] is the bit set of requests placed on domain d.
	occupancy []uint32
	// unassigned tracks requests still looking for a domain.
	unassigned uint32

	bestCost   int
	bestFound  bool
	bestAssign []int
}

func newBBState(s *solver, mask int) *bbState {
	remaining := make([][]int, s.nd)
	for d := range remaining {
		remaining[d] = append([]int(nil), s.capacities[d]...)
	}
	assignment := make([]int, s.nr)
	for i := range assignment {
		assignment[i] = -1
	}
	return &bbState{
		s:          s,
		mask:       mask,
		remaining:  remaining,
		assignment: assignment,
		occupancy:  make([]uint32, s.nd),
		unassigned: (1 << uint(s.nr)) - 1,
		bestCost:   maxInt,
	}
}

// legalDomains returns the domains on which req can currently be placed.
func (b *bbState) legalDomains(req int) []int {
	s := b.s
	pIdx := s.purposeIndex[s.in.Requests[req].KeyPurpose]
	need := s.in.Requests[req].Capacity
	out := make([]int, 0, s.nd)
	for _, d := range s.cheapOrder[req] {
		if b.mask&(1<<d) == 0 || b.remaining[d][pIdx] < need {
			continue
		}
		if s.conflict[req]&b.occupancy[d] != 0 {
			continue
		}
		out = append(out, d)
	}
	return out
}

// pickRequest applies the minimum-remaining-values heuristic.
func (b *bbState) pickRequest() (int, []int) {
	bestReq, bestDomains := -1, []int(nil)
	pending := b.unassigned
	for pending != 0 {
		req := bits.TrailingZeros32(pending)
		low := uint32(1) << uint(req)
		domains := b.legalDomains(req)
		if len(domains) == 0 {
			return req, nil
		}
		if bestReq == -1 || len(domains) < len(bestDomains) {
			bestReq, bestDomains = req, domains
		}
		pending ^= low
	}
	return bestReq, bestDomains
}

// lowerBound sums each unassigned request's cheapest feasible domain under
// the current subset, independent of already-consumed capacity.
func (b *bbState) lowerBound() int {
	total := 0
	pending := b.unassigned
	for pending != 0 {
		req := bits.TrailingZeros32(pending)
		low := uint32(1) << uint(req)
		total += b.s.minCost[b.mask][req]
		pending ^= low
	}
	return total
}

// solve runs branch-and-bound and returns the optimum cost, assignment and
// feasibility.
func (b *bbState) solve() (int, []int, bool) {
	if _, feasible := b.s.lowerBound(b.mask); !feasible {
		return 0, nil, false
	}
	b.dfs(0, b.lowerBound())
	if !b.bestFound {
		return 0, nil, false
	}
	return b.bestCost, append([]int(nil), b.bestAssign...), true
}

func (b *bbState) dfs(spent, bound int) {
	if bound >= b.bestCost {
		return
	}
	if b.unassigned == 0 {
		b.bestCost = spent
		b.bestFound = true
		b.bestAssign = append(b.bestAssign[:0], b.assignment...)
		return
	}

	req, domains := b.pickRequest()
	if len(domains) == 0 {
		return
	}
	s := b.s
	pIdx := s.purposeIndex[s.in.Requests[req].KeyPurpose]
	need := s.in.Requests[req].Capacity
	reqBit := uint32(1) << uint(req)

	// Remove req from the unassigned set once for all child branches; restore
	// it on the way out.
	b.unassigned &^= reqBit
	for _, d := range domains {
		stepCost := s.in.Cost[req][d]
		b.assignment[req] = d
		b.remaining[d][pIdx] -= need
		b.occupancy[d] |= reqBit

		if b.forwardFeasible() {
			nextBound := bound - s.minCost[b.mask][req] + stepCost
			b.dfs(spent+stepCost, nextBound)
		}

		b.occupancy[d] &^= reqBit
		b.remaining[d][pIdx] += need
		b.assignment[req] = -1
	}
	b.unassigned |= reqBit
}

// forwardFeasible verifies that every still-unassigned request retains at
// least one legal domain, and that per-purpose aggregate demand fits the
// jointly remaining capacity.
func (b *bbState) forwardFeasible() bool {
	demand := make([]int, len(b.s.purposes))
	pending := b.unassigned
	for pending != 0 {
		req := bits.TrailingZeros32(pending)
		low := uint32(1) << uint(req)
		if len(b.legalDomains(req)) == 0 {
			return false
		}
		pIdx := b.s.purposeIndex[b.s.in.Requests[req].KeyPurpose]
		demand[pIdx] += b.s.in.Requests[req].Capacity
		pending ^= low
	}

	for pIdx, required := range demand {
		available := 0
		for d := 0; d < b.s.nd; d++ {
			if b.mask&(1<<d) != 0 {
				available += b.remaining[d][pIdx]
			}
		}
		if required > available {
			return false
		}
	}
	return true
}
