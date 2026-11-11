package netsec

import (
	"errors"
	"sort"
)

// Solve limits mandated by the scheduling host contract.
const (
	MaxRequests = 20
	MaxDomains  = 6
)

// ErrInfeasible is returned when the requests cannot be placed under the
// key-purpose, capacity and isolation constraints. The returned *ConflictSet
// carries an independently reviewable explanation.
var ErrInfeasible = errors.New("netsec: no feasible domain assignment")

// Solve computes an optimal assignment for in.
//
// The primary objective is minimizing the number of enabled domains. Among
// all plans with that minimum count, the secondary objective is minimizing
// the summed latency cost. On infeasibility it returns a non-nil
// *ConflictSet together with ErrInfeasible.
func Solve(in Input) (*Plan, *ConflictSet, error) {
	if err := validate(in); err != nil {
		return nil, nil, err
	}
	s := newSolver(in)

	fullMask := (1 << s.nd) - 1
	bestCost, fullAssignment, feasible := s.search(fullMask)
	if !feasible {
		cs := s.buildConflictSet()
		return nil, &cs, ErrInfeasible
	}

	// Enumerate enabled-domain subsets in increasing cardinality. The first
	// feasible cardinality is the minimum enabled domain count.
	for k := 1; k < s.nd; k++ {
		best, bestMask, bestAssign := 0, 0, []int(nil)
		forEachSubset(fullMask, k, func(mask int) bool {
			// Each subset is optimized independently: a higher-cost small
			// subset must not be pruned by the full-set incumbent.
			cost, assignment, ok := s.search(mask)
			if ok {
				if bestMask == 0 || cost < best {
					best, bestMask, bestAssign = cost, mask, assignment
				}
			}
			return false
		})
		if bestMask != 0 {
			plan := s.buildPlan(bestMask, best, bestAssign)
			return &plan, nil, nil
		}
	}

	plan := s.buildPlan(fullMask, bestCost, fullAssignment)
	return &plan, nil, nil
}

func validate(in Input) error {
	nr, nd := len(in.Requests), len(in.Domains)
	if nr > MaxRequests {
		return errors.New("netsec: too many requests")
	}
	if nd > MaxDomains {
		return errors.New("netsec: too many domains")
	}
	if nr == 0 {
		return errors.New("netsec: at least one request is required")
	}

	reqIDs := make(map[string]int, nr)
	for i := range in.Requests {
		r := &in.Requests[i]
		if r.ID == "" {
			return errors.New("netsec: request ID must not be empty")
		}
		if _, dup := reqIDs[r.ID]; dup {
			return errors.New("netsec: duplicate request ID: " + r.ID)
		}
		reqIDs[r.ID] = i
		if r.Capacity <= 0 {
			return errors.New("netsec: request capacity must be positive: " + r.ID)
		}
		if r.KeyPurpose == "" {
			return errors.New("netsec: request key purpose must not be empty: " + r.ID)
		}
	}

	domIDs := make(map[string]int, nd)
	for i := range in.Domains {
		d := &in.Domains[i]
		if d.ID == "" {
			return errors.New("netsec: domain ID must not be empty")
		}
		if _, dup := domIDs[d.ID]; dup {
			return errors.New("netsec: duplicate domain ID: " + d.ID)
		}
		domIDs[d.ID] = i
		for purpose, capValue := range d.Purposes {
			if purpose == "" {
				return errors.New("netsec: domain purpose must not be empty: " + d.ID)
			}
			if capValue < 0 {
				return errors.New("netsec: domain capacity must be non-negative: " + d.ID)
			}
		}
	}

	seenPairs := make(map[Pair]bool)
	for _, p := range in.MustSeparate {
		if _, ok := reqIDs[p.A]; !ok {
			return errors.New("netsec: MustSeparate references unknown request: " + p.A)
		}
		if _, ok := reqIDs[p.B]; !ok {
			return errors.New("netsec: MustSeparate references unknown request: " + p.B)
		}
		if p.A == p.B {
			return errors.New("netsec: MustSeparate cannot reference the same request twice: " + p.A)
		}
		if seenPairs[p.canonical()] {
			continue
		}
		seenPairs[p.canonical()] = true
	}

	if len(in.Cost) != nr {
		return errors.New("netsec: cost matrix must have one row per request")
	}
	for i, row := range in.Cost {
		if len(row) != nd {
			return errors.New("netsec: cost row length must equal domain count")
		}
		for _, c := range row {
			if c < 0 {
				return errors.New("netsec: latency cost must be non-negative for request: " +
					in.Requests[i].ID)
			}
		}
	}
	return nil
}

// canonical returns the pair with IDs ordered lexicographically.
func canonical(p Pair) Pair {
	if p.A > p.B {
		p.A, p.B = p.B, p.A
	}
	return p
}

func (p Pair) canonical() Pair { return canonical(p) }

// sortedDomainIndices returns the enabled domain indices in ascending order.
func sortedDomainIndices(mask, nd int) []int {
	out := make([]int, 0, nd)
	for d := 0; d < nd; d++ {
		if mask&(1<<d) != 0 {
			out = append(out, d)
		}
	}
	sort.Ints(out)
	return out
}
