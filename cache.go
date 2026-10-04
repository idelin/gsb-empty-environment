// Package writebacksim simulates a versioned dirty-page writeback cache.
//
// A page is Clean when its latest in-memory version is confirmed durable,
// Dirty when it carries unconfirmed modifications, and in Writeback while a
// snapshot of one of its versions is being persisted. Pages may be modified
// while a writeback is in flight; a completion receipt acknowledges only the
// exact version it carries and never cleans newer modifications. Dirty pages
// are never dropped: when the cache is full and no clean page can be
// evicted, allocation blocks (backpressure) until a writeback completes or
// the caller's context is canceled.
package writebacksim

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrClosed is returned by operations on a closed cache.
	ErrClosed = errors.New("writebacksim: cache is closed")
	// ErrEvicted is returned when using a page handle that is no longer
	// resident in the cache.
	ErrEvicted = errors.New("writebacksim: page is no longer resident")
	// ErrNoCleanPage is returned by TryAlloc when the cache is full and
	// every resident page is dirty or in writeback.
	ErrNoCleanPage = errors.New("writebacksim: capacity full, no clean page to evict")
)

// PageState is the visibility state of a cached page.
type PageState int

const (
	// StateClean means the latest version is confirmed durable.
	StateClean PageState = iota
	// StateDirty means the page carries unconfirmed modifications.
	StateDirty
	// StateWriteback means a snapshot of the page is being persisted.
	StateWriteback
)

func (s PageState) String() string {
	switch s {
	case StateClean:
		return "clean"
	case StateDirty:
		return "dirty"
	case StateWriteback:
		return "writeback"
	}
	return "unknown"
}

// Page is a handle to a cached page. Obtain it from Alloc or TryAlloc and
// mutate it only through the cache's Write method.
type Page struct {
	id        uint64
	data      []byte
	version   uint64 // latest in-memory version
	acked     uint64 // latest version confirmed durable by the disk
	inflight  bool   // a writeback of wbVersion is in flight
	wbVersion uint64 // version currently being written back
}

// ID returns the page identifier.
func (p *Page) ID() uint64 { return p.id }

// stateLocked derives the state from version bookkeeping; it must be called
// with the cache lock held.
func (p *Page) stateLocked() PageState {
	switch {
	case p.inflight:
		return StateWriteback
	case p.version > p.acked:
		return StateDirty
	default:
		return StateClean
	}
}

// cleanLocked reports whether the page can be evicted: nothing in flight
// and nothing unconfirmed.
func (p *Page) cleanLocked() bool {
	return !p.inflight && p.version == p.acked
}

// Cache is a capacity-bounded page cache with versioned writeback.
type Cache struct {
	mu           sync.Mutex
	pages        map[uint64]*Page
	capacity     int
	disk         *VersionedDisk
	compCh       chan Completion
	wake         chan struct{}
	notify       chan struct{}
	done         chan struct{}
	closed       bool
	aborted      bool
	retryBackoff time.Duration
}

// NewCache creates a cache holding at most capacity pages, backed by a
// fresh VersionedDisk reachable via Disk.
func NewCache(capacity int) *Cache {
	if capacity < 1 {
		capacity = 1
	}
	c := &Cache{
		pages:        make(map[uint64]*Page),
		capacity:     capacity,
		disk:         NewVersionedDisk(),
		compCh:       make(chan Completion, 4096),
		wake:         make(chan struct{}, 1),
		notify:       make(chan struct{}),
		done:         make(chan struct{}),
		retryBackoff: 10 * time.Millisecond,
	}
	c.disk.deliver = func(comp Completion) { c.compCh <- comp }
	go c.loop()
	return c
}

// Disk returns the simulated disk backing this cache.
func (c *Cache) Disk() *VersionedDisk { return c.disk }

// Alloc returns the page for id, creating and evicting as needed. Only
// clean pages are evictable; if the cache is full of dirty or in-flight
// pages, Alloc blocks until a writeback completes or ctx is canceled.
func (c *Cache) Alloc(ctx context.Context, id uint64) (*Page, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrClosed
		}
		if p, ok := c.pages[id]; ok {
			c.mu.Unlock()
			return p, nil
		}
		if len(c.pages) >= c.capacity && !c.evictOneLocked() {
			notify := c.notify
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-notify:
			}
			continue
		}
		p := &Page{id: id}
		c.pages[id] = p
		c.mu.Unlock()
		return p, nil
	}
}

