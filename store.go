package gcrt

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Constraint is an original congruence x ≡ Res (mod Mod) as submitted by
// the caller. Res keeps its original (possibly negative) value; the solver
// normalizes internally. The store retains every original constraint so
// that removing any of them restores the true remaining solution set.
type Constraint struct {
	ID  uint64
	Res *big.Int
	Mod *big.Int
}

// Solution describes the solution set of one snapshot. When OK is true the
// solution set is { Res + k*Period : k ∈ ℤ }; the empty system yields
// Res = 0, Period = 1, i.e. all integers. When OK is false the system is
// inconsistent and Conflict holds the IDs of the earliest contradictory
// pair of original constraints (earliest by insertion order: the first
// constraint whose addition makes the prefix infeasible, paired with the
// earliest earlier constraint it conflicts with).
type Solution struct {
	OK       bool
	Res      *big.Int
	Period   *big.Int
	Conflict [2]uint64
}

type snapshot struct {
	cons map[uint64]Constraint
	sol  Solution
}

// Store is a versioned constraint store. Version 0 is the empty system.
// Committed snapshots are immutable and remain queryable forever.
type Store struct {
	nextID   uint64
	versions []*snapshot
}

// NewStore returns a store whose only version (0) is the empty system.
func NewStore() *Store {
	s := &Store{nextID: 1}
	s.versions = append(s.versions, &snapshot{
		cons: map[uint64]Constraint{},
		sol:  allIntegers(),
	})
	return s
}

func allIntegers() Solution {
	return Solution{OK: true, Res: new(big.Int), Period: big.NewInt(1)}
}

func inconsistent(a, b uint64) Solution {
	return Solution{OK: false, Conflict: [2]uint64{a, b}}
}

// Latest returns the index of the most recent committed version.
func (s *Store) Latest() int { return len(s.versions) - 1 }

// Solve returns the solution set of the given committed version.
func (s *Store) Solve(version int) (Solution, error) {
	if version < 0 || version >= len(s.versions) {
		return Solution{}, fmt.Errorf("gcrt: no version %d", version)
	}
	return cloneSolution(s.versions[version].sol), nil
}

// Constraints returns the original constraints of a version, ordered by ID.
func (s *Store) Constraints(version int) ([]Constraint, error) {
	if version < 0 || version >= len(s.versions) {
		return nil, fmt.Errorf("gcrt: no version %d", version)
	}
	cons := s.versions[version].cons
	out := make([]Constraint, 0, len(cons))
	for _, id := range sortedIDs(cons) {
		out = append(out, cloneConstraint(cons[id]))
	}
	return out, nil
}

// Tx is a transaction staging additions and removals against a base
// version. It is not safe for concurrent use.
type Tx struct {
	store *Store
	cons  map[uint64]Constraint
	done  bool
}

// Begin starts a transaction on top of the latest version.
func (s *Store) Begin() *Tx { return s.BeginAt(s.Latest()) }

// BeginAt starts a transaction on top of an arbitrary committed version,
// which is how old snapshots are branched from.
func (s *Store) BeginAt(version int) *Tx {
	snap := s.versions[version]
	cons := make(map[uint64]Constraint, len(snap.cons))
	for id, c := range snap.cons {
		cons[id] = cloneConstraint(c)
	}
	return &Tx{store: s, cons: cons}
}

// Add stages a new constraint x ≡ res (mod mod) and returns its identity.
// mod must be positive; res may be negative or arbitrarily large. IDs are
// never reused, even if the transaction is rolled back.
func (t *Tx) Add(mod, res *big.Int) (uint64, error) {
	if t.done {
		return 0, errors.New("gcrt: transaction already closed")
	}
	if mod == nil || mod.Sign() <= 0 {
		return 0, errors.New("gcrt: modulus must be a positive integer")
	}
	if res == nil {
		return 0, errors.New("gcrt: residue must not be nil")
	}
	id := t.store.nextID
	t.store.nextID++
	t.cons[id] = Constraint{ID: id, Res: new(big.Int).Set(res), Mod: new(big.Int).Set(mod)}
	return id, nil
}

// Remove stages the deletion of an original constraint. Because the store
// keeps every original constraint, the solution set after removal is
// recomputed from the true remaining constraints, not patched onto the
// previously merged residue.
func (t *Tx) Remove(id uint64) error {
	if t.done {
		return errors.New("gcrt: transaction already closed")
	}
	if _, ok := t.cons[id]; !ok {
		return fmt.Errorf("gcrt: no constraint %d", id)
	}
	delete(t.cons, id)
	return nil
}

// Commit solves the staged constraint set and appends it as a new
// immutable version, returning its index.
func (t *Tx) Commit() (int, error) {
	if t.done {
		return 0, errors.New("gcrt: transaction already closed")
	}
	t.done = true
	t.store.versions = append(t.store.versions, &snapshot{
		cons: t.cons,
		sol:  solve(t.cons),
	})
	return t.store.Latest(), nil
}

// Rollback discards the staged changes.
func (t *Tx) Rollback() { t.done = true }

// solve merges all constraints in ID order. On the first merge failure it
// reports the earliest earlier constraint that pairwise conflicts with the
// current one; a system of congruences is consistent iff every pair is, so
// such a pair always exists.
func solve(cons map[uint64]Constraint) Solution {
	ids := sortedIDs(cons)
	merged := Normalize(big.NewInt(0), big.NewInt(1))
	for _, id := range ids {
		cur := Normalize(cons[id].Res, cons[id].Mod)
		next, ok := Merge(merged, cur)
		if ok {
			merged = next
			continue
		}
		for _, jid := range ids {
			if jid >= id {
				break
			}
			if _, ok := Merge(Normalize(cons[jid].Res, cons[jid].Mod), cur); !ok {
				return inconsistent(jid, id)
			}
		}
		return inconsistent(id, id) // unreachable: inconsistency is pairwise
	}
	return Solution{OK: true, Res: merged.Res, Period: merged.Mod}
}

func sortedIDs(cons map[uint64]Constraint) []uint64 {
	ids := make([]uint64, 0, len(cons))
	for id := range cons {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneConstraint(c Constraint) Constraint {
	return Constraint{ID: c.ID, Res: new(big.Int).Set(c.Res), Mod: new(big.Int).Set(c.Mod)}
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
