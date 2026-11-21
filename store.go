package admission

import (
	"fmt"
	"sync"
)

// Store atomically publishes complete graph versions. Compilation and every
// isolation check happen before the current pointer is replaced.
type Store struct {
	mu      sync.RWMutex
	current *Snapshot
	next    uint64
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{next: 1} }

// Publish compiles and validates g. If any volume, permission, reachability,
// cycle, overflow, or isolation check fails, the current version is unchanged.
func (s *Store) Publish(g Graph) (*Snapshot, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalid)
	}
	cg, err := compileGraph(g)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	version := s.next
	if version == 0 {
		version = 1
	}
	s.next = version + 1
	snap := &Snapshot{graph: cg, version: version}
	s.current = snap
	return snap, nil
}

// Current returns the latest published snapshot. The snapshot is immutable.
func (s *Store) Current() *Snapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Handle opens a handle against the current snapshot.
func (s *Store) Handle(name string) (*Handle, error) {
	snap := s.Current()
	if snap == nil {
		return nil, fmt.Errorf("%w: store has no published version", ErrNotFound)
	}
	return snap.Handle(name)
}
