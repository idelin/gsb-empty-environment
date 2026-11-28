// Package cowfs implements a ZFS-style copy-on-write storage layer:
// birth-transaction-group block identity, per-snapshot deadlists with exact
// space accounting, and a four-list ARC that is invalidated in lockstep with
// physical block frees.
package cowfs

import "sync"

// PhysID is the physical address of a block on the backing device.
type PhysID uint64

// Block is an immutable physical block. Every allocation, including an
// allocation that reuses a previously freed PhysID, produces a brand new
// block carrying the birth transaction group of the write that created it.
type Block struct {
	ID       PhysID
	BirthTxg uint64
	Size     int
	Data     []byte
}

// InvalidateHook is invoked whenever a physical block is freed. The ARC uses
// it to drop both cached data and ghost metadata so that a later id reuse can
// never observe a stale entry.
type InvalidateHook func(id PhysID)

// BlockStore models the on-disk block allocator. Freed ids go onto a free list
// and may be handed out again; AllocatedBytes therefore reflects live physical
// consumption and must return to zero once every block has been freed.
type BlockStore struct {
	mu        sync.Mutex
	blocks    map[PhysID]Block
	freeIDs   []PhysID
	nextID    PhysID
	allocated int
	hook      InvalidateHook
}

// NewBlockStore creates an empty allocator.
func NewBlockStore() *BlockStore {
	return &BlockStore{
		blocks: make(map[PhysID]Block),
		nextID: 1,
	}
}

// SetInvalidateHook registers the callback fired after every successful free.
func (s *BlockStore) SetInvalidateHook(h InvalidateHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = h
}

// Alloc writes a new physical block stamped with birthTxg and returns its id.
// Reused ids are indistinguishable from fresh ids to callers: the returned
// block fully replaces whatever occupied the id before it was freed.
func (s *BlockStore) Alloc(data []byte, birthTxg uint64) (PhysID, Block) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var id PhysID
	if n := len(s.freeIDs); n > 0 {
		id = s.freeIDs[n-1]
		s.freeIDs = s.freeIDs[:n-1]
	} else {
		id = s.nextID
		s.nextID++
	}

	b := Block{
		ID:       id,
		BirthTxg: birthTxg,
		Size:     len(data),
		Data:     append([]byte(nil), data...),
	}
	s.blocks[id] = b
	s.allocated += b.Size
	return id, b
}

// Free releases a physical block, returns its size and fires the invalidation
// hook outside the allocator lock. Freeing an unknown id reports false.
func (s *BlockStore) Free(id PhysID) (size int, ok bool) {
	s.mu.Lock()
	b, exists := s.blocks[id]
	if !exists {
		s.mu.Unlock()
		return 0, false
	}
	delete(s.blocks, id)
	s.freeIDs = append(s.freeIDs, id)
	s.allocated -= b.Size
	hook := s.hook
	s.mu.Unlock()

	if hook != nil {
		hook(id)
	}
	return b.Size, true
}

// Read returns a live block. The returned data slice must not be modified.
func (s *BlockStore) Read(id PhysID) (Block, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blocks[id]
	return b, ok
}

// AllocatedBytes is the number of bytes currently held by live blocks.
func (s *BlockStore) AllocatedBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allocated
}

// LiveBlocks is the number of currently allocated physical blocks.
func (s *BlockStore) LiveBlocks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blocks)
}

// FreeListLen reports how many ids are awaiting reuse.
func (s *BlockStore) FreeListLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.freeIDs)
}

// forEachLiveBlock invokes fn for every currently allocated block.
func (s *BlockStore) forEachLiveBlock(fn func(Block)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.blocks {
		fn(b)
	}
}
