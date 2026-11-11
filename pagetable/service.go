package pagetable

import "sync/atomic"

// Service is an in-memory snapshot service over a physical PageStore.
// Current returns the live snapshot with no locking; Update serializes edits
// and publishes a new PML4 root atomically. Old snapshots remain readable
// concurrently because transactions never mutate pre-existing pages.
type Service struct {
	store PageStore
	alloc Allocator
	root  atomic.Uint64
}

// NewService starts with the given PML4 root already present in store.
func NewService(store PageStore, alloc Allocator, initialRoot uint64) *Service {
	s := &Service{store: store, alloc: alloc}
	s.root.Store(initialRoot)
	return s
}

// Current returns the published snapshot.
func (s *Service) Current() *Snapshot {
	return &Snapshot{store: s.store, root: s.root.Load()}
}

// Update applies one or more patches as a single transaction.
//
// Validation and all allocations happen before the new root is published; on
// any error every frame allocated by the transaction is returned to the
// allocator and the previously published root stays valid. On success the new
// root is published with a single atomic word store.
func (s *Service) Update(patches ...Patch) (uint64, error) {
	t := newTxn(s.store, s.alloc)
	root := s.root.Load()
	var err error
	for _, p := range patches {
		root, err = t.apply(root, p)
		if err != nil {
			t.rollback()
			return 0, err
		}
	}
	root, err = t.commit(root)
	if err != nil {
		// commit already rolled back on failure.
		return 0, err
	}
	s.root.Store(root)
	return root, nil
}
