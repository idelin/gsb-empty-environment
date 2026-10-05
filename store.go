// Package blockgc implements mark-and-sweep garbage collection for a shared,
// content-addressed block store with an updatable root manifest.
package blockgc

import "sync"

// BlockID uniquely identifies a block (typically a content hash).
type BlockID string

// Block is an immutable unit of storage. Refs are outgoing references to
// other blocks; blocks are treated as immutable once stored.
type Block struct {
	ID   BlockID
	Data []byte
	Refs []BlockID
}

// WriteBarrier is invoked by the store after a new block is inserted. The
// garbage collector installs one so that blocks created while a collection
// is in flight (and the blocks they reference) cannot be reclaimed from
// under the writer.
type WriteBarrier func(newBlock BlockID, refs []BlockID)

// Store is a content-addressed block store. Blocks scheduled for deletion
// are first moved to a quarantine area: they remain physically present and
// can be restored until they are confirmed and permanently deleted.
type Store struct {
	mu          sync.Mutex
	blocks      map[BlockID]*Block
	quarantined map[BlockID]bool
	barrier     WriteBarrier
}

// NewStore returns an empty block store.
func NewStore() *Store {
	return &Store{
		blocks:      make(map[BlockID]*Block),
		quarantined: make(map[BlockID]bool),
	}
}

// SetBarrier installs the write barrier invoked on every Put. Used by the
// collector; there is at most one barrier.
func (s *Store) SetBarrier(b WriteBarrier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.barrier = b
}

// Put inserts a block. Blocks are immutable: re-putting an existing ID is a
// no-op. The write barrier runs after the block is visible to the collector.
func (s *Store) Put(b Block) {
	s.mu.Lock()
	if _, ok := s.blocks[b.ID]; ok {
		s.mu.Unlock()
		return
	}
	cp := b
	cp.Refs = append([]BlockID(nil), b.Refs...)
	s.blocks[b.ID] = &cp
	barrier := s.barrier
	s.mu.Unlock()
	if barrier != nil {
		barrier(b.ID, cp.Refs)
	}
}

// Get returns a block by ID. Quarantined blocks are still readable: readers
// holding an old root may reference them, and quarantine exists precisely so
// they can be restored before deletion is confirmed.
func (s *Store) Get(id BlockID) (Block, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blocks[id]
	if !ok {
		return Block{}, false
	}
	return *b, true
}

// LiveIDs returns the IDs of all non-quarantined blocks.
func (s *Store) LiveIDs() []BlockID {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BlockID, 0, len(s.blocks))
	for id := range s.blocks {
		if !s.quarantined[id] {
			out = append(out, id)
		}
	}
	return out
}

// IsQuarantined reports whether the block is currently quarantined.
func (s *Store) IsQuarantined(id BlockID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quarantined[id]
}

// Quarantine moves blocks into the quarantine area. They remain stored and
// readable, but are excluded from LiveIDs and eligible for deletion.
func (s *Store) Quarantine(ids ...BlockID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if _, ok := s.blocks[id]; ok {
			s.quarantined[id] = true
		}
	}
}

// Restore moves blocks back out of quarantine. Transitive restore of
// referenced blocks is the caller's responsibility (see Collector).
func (s *Store) Restore(ids ...BlockID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.quarantined, id)
	}
}

// Delete permanently removes quarantined blocks. Blocks that are no longer
// quarantined (e.g. resurrected by a concurrent write barrier) are skipped,
// so a stale deletion list can never remove a live block.
func (s *Store) Delete(ids ...BlockID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if s.quarantined[id] {
			delete(s.blocks, id)
			delete(s.quarantined, id)
		}
	}
}

// QuarantinedIDs returns the IDs currently in quarantine (used by tests and
// crash recovery).
func (s *Store) QuarantinedIDs() []BlockID {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BlockID, 0, len(s.quarantined))
	for id := range s.quarantined {
		out = append(out, id)
	}
	return out
}