// TryAlloc is the non-blocking variant of Alloc; it fails with
// ErrNoCleanPage instead of applying backpressure.
func (c *Cache) TryAlloc(id uint64) (*Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if p, ok := c.pages[id]; ok {
		return p, nil
	}
	if len(c.pages) >= c.capacity && !c.evictOneLocked() {
		return nil, ErrNoCleanPage
	}
	p := &Page{id: id}
	c.pages[id] = p
	return p, nil
}

// evictOneLocked drops one clean page; dirty and in-flight pages are never
// dropped. It reports whether a page was evicted.
func (c *Cache) evictOneLocked() bool {
	for pid, p := range c.pages {
		if p.cleanLocked() {
			delete(c.pages, pid)
			return true
		}
	}
	return false
}

// Write modifies a resident page, bumping its version. It is allowed while
// a writeback of an older version is in flight; the page is then re-dirtied
// and will be written back again once the current round completes.
func (c *Cache) Write(p *Page, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if cur, ok := c.pages[p.id]; !ok || cur != p {
		return ErrEvicted
	}
	p.data = append([]byte(nil), data...)
	p.version++
	c.kick()
	return nil
}

// Read returns a copy of the page's current in-memory data.
func (c *Cache) Read(p *Page) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.pages[p.id]; !ok || cur != p {
		return nil, ErrEvicted
	}
	return append([]byte(nil), p.data...), nil
}

// State returns the current state of a resident page.
func (c *Cache) State(id uint64) (PageState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pages[id]
	if !ok {
		return StateClean, false
	}
	return p.stateLocked(), true
}

// Versions returns the latest in-memory version and the latest version
// confirmed durable for a resident page.
func (c *Cache) Versions(id uint64) (version, acked uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pages[id]
	if !ok {
		return 0, 0, false
	}
	return p.version, p.acked, true
}

// Close stops accepting new work and flushes every dirty page, waiting for
// the corresponding receipts. It returns nil once every resident page's
// latest version is confirmed durable. If ctx expires first, the flusher is
// aborted and ctx's error is returned; unconfirmed pages stay dirty and are
// never reported clean.
func (c *Cache) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		done := c.done
		c.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.closed = true
	c.broadcastLocked()
	c.mu.Unlock()
	c.kick()

	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		c.aborted = true
		c.mu.Unlock()
		c.kick()
		<-c.done
		return ctx.Err()
	}
}

// loop is the writeback scheduler: it starts writebacks for dirty pages
// (at most one in flight per page) and applies completion receipts.
func (c *Cache) loop() {
	defer close(c.done)
	for {
		c.mu.Lock()
		if c.aborted || (c.closed && !c.hasUnflushedLocked()) {
			c.mu.Unlock()
			return
		}
		var reqs []WriteRequest
		for _, p := range c.pages {
			if p.version > p.acked && !p.inflight {
				p.inflight = true
				p.wbVersion = p.version
				reqs = append(reqs, WriteRequest{
					PageID:  p.id,
					Version: p.version,
					Data:    append([]byte(nil), p.data...),
				})
			}
		}
		c.mu.Unlock()

		for _, req := range reqs {
			c.disk.Write(req)
		}

		select {
		case comp := <-c.compCh:
			if c.handleCompletion(comp) && c.retryBackoff > 0 {
				time.Sleep(c.retryBackoff)
			}
		case <-c.wake:
		}
	}
}

// handleCompletion applies one receipt. It returns true if the receipt
// reports a failure. Receipts that do not match the page's current
// in-flight version (late, duplicated, or straggler receipts) are ignored,
// and a successful receipt acknowledges only its own version: a page
// modified during the writeback stays dirty.
func (c *Cache) handleCompletion(comp Completion) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pages[comp.PageID]
	if !ok || !p.inflight || p.wbVersion != comp.Version {
		return false
	}
	p.inflight = false
	if comp.Err != nil {
		// The version was not persisted; the page stays dirty (its
		// version is still ahead of acked) and will be retried.
		c.broadcastLocked()
		return true
	}
	if comp.Version > p.acked {
		p.acked = comp.Version
	}
	c.broadcastLocked()
	return false
}

// hasUnflushedLocked reports whether any page is dirty or in writeback.
func (c *Cache) hasUnflushedLocked() bool {
	for _, p := range c.pages {
		if p.inflight || p.version > p.acked {
			return true
		}
	}
	return false
}

// broadcastLocked wakes every goroutine waiting for a state change.
func (c *Cache) broadcastLocked() {
	close(c.notify)
	c.notify = make(chan struct{})
}

// kick nudges the scheduler loop without blocking.
func (c *Cache) kick() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
