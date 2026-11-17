package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// ErrNoFreePages reports that the page pool has no page available for
// allocation. Pages are unavailable while they are leased or while a mapping
// still holds a reference to them.
var ErrNoFreePages = errors.New("netsec: no free pages")

// PageID is a stable identifier for a page. IDs are never reused, even when the
// same backing page is returned to the pool and allocated again.
type PageID uint64

// Page is an opaque handle to a page owned by a PagePool.
//
// A Page must only be made available to a device through a Mapping. The mapping
// owns the reference until it releases the page, and the pool will not return
// the page to general allocation before that happens.
type Page struct {
	id   PageID
	pool *PagePool
}

// ID returns the page's stable identifier.
func (p *Page) ID() PageID {
	if p == nil {
		return 0
	}
	return p.id
}

// PagePool owns a bounded set of pages. Leased pages are not handed out again
// until the mapping that owns them releases them.
type PagePool struct {
	mu       sync.Mutex
	nextID   PageID
	free     []*Page
	leased   map[*Page]struct{}
	bound    map[*Page]struct{}
	capacity int
}

// NewPagePool creates a pool with pageCount initially free pages.
//
// A negative pageCount is a programming error and causes a panic.
func NewPagePool(pageCount int) *PagePool {
	if pageCount < 0 {
		panic(fmt.Sprintf("netsec: negative page count %d", pageCount))
	}

	p := &PagePool{
		leased:   make(map[*Page]struct{}),
		bound:    make(map[*Page]struct{}),
		capacity: pageCount,
	}
	for range pageCount {
		p.addPageLocked()
	}
	return p
}

func (p *PagePool) addPageLocked() *Page {
	p.nextID++
	page := &Page{id: p.nextID, pool: p}
	p.free = append(p.free, page)
	return page
}

// Grow adds pageCount free pages to the pool and returns their handles. The
// pages are not leased, so they remain available to Allocate.
func (p *PagePool) Grow(pageCount int) []*Page {
	if pageCount < 0 {
		panic(fmt.Sprintf("netsec: negative page count %d", pageCount))
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	added := make([]*Page, 0, pageCount)
	for range pageCount {
		added = append(added, p.addPageLocked())
	}
	p.capacity += pageCount
	return added
}

// Allocate leases a free page from the pool. The caller must eventually pass
// the page to NewMapping or otherwise return it through the mapping lifetime
// component.
func (p *PagePool) Allocate() (*Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.free) == 0 {
		return nil, ErrNoFreePages
	}
	last := len(p.free) - 1
	page := p.free[last]
	p.free[last] = nil
	p.free = p.free[:last]
	p.leased[page] = struct{}{}
	return page, nil
}

// FreeCount returns the number of pages currently available for allocation.
func (p *PagePool) FreeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free)
}

// LeasedCount returns the number of pages currently outside the free pool,
// including pages bound to mappings.
func (p *PagePool) LeasedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.leased)
}

// Capacity returns the total number of pages owned by the pool.
func (p *PagePool) Capacity() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.capacity
}

func (p *PagePool) bind(pages []*Page) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	seen := make(map[*Page]struct{}, len(pages))
	for _, page := range pages {
		if page == nil {
			return errors.New("netsec: cannot bind a nil page")
		}
		if page.pool != p {
			return errors.New("netsec: page belongs to a different pool")
		}
		if _, ok := p.leased[page]; !ok {
			return errors.New("netsec: page is not leased from the pool")
		}
		if _, ok := p.bound[page]; ok {
			return errors.New("netsec: page is already bound to a mapping")
		}
		if _, ok := seen[page]; ok {
			return errors.New("netsec: duplicate page in mapping")
		}
		seen[page] = struct{}{}
	}

	for _, page := range pages {
		p.bound[page] = struct{}{}
	}
	return nil
}

func (p *PagePool) release(pages []*Page) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, page := range pages {
		if page == nil || page.pool != p {
			continue
		}
		if _, ok := p.bound[page]; !ok {
			continue
		}
		delete(p.bound, page)
		delete(p.leased, page)
		p.free = append(p.free, page)
	}
}
