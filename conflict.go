package netsec

import (
	"fmt"
	"sort"
)

// buildConflictSet extracts an inclusion-minimal infeasible request subset and
// the reviewable facts explaining it.
func (s *solver) buildConflictSet() ConflictSet {
	subset := s.minimalInfeasibleSubset()

	pairs := s.pairsWithin(subset)
	reasons := s.reasonsWithin(subset)

	ids := make([]string, 0, len(subset))
	for _, i := range subset {
		ids = append(ids, s.in.Requests[i].ID)
	}
	return ConflictSet{
		RequestSubset: ids,
		PairConflicts: pairs,
		Reasons:       reasons,
	}
}

// minimalInfeasibleSubset greedily shrinks the full request set while keeping
// it infeasible. The result is inclusion-minimal: every strict subset is
// feasible.
func (s *solver) minimalInfeasibleSubset() []int {
	subset := make([]int, s.nr)
	for i := range subset {
		subset[i] = i
	}

	changed := true
	for changed {
		changed = false
		for pos := 0; pos < len(subset); pos++ {
			trial := append(append([]int(nil), subset[:pos]...), subset[pos+1:]...)
			if len(trial) > 0 && !s.subsetFeasible(trial) {
				subset = trial
				changed = true
				break
			}
		}
	}

	sort.Ints(subset)
	return subset
}

// subsetFeasible runs a bounded search on only the given request indices,
// reusing the solver's purpose, capacity and conflict tables.
func (s *solver) subsetFeasible(indices []int) bool {
	f := &feasibilitySearch{
		solver:  s,
		indices: indices,
		mask:    (1 << s.nd) - 1,
		assign:  make([]int, s.nr),
		used:    make([][]int, s.nd),
	}
	for d := range f.used {
		f.used[d] = append([]int(nil), s.capacities[d]...)
	}
	for i := range f.assign {
		f.assign[i] = -1
	}
	return f.run(0)
}

type feasibilitySearch struct {
	solver  *solver
	indices []int
	mask    int
	assign  []int
	used    [][]int
}

func (f *feasibilitySearch) run(pos int) bool {
	if pos == len(f.indices) {
		return true
	}
	req := f.indices[pos]
	s := f.solver
	need := s.in.Requests[req].Capacity
	pIdx := s.purposeIndex[s.in.Requests[req].KeyPurpose]

	for _, d := range s.cheapOrder[req] {
		if f.used[d][pIdx] < need || !f.clearOfConflicts(req, d) {
			continue
		}
		f.assign[req] = d
		f.used[d][pIdx] -= need
		if f.run(pos + 1) {
			return true
		}
		f.used[d][pIdx] += need
		f.assign[req] = -1
	}
	return false
}

func (f *feasibilitySearch) clearOfConflicts(req, d int) bool {
	s := f.solver
	for _, i := range f.indices {
		if f.assign[i] == d && s.conflict[req]&(1<<uint(i)) != 0 {
			return false
		}
	}
	return true
}

func (s *solver) pairsWithin(subset []int) []PairConflict {
	inSet := make(map[int]bool, len(subset))
	for _, i := range subset {
		inSet[i] = true
	}

	out := make([]PairConflict, 0)
	seen := make(map[Pair]bool)
	for _, a := range subset {
		for _, b := range subset {
			if a >= b || s.conflict[a]&(1<<uint(b)) == 0 {
				continue
			}
			pair := s.pairOf(a, b)
			if seen[pair] {
				continue
			}
			seen[pair] = true
			source, ok := s.pairSource[pair]
			if !ok {
				source = "explicit"
			}
			out = append(out, PairConflict{Pair: pair, Source: source})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Pair.A != out[b].Pair.A {
			return out[a].Pair.A < out[b].Pair.A
		}
		return out[a].Pair.B < out[b].Pair.B
	})
	return out
}

func (s *solver) reasonsWithin(subset []int) []ConflictReason {
	inSet := make(map[int]bool, len(subset))
	for _, i := range subset {
		inSet[i] = true
	}

	out := make([]ConflictReason, 0)

	// Aggregate per-purpose demand and per-domain capacity inside the subset.
	demand := make(map[string]int)
	maxSingle := make(map[string]int)
	for _, i := range subset {
		r := &s.in.Requests[i]
		demand[r.KeyPurpose] += r.Capacity
		if r.Capacity > maxSingle[r.KeyPurpose] {
			maxSingle[r.KeyPurpose] = r.Capacity
		}
	}

	for _, purpose := range s.purposes {
		pIdx := s.purposeIndex[purpose]
		totalCapacity := 0
		maxCapacity := 0
		supporting := make([]string, 0)
		for d := range s.in.Domains {
			c := s.capacities[d][pIdx]
			totalCapacity += c
			if c > maxCapacity {
				maxCapacity = c
			}
			if c > 0 {
				supporting = append(supporting, s.in.Domains[d].ID)
			}
		}

		required := demand[purpose]
		reqIDs := s.requestIDsForPurpose(subset, purpose)

		switch {
		case totalCapacity == 0:
			out = append(out, ConflictReason{
				Kind:       "unsupported_purpose",
				Detail:     fmt.Sprintf("no enabled domain supports key purpose %q", purpose),
				RequestIDs: reqIDs,
				Purpose:    purpose,
				Required:   required,
			})
		case required > totalCapacity:
			out = append(out, ConflictReason{
				Kind: "purpose_capacity",
				Detail: fmt.Sprintf("key purpose %q needs %d units but the domains jointly offer %d",
					purpose, required, totalCapacity),
				RequestIDs: reqIDs,
				Purpose:    purpose,
				Required:   required,
				Available:  totalCapacity,
			})
		}

		// When no domain serves the purpose, unsupported_purpose already
		// captures the blocking fact; do not emit a redundant reason.
		if totalCapacity > 0 && maxSingle[purpose] > maxCapacity {
			out = append(out, ConflictReason{
				Kind: "single_request_capacity",
				Detail: fmt.Sprintf(
					"key purpose %q has a request needing %d units but no single domain offers more than %d",
					purpose, maxSingle[purpose], maxCapacity),
				RequestIDs: reqIDs,
				Purpose:    purpose,
				Required:   maxSingle[purpose],
				Available:  maxCapacity,
			})
		}
	}

	// When only pairwise constraints block placement, pair conflicts already
	// carry the full explanation; nothing else to add here.
	return out
}

func (s *solver) requestIDsForPurpose(subset []int, purpose string) []string {
	out := make([]string, 0)
	for _, i := range subset {
		if s.in.Requests[i].KeyPurpose == purpose {
			out = append(out, s.in.Requests[i].ID)
		}
	}
	sort.Strings(out)
	return out
}
