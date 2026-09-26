// Package blockstore implements a persistent byte-sequence store built on
// fixed-size physical blocks. Slices share blocks, edits are copy-on-write,
// and memory accounting deduplicates by physical block.
package blockstore

import (
	"errors"
	"fmt"
)

var (
	// ErrOutOfRange is returned when an offset or length falls outside the
	// logical bounds of a slice.
	ErrOutOfRange = errors.New("blockstore: out of range")
	// ErrLimitExceeded is returned when an operation would push physical
	// block usage past the store limit. The operation is rolled back.
	ErrLimitExceeded = errors.New("blockstore: memory limit exceeded")
	// ErrReleased is returned when reading from a released slice.
	ErrReleased = errors.New("blockstore: slice released")
	// ErrEditorClosed is returned when using a committed or cancelled editor.
	ErrEditorClosed = errors.New("blockstore: editor closed")
)

// block is one physical block. refs counts live slice extents pointing at
// it; a committed block with refs == 0 is reclaimed immediately.
type block struct {
	data []byte
	refs int
}

// Store owns all physical blocks and the memory ledger.
type Store struct {
	bs    int   // block size in bytes
	limit int64 // max (committed+temp) block bytes; 0 = unlimited

	blocks    map[uint64]*block
	nextID    uint64
	committed int // committed block count
	temps     map[uint64]bool
	tempCount int

	copied  int64 // cumulative bytes copied out of old blocks (COW)
	written int64 // cumulative new data bytes written into fresh blocks
	peak    int64 // high-water mark of (committed+temp)*bs

	slices map[*Slice]struct{}

	onAlloc func() // test hook, called after each temp allocation
}

// NewStore creates a store with the given physical block size and byte
// limit (0 means unlimited).
func NewStore(blockSize int, limit int64) *Store {
	if blockSize < 1 {
		panic("blockstore: block size must be >= 1")
	}
	if limit < 0 {
		panic("blockstore: limit must be >= 0")
	}
	return &Store{
		bs:     blockSize,
		limit:  limit,
		blocks: make(map[uint64]*block),
		temps:  make(map[uint64]bool),
		slices: make(map[*Slice]struct{}),
	}
}

// BlockSize returns the physical block size in bytes.
func (st *Store) BlockSize() int { return st.bs }

// Stats is a point-in-time snapshot of the memory ledger.
type Stats struct {
	BlockSize    int   // physical block size
	Blocks       int   // committed physical blocks (deduplicated)
	TempBlocks   int   // uncommitted temp blocks of in-flight edits
	LiveBytes    int64 // Blocks * BlockSize
	TempBytes    int64 // TempBlocks * BlockSize
	PeakBytes    int64 // high-water mark of LiveBytes+TempBytes
	CopiedBytes  int64 // cumulative COW bytes copied out of old blocks
	WrittenBytes int64 // cumulative new payload bytes written
	LiveSlices   int   // registered (unreleased) slices
	IndexBytes   int   // sum of encoded index sizes over live slices
}

// Stats returns the current ledger. Physical blocks are counted once no
// matter how many slices reference them.
func (st *Store) Stats() Stats {
	s := Stats{
		BlockSize:    st.bs,
		Blocks:       st.committed,
		TempBlocks:   st.tempCount,
		LiveBytes:    int64(st.committed) * int64(st.bs),
		TempBytes:    int64(st.tempCount) * int64(st.bs),
		PeakBytes:    st.peak,
		CopiedBytes:  st.copied,
		WrittenBytes: st.written,
		LiveSlices:   len(st.slices),
	}
	for sl := range st.slices {
		s.IndexBytes += len(sl.enc)
	}
	return s
}

func (st *Store) observePeak() {
	total := int64(st.committed+st.tempCount) * int64(st.bs)
	if total > st.peak {
		st.peak = total
	}
}

// txn tracks temp allocations of one in-flight edit so it can be rolled
// back as a whole.
type txn struct {
	st      *Store
	tempIDs []uint64
}

func (st *Store) beginTxn() *txn { return &txn{st: st} }

// alloc reserves one temp block. Temp blocks count toward the ledger and
// the limit while the edit is in flight.
func (t *txn) alloc() (uint64, error) {
	st := t.st
	if st.limit > 0 && int64(st.committed+st.tempCount+1)*int64(st.bs) > st.limit {
		return 0, ErrLimitExceeded
	}
	id := st.nextID
	st.nextID++
	st.blocks[id] = &block{data: make([]byte, st.bs)}
	st.temps[id] = true
	st.tempCount++
	t.tempIDs = append(t.tempIDs, id)
	st.observePeak()
	if st.onAlloc != nil {
		st.onAlloc()
	}
	return id, nil
}

// rollback frees every temp block the txn allocated.
func (t *txn) rollback() {
	st := t.st
	for _, id := range t.tempIDs {
		if st.temps[id] {
			delete(st.temps, id)
			delete(st.blocks, id)
			st.tempCount--
		}
	}
	t.tempIDs = nil
}

// commit promotes temp blocks referenced by extents and frees the rest.
func (t *txn) commit(extents []uint64) {
	st := t.st
	counts := make(map[uint64]int, len(extents))
	for _, id := range extents {
		counts[id]++
	}
	for id, n := range counts {
		b := st.blocks[id]
		if st.temps[id] {
			delete(st.temps, id)
			st.tempCount--
			st.committed++
		}
		b.refs += n
	}
	t.rollback() // free unreferenced leftovers
	st.observePeak()
	t.tempIDs = nil
}

// VerifyLedger independently walks every live slice, recounts block
// references, and cross-checks the store's accounting. It returns an error
// describing the first mismatch found.
func (st *Store) VerifyLedger() error {
	want := make(map[uint64]int)
	for sl := range st.slices {
		if sl.released {
			return fmt.Errorf("ledger: released slice %p still registered", sl)
		}
		for _, id := range sl.exts {
			want[id]++
		}
	}
	for id, n := range want {
		b, ok := st.blocks[id]
		if !ok {
			return fmt.Errorf("ledger: slice references missing block %d", id)
		}
		if st.temps[id] {
			return fmt.Errorf("ledger: slice references temp block %d", id)
		}
		if b.refs != n {
			return fmt.Errorf("ledger: block %d refs=%d, live slices reference it %d times", id, b.refs, n)
		}
	}
	committed := 0
	for id, b := range st.blocks {
		if st.temps[id] {
			continue
		}
		committed++
		if want[id] == 0 {
			return fmt.Errorf("ledger: committed block %d leaked (refs=%d, no live slice)", id, b.refs)
		}
	}
	if committed != st.committed {
		return fmt.Errorf("ledger: committed counter=%d, actual committed blocks=%d", st.committed, committed)
	}
	if len(st.temps) != st.tempCount {
		return fmt.Errorf("ledger: temp counter=%d, actual temp blocks=%d", st.tempCount, len(st.temps))
	}
	return nil
}
