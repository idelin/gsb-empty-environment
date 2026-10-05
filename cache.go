package writeback

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by cache operations after Close.
var ErrClosed = errors.New("cache: closed")

// pageState is the lifecycle state of a resident page.
type pageState int

const (
	stateClean   pageState = iota // in-memory image matches the durable image
	stateDirty                    // modified, not yet being written back
	stateWriting                  // a writeback of an older snapshot is in flight
)

func (s pageState) String() string {
	switch s {
	case stateClean:
		return "clean"
	case stateDirty:
		return "dirty"
	case stateWriting:
		return "writing"
	}
	return "unknown"
}

// Page is a resident cache page.
//
// version is the latest in-memory version; cleanVersion is the newest
// version confirmed durable by the disk. A page is fully clean only when
// version == cleanVersion. While a writeback is in flight, writingVer
// records which snapshot version it carries: the completion of that
// writeback confirms exactly that version, never a newer one.
type Page struct {
	id           uint64
	data         []byte
	version      uint64
	cleanVersion uint64
	writingVer   uint64
	state        pageState
}

// Cache is a write-back page cache over a SimDisk.
//
// Dirty pages are never dropped: eviction only considers clean pages. When
// the cache is full of unflushable pages, loaders block (backpressure) until
// a writeback completes and frees a clean victim, or until their context is
// cancelled.
type Cache struct {
	mu       sync.Mutex
	cond     *sync.Cond
	disk     *SimDisk
	capacity int
	pages    map[uint64]*Page
	closed   bool
	wg       sync.WaitGroup
}

func NewCache(disk *SimDisk, capacity int) *Cache {
	c := &Cache{
		disk:     disk,
		capacity: capacity,
		pages:    make(map[uint64]*Page),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Write loads the page (blocking under backpressure) and modifies it.
func (c *Cache) Write(ctx context.Context, id uint64, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.loadLocked(ctx, id)
	if err != nil {
		return err
	}
	p.data = append([]byte(nil), data...)
	p.version++
	if p.state == stateClean {
		p.state = stateDirty
	}
	// If a writeback is in flight it keeps its snapshot; the new version
	// will be written back by a later flush.
	return nil
}

// Read loads the page (blocking under backpressure) and returns its data.
func (c *Cache) Read(ctx context.Context, id uint64) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.loadLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), p.data...), nil
}

// loadLocked returns the resident page, loading it from disk if needed.
// It blocks while the cache is full and no clean victim exists.
func (c *Cache) loadLocked(ctx context.Context, id uint64) (*Page, error) {
	if c.closed {
		return nil, ErrClosed
	}
	if p, ok := c.pages[id]; ok {
		return p, nil
	}
	// Wake the waiter when the context is cancelled. A goroutine is used
	// (not context.AfterFunc) because the caller still holds c.mu when
	// loadLocked returns, and AfterFunc's stop would deadlock waiting for
	// a callback that is itself blocked on c.mu.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		case <-done:
		}
	}()
	for len(c.pages) >= c.capacity {
		if c.closed {
			return nil, ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.evictOneCleanLocked() {
			continue
		}
		// No evictable page: kick writebacks so a victim appears, then wait.
		c.kickWritebacksLocked()
		c.cond.Wait()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ver, _ := c.disk.Read(id)
	p := &Page{
		id:           id,
		data:         data,
		version:      ver,
		cleanVersion: ver,
		state:        stateClean,
	}
	c.pages[id] = p
	return p, nil
}

// evictOneCleanLocked drops one clean page. Dirty and writing pages are
// never candidates: their data exists nowhere else.
func (c *Cache) evictOneCleanLocked() bool {
	for id, p := range c.pages {
		if p.state == stateClean {
			delete(c.pages, id)
			return true
		}
	}
	return false
}

// kickWritebacksLocked starts a writeback for every dirty page.
func (c *Cache) kickWritebacksLocked() {
	for _, p := range c.pages {
		c.startWritebackLocked(p)
	}
}

// Flush starts a writeback for the page if it is dirty. It is asynchronous:
// completion is reconciled when the disk delivers the result.
func (c *Cache) Flush(id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if p, ok := c.pages[id]; ok {
		c.startWritebackLocked(p)
	}
	return nil
}

// FlushAll starts a writeback for every dirty resident page.
func (c *Cache) FlushAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.kickWritebacksLocked()
	return nil
}

// startWritebackLocked snapshots a dirty page and submits it to the disk.
// At most one writeback per page is in flight at any time.
func (c *Cache) startWritebackLocked(p *Page) {
	if p.state != stateDirty {
		return
	}
	ver := p.version
	data := append([]byte(nil), p.data...)
	p.state = stateWriting
	p.writingVer = ver
	done := c.disk.Submit(p.id, ver, data)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		err := <-done
		c.onAck(p.id, ver, err)
	}()
}

// onAck reconciles a writeback completion for one specific version.
//
// The ack confirms exactly version ver. cleanVersion only moves forward, so
// a late ack for an old version can never regress it; the page state only
// changes if the ack matches the in-flight writeback, so a stale ack can
// never mark newer modifications clean.
func (c *Cache) onAck(id, ver uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pages[id]
	if !ok {
		return
	}
	if err != nil {
		// Writeback failed: the snapshot was not confirmed. If the page
		// has unconfirmed modifications it goes back to dirty so a later
		// flush retries.
		if p.state == stateWriting && p.writingVer == ver && p.version > p.cleanVersion {
			p.state = stateDirty
		}
		c.cond.Broadcast()
		return
	}
	if ver > p.cleanVersion {
		p.cleanVersion = ver
	}
	if p.state == stateWriting && p.writingVer == ver {
		if p.version == ver {
			p.state = stateClean
		} else {
			// Modified while the writeback was in flight: the confirmed
			// snapshot is stale, the page is dirty again.
			p.state = stateDirty
		}
	}
	c.cond.Broadcast()
}

// Checkpoint flushes all dirty pages and returns once every write submitted
// before this call is durable.
func (c *Cache) Checkpoint(ctx context.Context) error {
	if err := c.FlushAll(); err != nil {
		return err
	}
	ch := c.disk.Barrier()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the cache. In-flight writebacks are completed by closing the
// disk (a graceful shutdown flush) and their acks are reconciled before
// Close returns. Dirty data that was never flushed is left unpersisted; the
// durable image of every page remains its latest confirmed version.
func (c *Cache) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	c.disk.Close()
	c.wg.Wait()
	return nil
}

// Snapshot returns a copy of a resident page, for inspection and tests.
func (c *Cache) Snapshot(id uint64) (Page, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pages[id]
	if !ok {
		return Page{}, false
	}
	cp := *p
	cp.data = append([]byte(nil), p.data...)
	return cp, true
}

// Resident reports how many pages are currently cached.
func (c *Cache) Resident() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pages)
}
