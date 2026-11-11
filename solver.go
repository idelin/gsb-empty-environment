package netsec

import (
	"math"
	"sort"
)

// maxInt is the pruning sentinel used before a search finds its first leaf.
const maxInt = math.MaxInt

type solver struct {
	in Input
	nr int
	nd int

	// purposeIndex maps every request key purpose to a compact slice index.
	purposeIndex map[string]int
	purposes     []string
	// capacities[d][p] is domain d's capacity for purpose p, zero if absent.
	capacities [][]int
	// feasibleMask[i] lists domains that serve request i's key purpose.
	feasibleMask []int
	// conflict[i] is the set of request indices separated from i.
	conflict []uint32
	// pairSource records the provenance of every conflict pair.
	pairSource map[Pair]string

	// cheapOrder[i] lists feasible domains of i sorted by ascending cost.
	cheapOrder [][]int
	// minCost[mask][i] is request i's cheapest feasible domain inside mask,
	// or -1 when none exists.
	minCost [][]int
}

func newSolver(in Input) *solver {
	nr, nd := len(in.Requests), len(in.Domains)
	s := &solver{
		in:           in,
		nr:           nr,
		nd:           nd,
		purposeIndex: make(map[string]int),
		pairSource:   make(map[Pair]string),
		feasibleMask: make([]int, nr),
		conflict:     make([]uint32, nr),
		cheapOrder:   make([][]int, nr),
	}

	for i := range in.Requests {
		if _, ok := s.purposeIndex[in.Requests[i].KeyPurpose]; !ok {
			s.purposeIndex[in.Requests[i].KeyPurpose] = len(s.purposes)
			s.purposes = append(s.purposes, in.Requests[i].KeyPurpose)
		}
	}

	s.capacities = make([][]int, nd)
	for d := range in.Domains {
		row := make([]int, len(s.purposes))
		for p, purpose := range s.purposes {
			row[p] = in.Domains[d].Purposes[purpose]
		}
		s.capacities[d] = row
	}

	for i := range in.Requests {
		mask := 0
		pIdx := s.purposeIndex[in.Requests[i].KeyPurpose]
		for d := range in.Domains {
			if s.capacities[d][pIdx] > 0 {
				mask |= 1 << d
			}
		}
		s.feasibleMask[i] = mask
	}

	s.addApprovalConflicts()
	for _, p := range in.MustSeparate {
		a, b := s.indexOf(p.A), s.indexOf(p.B)
		s.conflict[a] |= 1 << uint(b)
		s.conflict[b] |= 1 << uint(a)
		s.pairSource[p.canonical()] = "explicit"
	}

	for i := range in.Requests {
		domains := make([]int, 0, nd)
		for d := 0; d < nd; d++ {
			if s.feasibleMask[i]&(1<<d) != 0 {
				domains = append(domains, d)
			}
		}
		sort.SliceStable(domains, func(a, b int) bool {
			return in.Cost[i][domains[a]] < in.Cost[i][domains[b]]
		})
		s.cheapOrder[i] = domains
	}

	s.minCost = make([][]int, 1<<nd)
	for mask := 0; mask < (1 << nd); mask++ {
		row := make([]int, nr)
		for i := range in.Requests {
			row[i] = -1
			for _, d := range s.cheapOrder[i] {
				if mask&(1<<d) != 0 {
					row[i] = in.Cost[i][d]
					break
				}
			}
		}
		s.minCost[mask] = row
	}
	return s
}

// addApprovalConflicts forces every pair of sensitive approval requests that
// share a tenant onto different domains, even when their approval roles
// differ. This is the conservative reading of the isolation requirement.
func (s *solver) addApprovalConflicts() {
	byTenant := make(map[string][]int)
	for i := range s.in.Requests {
		r := &s.in.Requests[i]
		if r.Sensitive && r.ApprovalRole != "" {
			byTenant[r.TenantID] = append(byTenant[r.TenantID], i)
		}
	}
	for _, indices := range byTenant {
		for x := 0; x < len(indices); x++ {
			for y := x + 1; y < len(indices); y++ {
				a, b := indices[x], indices[y]
				s.conflict[a] |= 1 << uint(b)
				s.conflict[b] |= 1 << uint(a)
				s.pairSource[s.pairOf(a, b)] = "approval_role"
			}
		}
	}
}

func (s *solver) indexOf(id string) int {
	for i := range s.in.Requests {
		if s.in.Requests[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *solver) pairOf(a, b int) Pair {
	return Pair{
		A: s.in.Requests[a].ID,
		B: s.in.Requests[b].ID,
	}.canonical()
}

// search returns the minimum latency cost over assignments restricted to the
// domains in mask, the corresponding assignment, and whether any feasible
// assignment exists.
func (s *solver) search(mask int) (int, []int, bool) {
	if _, feasible := s.lowerBound(mask); !feasible {
		return 0, nil, false
	}
	return newBBState(s, mask).solve()
}

// lowerBound sums every request's cheapest feasible domain inside mask and
// rejects subsets that cannot serve some key purpose.
func (s *solver) lowerBound(mask int) (int, bool) {
	total := 0
	for i := range s.in.Requests {
		c := s.minCost[mask][i]
		if c < 0 {
			return 0, false
		}
		total += c
	}
	return total, true
}

func (s *solver) buildPlan(mask, totalCost int, assignment []int) Plan {
	enabled := sortedDomainIndices(mask, s.nd)
	return Plan{
		Assignment:     append([]int(nil), assignment...),
		EnabledDomains: enabled,
		EnabledCount:   len(enabled),
		TotalCost:      totalCost,
	}
}

// forEachSubset invokes fn for every k-bit subset of mask in ascending
// numeric order. Returning true from fn stops enumeration early.
func forEachSubset(mask, k int, fn func(sub int) bool) {
	var rec func(remaining, start, chosen int) bool
	rec = func(remaining, start, chosen int) bool {
		if remaining == 0 {
			return fn(chosen)
		}
		for bit := start; bit < MaxDomains; bit++ {
			if mask&(1<<bit) == 0 {
				continue
			}
			if rec(remaining-1, bit+1, chosen|(1<<bit)) {
				return true
			}
		}
		return false
	}
	rec(k, 0, 0)
}
