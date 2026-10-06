package gcrt

import (
	"fmt"
	"math/big"
)

// System is a mutable set of original constraints together with a cached
// merged solution. The original constraints are always retained: the merged
// residue alone is not invertible, so removing any constraint requires the
// true remaining set to restore the real solution set.
type System struct {
	order []int64 // constraint IDs in insertion order
	cons  map[int64]Constraint
	sol   Solution // cached merged solution over all active constraints
}

// NewSystem returns an empty system whose solution set is all integers.
func NewSystem() *System {
	return &System{
		cons: make(map[int64]Constraint),
		sol:  All(),
	}
}

// Add inserts a constraint. The cached solution is updated incrementally.
func (s *System) Add(c Constraint) error {
	if err := c.validate(); err != nil {
		return err
	}
	if _, dup := s.cons[c.ID]; dup {
		return fmt.Errorf("gcrt: duplicate constraint ID %d", c.ID)
	}
	s.cons[c.ID] = c
	s.order = append(s.order, c.ID)
	if s.sol.OK {
		r, m := c.normalized()
		nr, nm, ok := merge(s.sol.Res, s.sol.Period, r, m)
		if ok {
			s.sol = Solution{OK: true, Res: nr, Period: nm}
		} else {
			// Find the earliest active constraint conflicting with c.
			s.sol = Solution{OK: false, Conflict: [2]int64{s.earliestConflict(c), c.ID}}
		}
	}
	// If the system was already infeasible, the earliest conflicting pair is
	// unchanged by adding more constraints.
	return nil
}

// Remove deletes a constraint by ID and restores the true solution set of
// the remaining constraints via a full generalized merge.
func (s *System) Remove(id int64) bool {
	if _, ok := s.cons[id]; !ok {
		return false
	}
	delete(s.cons, id)
	next := s.order[:0]
	for _, oid := range s.order {
		if oid != id {
			next = append(next, oid)
		}
	}
	s.order = next
	s.recompute()
	return true
}

// Solution returns the cached merged solution of the active constraints.
func (s *System) Solution() Solution { return s.sol }

// Constraints returns the active constraints in insertion order.
func (s *System) Constraints() []Constraint {
	out := make([]Constraint, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.cons[id])
	}
	return out
}

// recompute restores the cached solution from the original constraints.
func (s *System) recompute() {
	s.sol = SolveConstraints(s.Constraints())
}

// earliestConflict returns the ID of the first active constraint (in
// insertion order) that is pairwise inconsistent with c.
func (s *System) earliestConflict(c Constraint) int64 {
	for _, id := range s.order {
		if id == c.ID {
			continue
		}
		if !pairwiseConsistent(s.cons[id], c) {
			return id
		}
	}
	return -1 // unreachable when a merge conflict was observed
}

// Clone returns a deep copy safe to keep as an immutable snapshot.
func (s *System) Clone() *System {
	out := &System{
		order: append([]int64(nil), s.order...),
		cons:  make(map[int64]Constraint, len(s.cons)),
		sol:   cloneSolution(s.sol),
	}
	for id, c := range s.cons {
		out.cons[id] = cloneConstraint(c)
	}
	return out
}

func cloneConstraint(c Constraint) Constraint {
	return Constraint{ID: c.ID, Mod: new(big.Int).Set(c.Mod), Res: new(big.Int).Set(c.Res)}
}

func cloneSolution(s Solution) Solution {
	out := Solution{OK: s.OK, Conflict: s.Conflict}
	if s.Res != nil {
		out.Res = new(big.Int).Set(s.Res)
	}
	if s.Period != nil {
		out.Period = new(big.Int).Set(s.Period)
	}
	return out
}
