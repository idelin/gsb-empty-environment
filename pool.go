// Package bufpool implements a bounded, thread-safe vector buffer pool.
//
// Buffers are borrowed through handles. Each borrow from the idle pool bumps
// the buffer's generation, invalidating handles from earlier generations.
// Capacity bytes are billed against the pool budget; length is tracked
// separately in the ledger.
package bufpool

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrNoBudget is returned when the capacity budget (idle + in-use)
	// cannot cover a requested allocation. Failed acquires never touch
	// reference counts.
	ErrNoBudget = errors.New("bufpool: capacity budget exhausted")
	// ErrReleased is returned when operating on an already released handle.
	ErrReleased = errors.New("bufpool: handle already released")
	// ErrStale is returned when a handle's generation no longer matches the
	// buffer's current generation.
	ErrStale = errors.New("bufpool: stale handle generation")
	// ErrShared is returned when requesting mutable access while read-only
	// borrows are outstanding.
	ErrShared = errors.New("bufpool: buffer has shared read-only borrows")
	// ErrNotWritable is returned when mutable access is requested through a
	// read-only (retained) handle.
	ErrNotWritable = errors.New("bufpool: handle is read-only")
)

// buffer is a physical backing allocation tracked by the ledger.
type buffer struct {
	id         uint64
	data       []byte // len(data) == length, cap(data) == billed capacity
	length     int
	generation uint64
	refs       int
	writable   bool // current borrow grants mutable access
}

// Pool is a bounded vector buffer pool. The capacity budget covers both idle
// and in-use buffers.
type Pool struct {
	mu        sync.Mutex
	cond      *sync.Cond
	maxBytes  int64
	usedBytes int64 // capacity bytes, idle + in-use
	idle      []*buffer
	nextID    uint64
	ledger    *Ledger
}

// NewPool creates a pool whose total capacity (idle + in-use) never exceeds
// maxBytes.
func NewPool(maxBytes int64) *Pool {
	p := &Pool{maxBytes: maxBytes, ledger: NewLedger()}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Ledger returns the pool's independent physical-buffer ledger.
func (p *Pool) Ledger() *Ledger { return p.ledger }

// Acquire borrows a buffer with the given length and capacity. It reuses an
// idle buffer when one has sufficient capacity, otherwise allocates a new
// physical buffer if the budget allows. A failed acquire changes no reference
// counts and bills nothing.
func (p *Pool) Acquire(length, capacity int, writable bool) (*Handle, error) {
	if length < 0 || capacity < length {
		return nil, fmt.Errorf("bufpool: invalid length/capacity: %d/%d", length, capacity)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.acquireLocked(length, capacity, writable)
}

func (p *Pool) acquireLocked(length, capacity int, writable bool) (*Handle, error) {
	if idx := p.findIdleLocked(capacity); idx >= 0 {
		buf := p.idle[idx]
		p.idle = append(p.idle[:idx], p.idle[idx+1:]...)
		buf.generation++
		buf.refs = 1
		buf.writable = writable
		buf.length = length
		buf.data = buf.data[:length]
		p.ledger.noteBorrow(buf)
		return &Handle{pool: p, buf: buf, gen: buf.generation, writable: writable}, nil
	}
	need := int64(capacity)
	if p.usedBytes+need > p.maxBytes {
		return nil, ErrNoBudget
	}
	p.nextID++
	buf := &buffer{
		id:         p.nextID,
		data:       make([]byte, length, capacity),
		length:     length,
		generation: 1,
		refs:       1,
		writable:   writable,
	}
	p.usedBytes += need
	p.ledger.noteAlloc(buf)
	return &Handle{pool: p, buf: buf, gen: buf.generation, writable: writable}, nil
}

// findIdleLocked returns the index of the smallest idle buffer with enough
// capacity, or -1.
func (p *Pool) findIdleLocked(capacity int) int {
	best, bestCap := -1, 0
	for i, buf := range p.idle {
		c := cap(buf.data)
		if c >= capacity && (best < 0 || c < bestCap) {
			best, bestCap = i, c
		}
	}
	return best
}

// AcquireWait blocks until a buffer can be borrowed or ctx is canceled.
func (p *Pool) AcquireWait(ctx context.Context, length, capacity int, writable bool) (*Handle, error) {
	for {
		p.mu.Lock()
		h, err := p.acquireLocked(length, capacity, writable)
		if err != ErrNoBudget {
			p.mu.Unlock()
			return h, err
		}
		if err := p.waitLocked(ctx); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		p.mu.Unlock()
	}
}

// waitLocked waits on the pool condition with ctx cancellation. p.mu must be
// held; it remains held on return.
func (p *Pool) waitLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		case <-done:
		}
	}()
	p.cond.Wait()
	return ctx.Err()
}

// AcquireMany borrows up to n buffers. If the budget runs out it returns the
// buffers acquired so far together with ErrNoBudget; reference counts of
// unrelated buffers are never touched by the failed portion.
func (p *Pool) AcquireMany(n, length, capacity int, writable bool) ([]*Handle, error) {
	handles := make([]*Handle, 0, n)
	for i := 0; i < n; i++ {
		h, err := p.Acquire(length, capacity, writable)
		if err != nil {
			return handles, err
		}
		handles = append(handles, h)
	}
	return handles, nil
}

// Clear releases only the idle portion of the pool, returning its capacity to
// the budget. In-use buffers stay billed until released.
func (p *Pool) Clear() {
	p.mu.Lock()
	for _, buf := range p.idle {
		p.usedBytes -= int64(cap(buf.data))
		p.ledger.noteFree(buf)
	}
	p.idle = nil
	p.cond.Broadcast()
	p.mu.Unlock()
}

// Stats reports pool utilization. Length and capacity are billed separately:
// capacity against the budget, length only as ledger information.
type Stats struct {
	MaxBytes       int64
	UsedBytes      int64 // capacity bytes, idle + in-use
	IdleBytes      int64
	InUseBytes     int64
	IdleBuffers    int
	PhysicalAllocs int64
	PhysicalFrees  int64
}

// Stats returns a consistent snapshot of pool utilization.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := Stats{
		MaxBytes:    p.maxBytes,
		UsedBytes:   p.usedBytes,
		IdleBuffers: len(p.idle),
	}
	for _, buf := range p.idle {
		s.IdleBytes += int64(cap(buf.data))
	}
	s.InUseBytes = s.UsedBytes - s.IdleBytes
	ls := p.ledger.snapshot()
	s.PhysicalAllocs = ls.Allocs
	s.PhysicalFrees = ls.Frees
	return s
}

// releaseLocked returns a buffer to the idle pool once its refs hit zero.
func (p *Pool) releaseLocked(buf *buffer) {
	p.idle = append(p.idle, buf)
	p.cond.Broadcast()
}
