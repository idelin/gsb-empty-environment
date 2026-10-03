// Package storage provides the page layer: a disk store and a buffer
// pool tracking PageLSN / RecLSN as required by ARIES.
package storage

import (
	"sort"
	"sync"
)

// PageImage is the durable on-disk state of a page.
type PageImage struct {
	Value   int64
	PageLSN uint64
}

// Disk is stable page storage.
type Disk interface {
	ReadPage(pageID uint64) (PageImage, bool)
	WritePage(pageID uint64, img PageImage)
	Snapshot() map[uint64]PageImage
}

// MemDisk is an in-memory Disk.
type MemDisk struct {
	mu    sync.Mutex
	pages map[uint64]PageImage
}

func NewMemDisk() *MemDisk { return &MemDisk{pages: make(map[uint64]PageImage)} }

func (d *MemDisk) ReadPage(pageID uint64) (PageImage, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	img, ok := d.pages[pageID]
	return img, ok
}

func (d *MemDisk) WritePage(pageID uint64, img PageImage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pages[pageID] = img
}

func (d *MemDisk) Snapshot() map[uint64]PageImage {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[uint64]PageImage, len(d.pages))
	for k, v := range d.pages {
		out[k] = v
	}
	return out
}

// Page is a buffered page frame.
type Page struct {
	ID      uint64
	Value   int64
	PageLSN uint64 // LSN of the most recent log record applied to this page
	Dirty   bool
	RecLSN  uint64 // LSN of the first log record that dirtied the page since last flush
}

// DirtyEntry mirrors wal.DirtyEntry without creating an import cycle.
type DirtyEntry struct {
	PageID uint64
	RecLSN uint64
}

// BufferPool caches pages. All methods are safe for concurrent use;
// callers that mutate a fetched Page must serialize externally (the db
// layer does).
type BufferPool struct {
	mu    sync.Mutex
	disk  Disk
	pages map[uint64]*Page
}

func NewBufferPool(disk Disk) *BufferPool {
	return &BufferPool{disk: disk, pages: make(map[uint64]*Page)}
}

// Fetch returns the buffered page, loading it from disk if necessary.
// Missing pages start zero-valued with PageLSN 0.
func (p *BufferPool) Fetch(id uint64) *Page {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg, ok := p.pages[id]; ok {
		return pg
	}
	pg := &Page{ID: id}
	if img, ok := p.disk.ReadPage(id); ok {
		pg.Value = img.Value
		pg.PageLSN = img.PageLSN
	}
	p.pages[id] = pg
	return pg
}

// Flush writes a page to disk if dirty and clears its dirty state.
func (p *BufferPool) Flush(id uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg, ok := p.pages[id]; ok && pg.Dirty {
		p.disk.WritePage(id, PageImage{Value: pg.Value, PageLSN: pg.PageLSN})
		pg.Dirty = false
		pg.RecLSN = 0
	}
}

// FlushAll flushes every dirty page.
func (p *BufferPool) FlushAll() {
	p.mu.Lock()
	ids := make([]uint64, 0, len(p.pages))
	for id := range p.pages {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	for _, id := range ids {
		p.Flush(id)
	}
}

// DirtyPages returns the dirty page table contents, sorted by page ID
// for determinism.
func (p *BufferPool) DirtyPages() []DirtyEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []DirtyEntry
	for _, pg := range p.pages {
		if pg.Dirty {
			out = append(out, DirtyEntry{PageID: pg.ID, RecLSN: pg.RecLSN})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PageID < out[j].PageID })
	return out
}

// CachedIDs returns the IDs of pages currently in the pool.
func (p *BufferPool) CachedIDs() []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]uint64, 0, len(p.pages))
	for id := range p.pages {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Reset drops all buffered pages without flushing. Models a crash.
func (p *BufferPool) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pages = make(map[uint64]*Page)
}
