package planner

import "fmt"

// state tracks the incremental disk-usage simulation while an order is
// built or replayed. Shared blocks are accounted once because each block
// is applied exactly once and its final bytes are freed exactly once.
type state struct {
	usage int64
	peak  int64
	unpub []int  // unpub[d]: dependents of d not yet published
	indeg []int  // remaining unpublished deps (readiness)
	done  []bool // published blocks
}

func newState(p *Planner) *state {
	s := &state{
		unpub: make([]int, p.n),
		indeg: append([]int(nil), p.indegree...),
		done:  make([]bool, p.n),
	}
	for i := 0; i < p.n; i++ {
		s.unpub[i] = len(p.dependents[i])
	}
	return s
}

// apply publishes block i on top of the current state and returns the list
// of blocks whose final bytes were released, plus the peak before the move
// (needed by undo). i must be ready (all deps published).
func (s *state) apply(p *Planner, i int) (freed []int, oldPeak int64) {
	oldPeak = s.peak
	// download + decompress: compressed and temp coexist (peak candidate).
	s.usage += p.compressed[i] + p.temp[i]
	if s.usage > s.peak {
		s.peak = s.usage
	}
	// atomic publish: temp and compressed freed, final remains. Usage can
	// rise here when final exceeds compressed+temp, so re-check the peak.
	s.usage += p.final[i] - p.compressed[i] - p.temp[i]
	if s.usage > s.peak {
		s.peak = s.usage
	}
	// release finals of deps whose last dependent just published.
	for _, d := range p.deps[i] {
		s.unpub[d]--
		if s.unpub[d] == 0 && !p.output[d] {
			s.usage -= p.final[d]
			freed = append(freed, d)
		}
	}
	for _, j := range p.dependents[i] {
		s.indeg[j]--
	}
	s.done[i] = true
	return freed, oldPeak
}

// undo reverses apply exactly.
func (s *state) undo(p *Planner, i int, freed []int, oldPeak int64) {
	s.done[i] = false
	for _, j := range p.dependents[i] {
		s.indeg[j]++
	}
	for _, d := range freed {
		s.usage += p.final[d]
	}
	for _, d := range p.deps[i] {
		s.unpub[d]++
	}
	s.usage -= p.final[i] // net usage delta of apply is +final[i] (-freed)
	s.peak = oldPeak
}

// eval replays a complete topological order and returns the peak usage.
func (p *Planner) eval(order []int) int64 {
	s := newState(p)
	for _, i := range order {
		s.apply(p, i)
	}
	return s.peak
}

// Eval validates order (a permutation respecting dependencies) and returns
// the peak disk usage of executing it.
func (p *Planner) Eval(order []int) (int64, error) {
	if len(order) != p.n {
		return 0, fmt.Errorf("order has %d blocks, want %d", len(order), p.n)
	}
	seen := make([]bool, p.n)
	position := make([]int, p.n)
	for pos, i := range order {
		if i < 0 || i >= p.n {
			return 0, fmt.Errorf("order contains invalid block index %d", i)
		}
		if seen[i] {
			return 0, fmt.Errorf("order repeats block %q", p.ids[i])
		}
		seen[i] = true
		position[i] = pos
	}
	for i := 0; i < p.n; i++ {
		for _, d := range p.deps[i] {
			if position[d] > position[i] {
				return 0, fmt.Errorf("block %q scheduled before its dependency %q", p.ids[i], p.ids[d])
			}
		}
	}
	return p.eval(order), nil
}

// OrderIDs converts an internal order to block IDs.
func (p *Planner) OrderIDs(order []int) []string {
	ids := make([]string, len(order))
	for k, i := range order {
		ids[k] = p.ids[i]
	}
	return ids
}
