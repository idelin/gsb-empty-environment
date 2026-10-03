package aries

import "sync"

// Page is a slotted data page simplified to a byte payload plus pageLSN.
type Page struct {
	ID      PageID
	Data    []byte
	PageLSN LSN
	Dirty   bool
}

func (p *Page) clone() *Page {
	return &Page{
		ID:      p.ID,
		Data:    append([]byte(nil), p.Data...),
		PageLSN: p.PageLSN,
		Dirty:   p.Dirty,
	}
}

// Disk is durable storage.
type Disk struct {
	mu    sync.Mutex
	pages map[PageID]*Page
}

// NewDisk creates an empty disk.
func NewDisk() *Disk { return &Disk{pages: make(map[PageID]*Page)} }

// ReadPage returns a copy of the persisted page, or a zero page.
func (d *Disk) ReadPage(id PageID) *Page {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.pages[id]; ok {
		return p.clone()
	}
	return &Page{ID: id, Data: []byte{}, PageLSN: 0}
}

// WritePage persists a page.
func (d *Disk) WritePage(p *Page) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := p.clone()
	c.Dirty = false
	d.pages[p.ID] = c
}

// BufferPool caches pages in memory; dirty pages are lost on crash.
type BufferPool struct {
	mu      sync.Mutex
	pages   map[PageID]*Page
	disk    *Disk
	onFlush func(id PageID)
}

// NewBufferPool creates a buffer pool backed by disk.
func NewBufferPool(disk *Disk) *BufferPool {
	return &BufferPool{pages: make(map[PageID]*Page), disk: disk}
}

// Fetch returns the in-memory copy of a page, loading it from disk if needed.
func (bp *BufferPool) Fetch(id PageID) *Page {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if p, ok := bp.pages[id]; ok {
		return p
	}
	p := bp.disk.ReadPage(id)
	bp.pages[id] = p
	return p
}

// FlushPage writes a dirty page back to disk.
func (bp *BufferPool) FlushPage(id PageID) {
	bp.mu.Lock()
	p, ok := bp.pages[id]
	if ok && p.Dirty {
		bp.disk.WritePage(p)
		p.Dirty = false
	}
	bp.mu.Unlock()
	if ok && bp.onFlush != nil {
		bp.onFlush(id)
	}
}

// FlushAll writes back every dirty page.
func (bp *BufferPool) FlushAll() {
	bp.mu.Lock()
	ids := make([]PageID, 0, len(bp.pages))
	for id := range bp.pages {
		ids = append(ids, id)
	}
	bp.mu.Unlock()
	for _, id := range ids {
		bp.FlushPage(id)
	}
}
