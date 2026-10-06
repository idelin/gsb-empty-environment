package gcrt

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
)

// Store keeps constraints under transactional control. Every commit produces
// an immutable version that can be queried later; version 0 is the initial
// empty system. Each version retains its original constraints, so removing
// any constraint restores the true solution set of the survivors.
type Store struct {
	mu       sync.RWMutex
	cur      *System
	versions []*System // versions[i] is the state after i commits
	nextID   int64
	tx       *transaction
}

type transaction struct {
	store *Store
	adds  []Constraint
	dels  []int64
	done  bool
}

// NewStore returns an empty store at version 0.
func NewStore() *Store {
	return &Store{cur: NewSystem(), versions: []*System{NewSystem()}, nextID: 1}
}

// Begin opens a transaction. Only one transaction may be open at a time.
func (s *Store) Begin() (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil {
		return nil, errors.New("gcrt: a transaction is already open")
	}
	tx := &transaction{store: s}
	s.tx = tx
	return tx, nil
}

// Transaction is a buffered set of constraint additions and removals.
type Transaction = transaction

// Add buffers a new constraint x ≡ res (mod mod) and returns its ID. IDs are
// assigned immediately and are not reused, even after a rollback.
func (tx *transaction) Add(mod, res *big.Int) (int64, error) {
	if tx.done {
		return 0, errors.New("gcrt: transaction is closed")
	}
	c := Constraint{ID: tx.store.nextID, Mod: new(big.Int).Set(mod), Res: new(big.Int).Set(res)}
	if err := c.validate(); err != nil {
		return 0, err
	}
	tx.store.nextID++
	tx.adds = append(tx.adds, c)
	return c.ID, nil
}

// Remove buffers the removal of the constraint with the given ID.
func (tx *transaction) Remove(id int64) error {
	if tx.done {
		return errors.New("gcrt: transaction is closed")
	}
	tx.dels = append(tx.dels, id)
	return nil
}

// Commit applies the buffered changes and publishes a new version.
func (tx *transaction) Commit() (int, error) {
	if tx.done {
		return 0, errors.New("gcrt: transaction is closed")
	}
	tx.done = true
	s := tx.store
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tx = nil
	for _, c := range tx.adds {
		if err := s.cur.Add(c); err != nil {
			return 0, err
		}
	}
	for _, id := range tx.dels {
		s.cur.Remove(id)
	}
	s.versions = append(s.versions, s.cur.Clone())
	return len(s.versions) - 1, nil
}

// Rollback discards the buffered changes; the store is left untouched.
func (tx *transaction) Rollback() {
	if tx.done {
		return
	}
	tx.done = true
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()
	tx.store.tx = nil
}

// Version returns the current version number.
func (s *Store) Version() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.versions) - 1
}

// Solution returns the merged solution recorded at the given version.
func (s *Store) Solution(version int) (Solution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if version < 0 || version >= len(s.versions) {
		return Solution{}, fmt.Errorf("gcrt: no such version %d", version)
	}
	return cloneSolution(s.versions[version].sol), nil
}

// Constraints returns the original constraints active at the given version,
// in insertion order.
func (s *Store) Constraints(version int) ([]Constraint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if version < 0 || version >= len(s.versions) {
		return nil, fmt.Errorf("gcrt: no such version %d", version)
	}
	cs := s.versions[version].Constraints()
	for i := range cs {
		cs[i] = cloneConstraint(cs[i])
	}
	return cs, nil
}
