package netsec

import (
	"context"
	"sync"
)

type pageState uint8

const (
	stateFree    pageState = iota // zeroed and available on the free list
	stateLive                     // referenced by at least one authorized snapshot
	statePending                  // last reference released, queued for zeroing
	stateZeroing                  // popped by the reclaimer, Zero in progress
	stateDead                     // zeroing failed; quarantined forever
)

// PhysPage is one fixed-size backing memory page owned by a Pool. Callers
// never create PhysPage values directly.
type PhysPage struct {
	id    int
	mem   []byte
	refs  int
	state pageState
}

// ID returns the stable identifier of the physical page. The same ID is
// reused only after a page has been zeroed and returned to the free list.
func (pg *PhysPage) ID() int { return pg.id }

// Bytes returns the page's backing memory. The contents must only be mutated
// through the Host copy-on-write API; shared pages are treated as read-only.
func (pg *PhysPage) Bytes() []byte { return pg.mem }

// PoolStats is a point-in-time snapshot of pool accounting. The four bucket
// counts always sum to Created.
type PoolStats struct {
	PageSize    int
	Capacity    int
	Created     int // backing pages allocated since startup (<= Capacity)
	Free        int // zeroed pages available for reallocation
	Live        int // pages with at least one authorized reference
	PendingZero int // pages queued for or undergoing zeroing
	Quarantined int // pages permanently retired after a zeroing failure
}

// Pool is a fixed-capacity allocator of physical pages. A page becomes
// allocatable again only after its last authorized reference has been
// released and the configured Zeroer has successfully wiped it. Pages whose
// zeroing fails are quarantined and never handed out again.
type Pool struct {
	mu         sync.Mutex
	cond       *sync.Cond
	pageSize   int
	capacity   int
	nextID     int
	created    int
	pages      map[int]*PhysPage
	free       []*PhysPage
	queue      []*PhysPage
	zeroing    int
	quarantine []*PhysPage
	zeroer     Zeroer
	closed     bool
	done       chan struct{}
}

// NewPool creates a pool with pageSize-byte pages and at most capacity
// simultaneously existing backing pages, then starts its reclaimer goroutine.
func NewPool(pageSize, capacity int, zeroer Zeroer) (*Pool, error) {
	if pageSize <= 0 || capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	if zeroer == nil {
		return nil, ErrInvalidConfig
	}
	p := &Pool{
		pageSize: pageSize,
		capacity: capacity,
		pages:    make(map[int]*PhysPage),
		zeroer:   zeroer,
		done:     make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	go p.reclaim()
	return p, nil
}

// TryAlloc returns a zeroed page with one reference, or ErrPoolExhausted when
// no page can currently be provided.
func (p *Pool) TryAlloc() (*PhysPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tryAllocLocked()
}

// Alloc returns a zeroed page with one reference, waiting until one becomes
// available. It returns early when ctx is canceled or the pool is closed.
func (p *Pool) Alloc(ctx context.Context) (*PhysPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		pg, err := p.tryAllocLocked()
		if err == nil {
			return pg, nil
		}
		if err != ErrPoolExhausted {
			return nil, err
		}
		if err := p.waitLocked(ctx, p.allocatable); err != nil {
			return nil, err
		}
	}
}

func (p *Pool) tryAllocLocked() (*PhysPage, error) {
	if p.closed {
		return nil, ErrClosed
	}
	if n := len(p.free); n > 0 {
		pg := p.free[n-1]
		p.free = p.free[:n-1]
		pg.state = stateLive
		pg.refs = 1
		return pg, nil
	}
	if p.created >= p.capacity {
		return nil, ErrPoolExhausted
	}
	pg := &PhysPage{
		id:    p.nextID,
		mem:   make([]byte, p.pageSize),
		refs:  1,
		state: stateLive,
	}
	p.nextID++
	p.created++
	p.pages[pg.id] = pg
	return pg, nil
}

func (p *Pool) allocatable() bool {
	return p.closed || len(p.free) > 0 || p.created < p.capacity
}

// Acquire adds one authorized reference to a live page. It is used when a
// snapshot forks and shares its pages with a child.
func (p *Pool) Acquire(pg *PhysPage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg.state != stateLive {
		panic("netsec: Acquire on non-live page")
	}
	pg.refs++
}

// Release drops one authorized reference. When the last reference is released
// the page is detached and queued for zeroing; it is not returned to the free
// list until the Zeroer has successfully wiped it.
func (p *Pool) Release(pg *PhysPage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg.state != stateLive {
		panic("netsec: Release on non-live page")
	}
	pg.refs--
	if pg.refs < 0 {
		panic("netsec: page reference count underflow")
	}
	if pg.refs == 0 {
		pg.state = statePending
		p.queue = append(p.queue, pg)
		p.cond.Broadcast()
	}
}

// Refs returns the current number of authorized references to a page.
func (p *Pool) Refs(pg *PhysPage) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pg.refs
}

// WaitAvailable blocks until an allocation could proceed, ctx is canceled, or
// the pool is closed.
func (p *Pool) WaitAvailable(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitLocked(ctx, p.allocatable)
}

// WaitDrained blocks until no page is queued for zeroing or being zeroed.
func (p *Pool) WaitDrained(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitLocked(ctx, func() bool {
		return len(p.queue) == 0 && p.zeroing == 0
	})
}

// waitLocked waits on the pool condition until pred holds or ctx is canceled.
// The caller must hold p.mu.
func (p *Pool) waitLocked(ctx context.Context, pred func() bool) error {
	if pred() {
		return nil
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		case <-done:
		}
	}()
	for !pred() && ctx.Err() == nil {
		p.cond.Wait()
	}
	close(done)
	return ctx.Err()
}

// reclaim is the background goroutine that zeroes detached pages and either
// returns them to the free list or quarantines them on failure.
func (p *Pool) reclaim() {
	defer close(p.done)
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.closed {
			p.cond.Wait()
		}
		if p.closed && len(p.queue) == 0 {
			p.mu.Unlock()
			return
		}
		pg := p.queue[0]
		p.queue = p.queue[1:]
		pg.state = stateZeroing
		p.zeroing++
		mem := pg.mem
		p.mu.Unlock()

		err := p.zeroer.Zero(pg.id, mem)

		p.mu.Lock()
		p.zeroing--
		if err != nil {
			pg.state = stateDead
			p.quarantine = append(p.quarantine, pg)
		} else if !allZero(pg.mem) {
			// Defensive: a Zeroer that reports success without wiping is
			// treated as a failure so stale secrets are never reallocated.
			pg.state = stateDead
			p.quarantine = append(p.quarantine, pg)
		} else {
			pg.state = stateFree
			p.free = append(p.free, pg)
		}
		p.cond.Broadcast()
		p.mu.Unlock()
	}
}

// Stats returns a point-in-time snapshot of pool accounting.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	live := 0
	for _, pg := range p.pages {
		if pg.state == stateLive {
			live++
		}
	}
	return PoolStats{
		PageSize:    p.pageSize,
		Capacity:    p.capacity,
		Created:     p.created,
		Free:        len(p.free),
		Live:        live,
		PendingZero: len(p.queue) + p.zeroing,
		Quarantined: len(p.quarantine),
	}
}

// Close drains the zeroing queue and stops the reclaimer goroutine. If the
// zeroer is paused it must be resumed before Close, otherwise Close blocks.
// Close is idempotent.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.done
		return
	}
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	<-p.done
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
