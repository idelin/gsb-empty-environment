package pagetable

import (
	"errors"
	"sync"
)

// memStore is an in-memory physical address space. Writes to distinct pages
// never disturb other pages, so old snapshots stay readable while a
// transaction writes freshly allocated frames.
type memStore struct {
	mu     sync.Mutex
	pages  map[uint64]*[PageSize]byte
	writes int
}

func newMemStore() *memStore {
	return &memStore{pages: map[uint64]*[PageSize]byte{}}
}

func (m *memStore) page(addr uint64) *[PageSize]byte {
	p, ok := m.pages[addr]
	if !ok {
		p = &[PageSize]byte{}
		m.pages[addr] = p
	}
	return p
}

func (m *memStore) ReadAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr := uint64(off)
	pg := m.page(addr &^ uint64(PageSize-1))
	offInPage := int(addr & uint64(PageSize-1))
	return copy(p, pg[offInPage:]), nil
}

func (m *memStore) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr := uint64(off)
	pg := m.page(addr &^ uint64(PageSize-1))
	offInPage := int(addr & uint64(PageSize-1))
	copy(pg[offInPage:], p)
	m.writes++
	return len(p), nil
}

// countingAllocator hands out zeroed frames and tracks live allocations.
type countingAllocator struct {
	base  uint64
	next  uint64
	live  map[uint64]struct{}
	total int
	mu    sync.Mutex
}

func newCountingAllocator(base uint64) *countingAllocator {
	return &countingAllocator{base: base, next: base, live: map[uint64]struct{}{}}
}

func (a *countingAllocator) AllocPage() (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	addr := a.next
	a.next += PageSize
	a.live[addr] = struct{}{}
	a.total++
	return addr, nil
}

func (a *countingAllocator) FreePage(addr uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.live, addr)
}

func (a *countingAllocator) liveCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.live)
}

var errInjectedAlloc = errors.New("injected allocation failure")

// failAtAllocator fails exactly one designated AllocPage call (0-based),
// delegating everything else to an inner allocator.
type failAtAllocator struct {
	inner  Allocator
	failAt int
	calls  int
}

func (f *failAtAllocator) AllocPage() (uint64, error) {
	if f.calls == f.failAt {
		f.calls++
		return 0, errInjectedAlloc
	}
	f.calls++
	return f.inner.AllocPage()
}

func (f *failAtAllocator) FreePage(addr uint64) { f.inner.FreePage(addr) }
