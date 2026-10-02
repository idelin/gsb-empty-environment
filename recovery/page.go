// Package recovery implements the classic ARIES crash-recovery protocol:
// Analysis (rebuild DirtyPageTable + TransactionTable), Redo (repeat
// history) and Undo (rollback losers via compensation log records).
package recovery

import (
	"sync"

	"aries/wal"
)

// Page is a fixed-size slotted byte array plus the LSN of the most recent
// log record applied to it (pageLSN), which drives Redo idempotence.
type Page struct {
	ID   wal.PageID
	LSN  wal.LSN
	Data []byte
}

// Disk is durable page storage.
type Disk interface {
	ReadPage(id wal.PageID) (*Page, bool)
	WritePage(p *Page)
}

// MemDisk is an in-memory Disk used for tests and the demo.
type MemDisk struct {
	mu     sync.RWMutex
	pages  map[wal.PageID]*Page
	writes int
}

func NewMemDisk() *MemDisk {
	return &MemDisk{pages: make(map[wal.PageID]*Page)}
}

func (d *MemDisk) ReadPage(id wal.PageID) (*Page, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.pages[id]
	if !ok {
		return nil, false
	}
	cp := &Page{ID: p.ID, LSN: p.LSN, Data: append([]byte(nil), p.Data...)}
	return cp, true
}

func (d *MemDisk) WritePage(p *Page) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pages[p.ID] = &Page{ID: p.ID, LSN: p.LSN, Data: append([]byte(nil), p.Data...)}
	d.writes++
}

// Writes reports how many pages were ever flushed to disk (test aid).
func (d *MemDisk) Writes() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.writes
}

// BufferPool caches pages. Dirty pages are only written back on explicit
// Flush calls, so dropping the pool without flushing simulates a crash.
type BufferPool struct {
	mu       sync.Mutex
	disk     Disk
	pageSize int
	pages    map[wal.PageID]*Page
	dirty    map[wal.PageID]bool
	recLSN   map[wal.PageID]wal.LSN
}

func NewBufferPool(disk Disk, pageSize int) *BufferPool {
	return &BufferPool{
		disk:     disk,
		pageSize: pageSize,
		pages:    make(map[wal.PageID]*Page),
		dirty:    make(map[wal.PageID]bool),
		recLSN:   make(map[wal.PageID]wal.LSN),
	}
}

// Fetch returns the cached page, loading it from disk if needed. Missing
// pages are materialized as zero-filled pages.
func (bp *BufferPool) Fetch(id wal.PageID) *Page {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if p, ok := bp.pages[id]; ok {
		return p
	}
	p, ok := bp.disk.ReadPage(id)
	if !ok {
		p = &Page{ID: id, Data: make([]byte, bp.pageSize)}
	}
	bp.pages[id] = p
	return p
}

// MarkDirty records that p was modified by the log record with the given LSN.
func (bp *BufferPool) MarkDirty(p *Page, lsn wal.LSN) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if !bp.dirty[p.ID] {
		bp.recLSN[p.ID] = lsn
	}
	p.LSN = lsn
	bp.dirty[p.ID] = true
}

// FlushPage writes one dirty page back to disk.
func (bp *BufferPool) FlushPage(id wal.PageID) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if p, ok := bp.pages[id]; ok && bp.dirty[id] {
		bp.disk.WritePage(p)
		delete(bp.dirty, id)
		delete(bp.recLSN, id)
	}
}

// FlushAll writes every dirty page back to disk.
func (bp *BufferPool) FlushAll() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	for id := range bp.dirty {
		bp.disk.WritePage(bp.pages[id])
	}
	bp.dirty = make(map[wal.PageID]bool)
	bp.recLSN = make(map[wal.PageID]wal.LSN)
}

// DirtyPages returns a snapshot of the dirty page set mapping each page to
// its recLSN: the oldest LSN that dirtied it since the last flush.
func (bp *BufferPool) DirtyPages() map[wal.PageID]wal.LSN {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	out := make(map[wal.PageID]wal.LSN, len(bp.dirty))
	for id := range bp.dirty {
		out[id] = bp.recLSN[id]
	}
	return out
}

// Crash discards all buffered (unflushed) state, simulating a power loss.
// Pages already written to Disk survive.
func (bp *BufferPool) Crash() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.pages = make(map[wal.PageID]*Page)
	bp.dirty = make(map[wal.PageID]bool)
	bp.recLSN = make(map[wal.PageID]wal.LSN)
}
