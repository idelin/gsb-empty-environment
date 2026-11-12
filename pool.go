package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// PageSize is the simulated page granularity in bytes.
const PageSize = 4096

// ErrUnknownPage is returned when a page ID is not owned by the pool.
var ErrUnknownPage = errors.New("netsec: unknown page")

// Pool is a simulated normal page allocator. A page that still carries DMA
// pins is never handed out again; only once the last pin drops does the page
// get scrubbed and returned to the free list.
type Pool struct {
	mu     sync.Mutex
	nextID PageID
	pages  map[PageID]*page
}

type page struct {
	data []byte
	pins int
	free bool
}

// NewPool creates an allocator pre-populated with size free pages.
func NewPool(size int) *Pool {
	p := &Pool{pages: make(map[PageID]*page)}
	for i := 0; i < size; i++ {
		id := p.nextID
		p.nextID++
		p.pages[id] = &page{free: true, data: make([]byte, PageSize)}
	}
	return p
}

// Alloc returns a free, unpinned page, growing the pool when every existing
// page is either allocated or pinned by in-flight DMA.
func (p *Pool) Alloc() PageID {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, pg := range p.pages {
		if pg.free && pg.pins == 0 {
			pg.free = false
			return id
		}
	}
	id := p.nextID
	p.nextID++
	p.pages[id] = &page{data: make([]byte, PageSize)}
	return id
}

// pin takes a DMA reference on a page, removing it from the free list.
func (p *Pool) pin(id PageID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, ok := p.pages[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownPage, id)
	}
	pg.pins++
	pg.free = false
	return nil
}

// unpin drops a DMA reference. When the last pin drops the page contents are
// scrubbed before the page returns to the normal allocation pool.
func (p *Pool) unpin(id PageID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, ok := p.pages[id]
	if !ok || pg.pins <= 0 {
		panic(fmt.Sprintf("netsec: unbalanced unpin of page %d", id))
	}
	pg.pins--
	if pg.pins == 0 && !pg.free {
		clear(pg.data)
		pg.free = true
	}
}

// Write copies data into a page, simulating a CPU store to mapped memory.
func (p *Pool) Write(id PageID, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, ok := p.pages[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownPage, id)
	}
	copy(pg.data, data)
	return nil
}

// Read returns a copy of the page contents.
func (p *Pool) Read(id PageID) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, ok := p.pages[id]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownPage, id)
	}
	out := make([]byte, len(pg.data))
	copy(out, pg.data)
	return out, nil
}

// Pins reports the current DMA pin count of a page.
func (p *Pool) Pins(id PageID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pages[id].pins
}

// IsFree reports whether a page currently sits in the free list.
func (p *Pool) IsFree(id PageID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pages[id].free
}
