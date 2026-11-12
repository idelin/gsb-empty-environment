package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

const (
	// PageSize is the Wasm page size in bytes.
	PageSize = 65536
	// MaxPages is the wasm32 memory limit: 65536 pages = 4 GiB.
	MaxPages = 65536
)

// ErrLimitExceeded is returned when a memory request would exceed the
// tenant budget or the module-declared maximum.
var ErrLimitExceeded = errors.New("netsec: memory limit exceeded")

// ErrOutOfBounds is returned when a guest pointer/length pair falls
// outside the current memory size.
var ErrOutOfBounds = errors.New("netsec: guest memory access out of bounds")

// Budget tracks a tenant's total memory budget in pages. It is shared
// across all of the tenant's instances, so usage accumulates
// cross-instance and is safe for concurrent use.
type Budget struct {
	total uint64
	used  atomic.Uint64
}

// NewBudget creates a tenant budget of totalPages pages.
func NewBudget(totalPages uint64) *Budget {
	return &Budget{total: totalPages}
}

// Total returns the budget's total pages.
func (b *Budget) Total() uint64 { return b.total }

// Used returns the pages currently charged to the tenant.
func (b *Budget) Used() uint64 { return b.used.Load() }

// tryAdd charges delta pages to the budget, or reports failure without
// changing usage. The comparison is arranged so the addition can never
// overflow or underflow.
func (b *Budget) tryAdd(delta uint64) bool {
	if delta == 0 {
		return true
	}
	for {
		used := b.used.Load()
		if delta > b.total || used > b.total-delta {
			return false
		}
		if b.used.CompareAndSwap(used, used+delta) {
			return true
		}
	}
}

// release returns delta pages to the budget.
func (b *Budget) release(delta uint64) {
	for {
		used := b.used.Load()
		if delta >= used {
			b.used.Store(0)
			return
		}
		if b.used.CompareAndSwap(used, used-delta) {
			return
		}
	}
}

// Instance guards one Wasm module instance's memory. The zero value is
// not usable; construct with NewInstance.
type Instance struct {
	budget   *Budget
	maxPages uint32

	mu     sync.Mutex
	pages  uint32
	mem    []byte
	closed bool
}

// NewInstance creates an instance with initialPages already committed and
// a module-declared limit of maxPages. Both are bounded by the tenant
// budget and the wasm32 limit.
func NewInstance(b *Budget, initialPages, maxPages uint32) (*Instance, error) {
	if maxPages > MaxPages {
		return nil, fmt.Errorf("netsec: declared max %d pages exceeds wasm32 limit %d", maxPages, MaxPages)
	}
	if initialPages > maxPages {
		return nil, fmt.Errorf("netsec: initial pages %d exceed declared max %d", initialPages, maxPages)
	}
	if !b.tryAdd(uint64(initialPages)) {
		return nil, ErrLimitExceeded
	}
	return &Instance{
		budget:   b,
		maxPages: maxPages,
		pages:    initialPages,
		mem:      make([]byte, uint64(initialPages)*PageSize),
	}, nil
}

// Grow attempts to grow memory by delta pages, mirroring memory.grow:
// it returns the previous size in pages, or -1 on failure. Checks run
// against the module-declared max and the tenant budget with
// overflow-safe arithmetic; on failure no usage statistics change.
// A delta of 0 is a probe and returns the current size.
func (i *Instance) Grow(delta uint32) int32 {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return -1
	}
	if delta == 0 {
		return int32(i.pages)
	}
	// Both operands are <= 2^32, so the uint64 sum cannot overflow.
	newPages := uint64(i.pages) + uint64(delta)
	if newPages > uint64(i.maxPages) {
		return -1
	}
	if !i.budget.tryAdd(uint64(delta)) {
		return -1
	}
	old := i.pages
	i.pages = uint32(newPages)
	// Reallocate and drop the old backing array: any view obtained
	// before this grow is stale and must be discarded by callers.
	newMem := make([]byte, newPages*PageSize)
	copy(newMem, i.mem)
	i.mem = newMem
	return int32(old)
}

// Pages returns the current memory size in pages.
func (i *Instance) Pages() uint32 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.pages
}

// View returns the current memory. The view is invalidated by Grow and
// Close; callers must re-validate and re-slice after any grow instead of
// retaining a stale view.
func (i *Instance) View() []byte {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.mem
}

// validateLocked checks ptr+len against the current memory size using
// uint64 arithmetic so a wrapping 32-bit sum cannot pass.
func (i *Instance) validateLocked(ptr, length uint32) error {
	if uint64(ptr)+uint64(length) > uint64(len(i.mem)) {
		return ErrOutOfBounds
	}
	return nil
}

// Read validates ptr+len against the current memory size and returns the
// guest bytes. It must be called after every grow so validation always
// uses the current size rather than a stale view.
func (i *Instance) Read(ptr, length uint32) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return nil, ErrOutOfBounds
	}
	if err := i.validateLocked(ptr, length); err != nil {
		return nil, err
	}
	end := uint64(ptr) + uint64(length)
	return i.mem[ptr:end], nil
}

// Write validates ptr+len(data) against the current memory size and
// copies data into guest memory.
func (i *Instance) Write(ptr uint32, data []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrOutOfBounds
	}
	if uint64(ptr)+uint64(len(data)) > uint64(len(i.mem)) {
		return ErrOutOfBounds
	}
	copy(i.mem[ptr:], data)
	return nil
}

// Close releases the instance's pages back to the tenant budget.
func (i *Instance) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return
	}
	i.closed = true
	i.budget.release(uint64(i.pages))
	i.mem = nil
}
