package scheduler

import "fmt"

// Verify independently checks that rounds form a valid schedule for the
// current graph: every live edge appears exactly once, no round contains
// two edges sharing an endpoint, and the number of rounds equals the
// maximum degree. It returns nil on success and a descriptive error
// otherwise. It does not trust or inspect any internal coloring state.
func (s *Scheduler) Verify(rounds [][]int) error {
	if want := s.MaxDegree(); len(rounds) != want {
		return fmt.Errorf("got %d rounds, want %d (max degree)", len(rounds), want)
	}
	seen := make(map[int]bool, len(s.edges))
	for r, round := range rounds {
		usedU := make(map[int]bool)
		usedV := make(map[int]bool)
		for _, id := range round {
			e, ok := s.edges[id]
			if !ok {
				return fmt.Errorf("round %d contains unknown edge ID %d (stale or deleted edge)", r, id)
			}
			if seen[id] {
				return fmt.Errorf("edge %d appears more than once", id)
			}
			seen[id] = true
			if usedU[e.U] {
				return fmt.Errorf("round %d: left vertex %d used by two edges", r, e.U)
			}
			if usedV[e.V] {
				return fmt.Errorf("round %d: right vertex %d used by two edges", r, e.V)
			}
			usedU[e.U] = true
			usedV[e.V] = true
		}
	}
	if len(seen) != len(s.edges) {
		for id := range s.edges {
			if !seen[id] {
				return fmt.Errorf("edge %d is not scheduled in any round", id)
			}
		}
	}
	return nil
}
