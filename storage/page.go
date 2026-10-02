// Package storage models the page layer of a database: a durable page
// store ("disk") plus a volatile buffer pool. Crash() drops the buffer
// pool, which is exactly what a sudden power loss does to dirty pages.
package storage

import "sync"

// PageSize is the fixed size of a page in bytes.
const PageSize = 4096

// Page is a single slotted-less data page. LSN is the pageLSN: the LSN of
// the most recent log record whose update is reflected in Data.
type Page struct {
	ID   uint32
	LSN  uint64
	Data []byte
}

func clonePage(p *Page) *Page {
	q := &Page{ID: p.ID, LSN: p.LSN, Data: make([]byte, len(p.Data))}
	copy(q.Data, p.Data)
	return q
}

// PageStore is a thread-safe page store with a volatile buffer pool.
// persisted models disk, dirty models the in-memory buffer pool.
type PageStore struct {
	mu        sync.RWMutex
	persisted map[uint32]*Page
	dirty     map[uint32]*Page
}

// NewPageStore returns an empty store.
func NewPageStore() *PageStore {
	return &PageStore{
		persisted: make(map[uint32]*Page),
		dirty:     make(map[uint32]*Page),
	}
}

// Fetch returns a private copy of the page: the dirty version if present,
// else the persisted version, else a fresh zero page.
func (ps *PageStore) Fetch(id uint32) *Page {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if p, ok := ps.dirty[id]; ok {
		return clonePage(p)
	}
	if p, ok := ps.persisted[id]; ok {
		return clonePage(p)
	}
	return &Page{ID: id, Data: make([]byte, PageSize)}
}

// UnpinDirty installs the page into the buffer pool as dirty.
func (ps *PageStore) UnpinDirty(p *Page) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.dirty[p.ID] = clonePage(p)
}

// PageLSN returns the pageLSN of the most recent in-memory image.
func (ps *PageStore) PageLSN(id uint32) uint64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if p, ok := ps.dirty[id]; ok {
		return p.LSN
	}
	if p, ok := ps.persisted[id]; ok {
		return p.LSN
	}
	return 0
}

// DirtyPages snapshots the buffer pool as pageID -> pageLSN (used to build
// checkpoint records).
func (ps *PageStore) DirtyPages() map[uint32]uint64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	out := make(map[uint32]uint64, len(ps.dirty))
	for id, p := range ps.dirty {
		out[id] = p.LSN
	}
	return out
}

// FlushAll forces every dirty page to disk and empties the buffer pool.
func (ps *PageStore) FlushAll() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for id, p := range ps.dirty {
		ps.persisted[id] = clonePage(p)
	}
	ps.dirty = make(map[uint32]*Page)
}

// Crash simulates a sudden power loss: the volatile buffer pool is gone,
// only what was flushed to "disk" survives.
func (ps *PageStore) Crash() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.dirty = make(map[uint32]*Page)
}
