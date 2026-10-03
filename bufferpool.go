package bptree

import (
	"container/list"
	"errors"
	"fmt"
)

// PageID identifies a page on the (simulated) disk.
type PageID int64

// InvalidPageID marks the absence of a page link.
const InvalidPageID PageID = -1

// Page is a buffer-pool frame holding a B+ tree node.
type Page struct {
	id    PageID
	node  *node
	pins  int
	dirty bool
}

// ID returns the page identifier.
func (p *Page) ID() PageID { return p.id }

type frame struct {
	page *Page
	elem *list.Element
}

// BufferPool is a fixed-capacity pool of page frames with LRU eviction.
// A page may only be evicted while its pin count is zero; FetchPage
// returns an error instead of growing the pool past its capacity, so a
// page leak (a forgotten Unpin) surfaces immediately as an error.
type BufferPool struct {
	capacity int
	disk     map[PageID]*node // simulated durable storage
	frames   map[PageID]*frame
	lru      *list.List // PageIDs, front = most recently used
	nextID   PageID

	Hits      int
	Misses    int
	Evictions int
}

// NewBufferPool creates a pool holding at most capacity pages.
func NewBufferPool(capacity int) *BufferPool {
	if capacity < 2 {
		panic("buffer pool capacity must be >= 2")
	}
	return &BufferPool{
		capacity: capacity,
		disk:     make(map[PageID]*node),
		frames:   make(map[PageID]*frame),
		lru:      list.New(),
	}
}

// NewPage allocates a fresh page for n and returns it pinned.
func (bp *BufferPool) NewPage(n *node) (*Page, error) {
	id := bp.nextID
	bp.nextID++
	bp.disk[id] = n
	return bp.fetch(id)
}

// FetchPage pins the page and returns it. The caller must later call
// UnpinPage exactly once for every successful fetch.
func (bp *BufferPool) FetchPage(id PageID) (*Page, error) {
	if _, ok := bp.disk[id]; !ok {
		return nil, fmt.Errorf("fetch: page %d does not exist", id)
	}
	return bp.fetch(id)
}

func (bp *BufferPool) fetch(id PageID) (*Page, error) {
	if f, ok := bp.frames[id]; ok {
		f.page.pins++
		bp.lru.MoveToFront(f.elem)
		bp.Hits++
		return f.page, nil
	}
	bp.Misses++
	if len(bp.frames) >= bp.capacity {
		if err := bp.evict(); err != nil {
			return nil, err
		}
	}
	p := &Page{id: id, node: bp.disk[id], pins: 1}
	bp.frames[id] = &frame{page: p, elem: bp.lru.PushFront(id)}
	return p, nil
}

// evict removes the least recently used unpinned page.
func (bp *BufferPool) evict() error {
	for e := bp.lru.Back(); e != nil; e = e.Prev() {
		id := e.Value.(PageID)
		if bp.frames[id].page.pins == 0 {
			delete(bp.frames, id)
			bp.lru.Remove(e)
			bp.Evictions++
			return nil
		}
	}
	return errors.New("buffer pool exhausted: every page is pinned (page leak?)")
}

// UnpinPage releases one pin on the page. dirty marks the page modified.
func (bp *BufferPool) UnpinPage(id PageID, dirty bool) error {
	f, ok := bp.frames[id]
	if !ok {
		return fmt.Errorf("unpin: page %d is not in the pool", id)
	}
	if f.page.pins <= 0 {
		return fmt.Errorf("unpin: page %d has pin count 0", id)
	}
	f.page.pins--
	if dirty {
		f.page.dirty = true
	}
	bp.lru.MoveToFront(f.elem)
	return nil
}

// PinnedCount returns the total number of outstanding pins. It must be
// zero whenever no operation is in flight; anything else is a leak.
func (bp *BufferPool) PinnedCount() int {
	total := 0
	for _, f := range bp.frames {
		total += f.page.pins
	}
	return total
}

// PagesOnDisk reports how many pages the tree occupies.
func (bp *BufferPool) PagesOnDisk() int { return len(bp.disk) }
