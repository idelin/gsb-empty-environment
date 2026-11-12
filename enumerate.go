package netsec

import "fmt"

// maxEnumerations bounds exhaustive search so a mis-sized domain cannot turn
// a unit test into an unbounded loop.
const maxEnumerations = 2_000_000

// Enumerate exhaustively enumerates every legal integer assignment inside
// the declared event windows. Search is backtracking with partial-assignment
// pruning, and results are returned in lexicographic order over the event
// declaration order.
//
// It is intended for small time domains only: if the Cartesian product of
// the windows exceeds [maxEnumerations] an error is returned instead.
func (p *Problem) Enumerate() ([][]int64, error) {
	n := len(p.names)
	var product uint64 = 1
	for i := range p.names {
		size := uint64(p.hi[i]-p.lo[i]) + 1
		product *= size
		if product > maxEnumerations {
			return nil, fmt.Errorf("netsec: time domain too large to enumerate (>= %d assignments); tighten the integer windows", maxEnumerations)
		}
	}

	out := make([][]int64, 0)
	current := make([]int64, n)
	assigned := make([]bool, n)

	var backtrack func(i int)
	backtrack = func(i int) {
		if i == n {
			if !p.Legal(current) {
				return
			}
			out = append(out, append([]int64(nil), current...))
			return
		}
		for v := p.lo[i]; v <= p.hi[i]; v++ {
			current[i] = v
			assigned[i] = true
			if !partialLegal(p, current, assigned) {
				assigned[i] = false
				continue
			}
			backtrack(i + 1)
			assigned[i] = false
		}
		current[i] = 0
	}
	backtrack(0)
	return out, nil
}

// partialLegal prunes any prefix that already violates an ordering whose
// both endpoints are assigned. Bounds are enforced by the loop itself.
func partialLegal(p *Problem, current []int64, assigned []bool) bool {
	for _, ord := range p.orders {
		b := p.index[ord.Before]
		a := p.index[ord.After]
		if !assigned[b] || !assigned[a] {
			continue
		}
		if ord.Kind == StrictOrder {
			if !(current[b] < current[a]) {
				return false
			}
		} else if !(current[b] <= current[a]) {
			return false
		}
	}
	return true
}

// Coverage describes how a sample set relates to the exhaustive solution
// space of a small time domain.
type Coverage struct {
	// Total is the number of distinct legal assignments.
	Total int
	// Hit is the number of legal assignments present in the sample.
	Hit int
	// Missing lists up to limit legal assignments absent from the sample.
	Missing [][]int64
}

// CheckCoverage cross-checks sampled mutations against the exhaustive
// enumeration of the problem. It fails if any sampled mutation is illegal,
// and reports how many of the legal assignments the sample covers. Duplicate
// samples count once.
func (p *Problem) CheckCoverage(samples []Mutation, missingLimit int) (*Coverage, error) {
	all, err := p.Enumerate()
	if err != nil {
		return nil, err
	}
	legal := make(map[string][]int64, len(all))
	for _, t := range all {
		legal[fmt.Sprint(t)] = t
	}

	hit := make(map[string]bool)
	for k, m := range samples {
		if !p.Legal(m.Times) {
			return nil, fmt.Errorf("netsec: sample %d (seed %d, tag %q) is illegal: %v", k, m.Seed, m.Tag, m.Times)
		}
		key := fmt.Sprint(m.Times)
		if _, ok := legal[key]; !ok {
			return nil, fmt.Errorf("netsec: sample %d (seed %d) not present in exhaustive enumeration", k, m.Seed)
		}
		hit[key] = true
	}

	cov := &Coverage{Total: len(all), Hit: len(hit)}
	for _, t := range all {
		if len(cov.Missing) >= missingLimit {
			break
		}
		if !hit[fmt.Sprint(t)] {
			cov.Missing = append(cov.Missing, append([]int64(nil), t...))
		}
	}
	return cov, nil
}
