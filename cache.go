// Package cachemodel implements a page cache whose dirty state is tracked
// as version-tagged byte intervals. A writeback snapshots the page version
// and the dirty set, writes those bytes to the backing store, and on
// completion clears only the acked bytes whose dirty version is still the
// snapshotted one — bytes modified while the writeback was in flight, and
// bytes a short write never reached, stay dirty.
package cachemodel

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Store is the backing device. WriteAt may short-write: it returns the
// number of bytes actually persisted. Only those bytes are considered
// acked by the cache.
type Store interface {
	WriteAt(p []byte, off int64) (n int, err error)
	ReadAt(p []byte, off int64) (n int, err error)
}

// Cache is a set of pages backed by a Store.
type Cache struct {
	store    Store
	pageSize int

	// WriteHook, if set, is invoked while holding the page lock after a
	// write has been applied and versioned. It exists so tests can feed a
	// verification oracle with happens-before correctness.
	WriteHook func(pageID, off int, data []byte, ver uint64)

	mu    sync.Mutex
	pages map[int]*page

	// nextVer allocates globally unique write versions, so a reloaded page
	// never reuses versions from before its eviction.
	nextVer uint64
}

// New creates a cache of pageSize-byte pages on top of store.
func New(store Store, pageSize int) *Cache {
	if pageSize <= 0 {
		panic("cachemodel: pageSize must be positive")
	}
	return &Cache{store: store, pageSize: pageSize, pages: make(map[int]*page)}
}

type page struct {
	id   int
	c    *Cache
	mu   sync.Mutex
	cond sync.Cond

	data     []byte
	dirty    dirtySet
	version  uint64
	inflight bool // a writeback of this page is between snapshot and completion
	valid    bool // data has been loaded from the store
	evicted  bool
}

func newPage(c *Cache, id int) *page {
	p := &page{id: id, c: c, data: make([]byte, c.pageSize)}
	p.cond.L = &p.mu
	return p
}

func (c *Cache) getPage(id int) *page {
	for {
		c.mu.Lock()
		p, ok := c.pages[id]
		if !ok {
			p = newPage(c, id)
			c.pages[id] = p
			c.mu.Unlock()
			p.load()
			return p
		}
		c.mu.Unlock()

		p.mu.Lock()
		if !p.evicted {
			p.mu.Unlock()
			return p
		}
		p.mu.Unlock()
		// Lost a race with Evict; retry to fetch or create the fresh page.
	}
}

func (p *page) load() {
	buf := make([]byte, p.c.pageSize)
	n, _ := p.c.store.ReadAt(buf, int64(p.id)*int64(p.c.pageSize))
	p.mu.Lock()
	copy(p.data, buf[:n])
	p.valid = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *page) waitValidLocked() {
	for !p.valid {
		p.cond.Wait()
	}
}

// Write applies data at [off, off+len(data)) of page id, marks the range
// dirty at a fresh page version, and returns that version.
func (c *Cache) Write(id, off int, data []byte) (uint64, error) {
	if id < 0 || off < 0 || off+len(data) > c.pageSize {
		return 0, fmt.Errorf("cachemodel: write out of range (page %d, off %d, len %d)", id, off, len(data))
	}
	if len(data) == 0 {
		return 0, nil
	}
	p := c.getPage(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waitValidLocked()
	copy(p.data[off:], data)
	ver := atomic.AddUint64(&c.nextVer, 1)
	p.version = ver
	p.dirty.add(off, off+len(data), ver)
	if c.WriteHook != nil {
		c.WriteHook(id, off, data, ver)
	}
	return ver, nil
}

// Read copies page bytes into buf, loading the page from the store if needed.
func (c *Cache) Read(id, off int, buf []byte) error {
	if id < 0 || off < 0 || off+len(buf) > c.pageSize {
		return fmt.Errorf("cachemodel: read out of range (page %d, off %d, len %d)", id, off, len(buf))
	}
	p := c.getPage(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waitValidLocked()
	copy(buf, p.data[off:off+len(buf)])
	return nil
}

// writebackOnce performs at most one writeback pass over the page: it
// snapshots the version and dirty set, writes the snapshotted bytes to the
// store, then clears exactly the acked byte ranges whose dirty version is
// still <= the snapshot version.
func (c *Cache) writebackOnce(id int) error {
	p := c.getPage(id)

	p.mu.Lock()
	p.waitValidLocked()
	if p.inflight || p.dirty.empty() {
		p.mu.Unlock()
		return nil
	}
	ver := p.version
	snap := p.dirty.snapshot()
	type chunk struct {
		off  int
		data []byte
	}
	chunks := make([]chunk, 0, len(snap))
	for _, iv := range snap {
		buf := make([]byte, iv.end-iv.start)
		copy(buf, p.data[iv.start:iv.end])
		chunks = append(chunks, chunk{iv.start, buf})
	}
	p.inflight = true
	p.mu.Unlock()

	var acked []interval
	var firstErr error
	base := int64(id) * int64(c.pageSize)
	for _, ch := range chunks {
		n, err := c.store.WriteAt(ch.data, base+int64(ch.off))
		if n > len(ch.data) {
			n = len(ch.data)
		}
		if n > 0 {
			acked = append(acked, interval{start: ch.off, end: ch.off + n})
		}
		if err != nil {
			firstErr = err
			break
		}
		if n < len(ch.data) {
			// Short write without an error: stop conservatively; the
			// unwritten tail stays dirty.
			break
		}
	}

	p.mu.Lock()
	p.inflight = false
	p.dirty.clearAcked(acked, ver)
	p.cond.Broadcast()
	p.mu.Unlock()
	return firstErr
}

// Flush writes back until the page is clean, waiting for any writeback
// already in flight.
func (c *Cache) Flush(id int) error {
	p := c.getPage(id)
	for {
		p.mu.Lock()
		for p.inflight {
			p.cond.Wait()
		}
		if p.dirty.empty() {
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		if err := c.writebackOnce(id); err != nil {
			return err
		}
	}
}

// Evict removes the page from the cache. If a writeback is in flight it
// waits for it; if the page is (still) dirty it writes back first. A page
// re-dirtied while its writeback was in flight is therefore written back
// again before eviction completes.
func (c *Cache) Evict(id int) error {
	p := c.getPage(id)
	for {
		p.mu.Lock()
		for p.inflight || !p.valid {
			p.cond.Wait()
		}
		if p.dirty.empty() {
			p.evicted = true
			p.mu.Unlock()
			c.mu.Lock()
			if c.pages[id] == p {
				delete(c.pages, id)
			}
			c.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		if err := c.writebackOnce(id); err != nil {
			return err
		}
	}
}

// snapshotPage reports the page's data and dirty bitmap, or resident=false
// if the page is not currently cached. Used by tests and oracles.
func (c *Cache) snapshotPage(id int) (data []byte, dirty []bool, resident bool) {
	c.mu.Lock()
	p, ok := c.pages[id]
	c.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.evicted || !p.valid {
		return nil, nil, false
	}
	data = make([]byte, c.pageSize)
	copy(data, p.data)
	dirty = make([]bool, c.pageSize)
	for _, iv := range p.dirty.ivs {
		for i := iv.start; i < iv.end; i++ {
			dirty[i] = true
		}
	}
	return data, dirty, true
}
