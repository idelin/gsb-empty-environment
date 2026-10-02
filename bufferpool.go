package bptree

import (
	"container/list"
	"errors"
	"fmt"
)

// PageID identifies a page on "disk".
type PageID int64

// InvalidPageID marks the end of the leaf chain.
const InvalidPageID PageID = -1

// ErrPoolExhausted is returned when every frame is pinned and no page can be
// evicted. It guards the pool against memory overflow caused by pin leaks.
var ErrPoolExhausted = errors.New("bufferpool: all frames pinned, cannot evict")

// Page is a slotted page holding one B+ tree node.
type Page struct {
	ID    PageID
	node  *node
	dirty bool
}

// frame is a buffer-pool slot caching one page.
type frame struct {
	page *Page
	pins int
	elem *list.Element // position in the LRU list, valid only when pins == 0
}

// BufferPool is a fixed-size page cache with pin/unpin semantics.
// Pages live on the simulated disk map; the pool only caches up to
// `capacity` frames and evicts unpinned pages in LRU order.
type BufferPool struct {
	capacity int
	disk     map[PageID]*Page
	resident map[PageID]*frame
	lru      *list.List // PageIDs of unpinned frames, front = coldest
	nextID   PageID

	Hits      int
	Misses    int
	Evictions int
}

func NewBufferPool(capacity int) *BufferPool {
	if capacity < 2 {
		panic("bufferpool: capacity must be >= 2")
	}
	return &BufferPool{
		capacity: capacity,
		disk:     make(map[PageID]*Page),
		resident: make(map[PageID]*frame),
		lru:      list.New(),
	}
}

// NewPage allocates a fresh page on disk and returns it pinned.
func (p *BufferPool) NewPage() (*Page, error) {
	p.nextID++
	pg := &Page{ID: p.nextID}
	p.disk[pg.ID] = pg
	if err := p.pin(pg); err != nil {
		return nil, err
	}
	return pg, nil
}

// Fetch returns the page pinned. Callers must Unpin it exactly once.
func (p *BufferPool) Fetch(id PageID) (*Page, error) {
	pg, ok := p.disk[id]
	if !ok {
		return nil, fmt.Errorf("bufferpool: page %d does not exist", id)
	}
	if err := p.pin(pg); err != nil {
		return nil, err
	}
	return pg, nil
}

func (p *BufferPool) pin(pg *Page) error {
	if f, ok := p.resident[pg.ID]; ok {
		p.Hits++
		if f.pins == 0 {
			p.lru.Remove(f.elem)
			f.elem = nil
		}
		f.pins++
		return nil
	}
	p.Misses++
	if len(p.resident) >= p.capacity {
		if err := p.evict(); err != nil {
			return err
		}
	}
	p.resident[pg.ID] = &frame{page: pg, pins: 1}
	return nil
}

// Unpin releases one pin. When the count drops to zero the frame becomes
// evictable. Unpinning a page that is not pinned is a bug and panics.
func (p *BufferPool) Unpin(id PageID) {
	f, ok := p.resident[id]
	if !ok || f.pins == 0 {
		panic(fmt.Sprintf("bufferpool: unpin of unpinned page %d", id))
	}
	f.pins--
	if f.pins == 0 {
		f.elem = p.lru.PushBack(id)
	}
}

func (p *BufferPool) evict() error {
	front := p.lru.Front()
	if front == nil {
		return ErrPoolExhausted
	}
	id := front.Value.(PageID)
	p.lru.Remove(front)
	delete(p.resident, id)
	p.Evictions++
	return nil
}

// TotalPins sums the pin counts of all resident frames. After a well
// behaved workload it must return to zero; anything else is a pin leak.
func (p *BufferPool) TotalPins() int {
	total := 0
	for _, f := range p.resident {
		total += f.pins
	}
	return total
}
