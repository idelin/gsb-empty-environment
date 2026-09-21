// Package pool provides a bounded pool of expensive-to-create resources.
//
// Creation may be slow and may fail; the pool bounds the total number of
// resources that are either being created or in use. If an Acquire is
// cancelled while its creation is still in flight, the late-arriving
// resource is reclaimed (destroyed) instead of leaking. Close either waits
// for borrowed resources to be returned or forces immediate shutdown.
package pool

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrPoolClosed is returned by operations on a closed pool.
	ErrPoolClosed = errors.New("pool: pool is closed")
	// ErrDoubleReturn is returned when a lease is released more than once.
	ErrDoubleReturn = errors.New("pool: resource already returned")
)

// Factory creates a resource. It may be slow and may fail. The passed
// context is the Acquire caller's context; a cooperative factory may abort
// early, but the pool does not rely on it doing so.
type Factory func(ctx context.Context) (any, error)

// DestroyFunc disposes of a resource. It is called exactly once for every
// resource the factory ever produced: when a lease is returned as bad or
// after the pool is closed, when an idle resource is discarded at Close,
// and when a cancelled creation finishes late.
type DestroyFunc func(res any)

// ClosePolicy controls how Close treats borrowed resources.
type ClosePolicy int

const (
	// WaitForReturn blocks Close until every borrowed lease is released.
	WaitForReturn ClosePolicy = iota
	// Force closes immediately; outstanding leases are destroyed on return.
	Force
)

// Lease is a checked-out resource. It must be released exactly once.
type Lease struct {
	pool     *Pool
	res      any
	returned bool
}

// Value returns the underlying resource.
func (l *Lease) Value() any { return l.res }

// Release returns the resource to the pool. A bad resource is destroyed
// instead of being reused. Releasing twice returns ErrDoubleReturn.
func (l *Lease) Release(bad bool) error {
	p := l.pool
	p.mu.Lock()
	if l.returned {
		p.mu.Unlock()
		return ErrDoubleReturn
	}
	l.returned = true
	delete(p.inUse, l)
	reusable := !bad && !p.closed
	if reusable {
		p.idle = append(p.idle, l.res)
	}
	p.signalLocked()
	p.mu.Unlock()
	if !reusable {
		p.destroy(l.res)
	}
	return nil
}

// Pool bounds the number of resources being created plus in use.
type Pool struct {
	factory Factory
	destroy DestroyFunc
	max     int

	mu            sync.Mutex
	idle          []any
	inUse         map[*Lease]struct{}
	creating      int
	notify        chan struct{} // closed to broadcast any state change
	closedCh      chan struct{} // closed when the pool is closed
	closed        bool
	drained       chan struct{} // closed once closed && no leases outstanding
	drainedClosed bool
}

// New returns a pool that keeps at most max resources created or in use.
func New(max int, factory Factory, destroy DestroyFunc) *Pool {
	if max <= 0 {
		panic("pool: max must be positive")
	}
	if factory == nil || destroy == nil {
		panic("pool: factory and destroy are required")
	}
	return &Pool{
		factory:  factory,
		destroy:  destroy,
		max:      max,
		inUse:    make(map[*Lease]struct{}),
		notify:   make(chan struct{}),
		closedCh: make(chan struct{}),
		drained:  make(chan struct{}),
	}
}

// signalLocked wakes waiters and closes drained when appropriate.
// p.mu must be held.
func (p *Pool) signalLocked() {
	close(p.notify)
	p.notify = make(chan struct{})
	if p.closed && len(p.inUse) == 0 && !p.drainedClosed {
		close(p.drained)
		p.drainedClosed = true
	}
}

// Acquire borrows a resource, creating one if the pool is below its limit.
// It fails with the context error if the wait or the creation is cancelled;
// a creation that finishes after cancellation is reclaimed asynchronously.
func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}
		if n := len(p.idle); n > 0 {
			res := p.idle[n-1]
			p.idle = p.idle[:n-1]
			l := &Lease{pool: p, res: res}
			p.inUse[l] = struct{}{}
			p.mu.Unlock()
			return l, nil
		}
		if p.creating+len(p.inUse) < p.max {
			p.creating++
			p.mu.Unlock()
			return p.create(ctx)
		}
		notify := p.notify
		closedCh := p.closedCh
		p.mu.Unlock()
		select {
		case <-notify:
		case <-closedCh:
			return nil, ErrPoolClosed
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		p.mu.Lock()
	}
}

type createResult struct {
	res any
	err error
}

// create runs the factory for a slot already reserved in p.creating.
func (p *Pool) create(ctx context.Context) (*Lease, error) {
	done := make(chan createResult, 1)
	go func() {
		res, err := p.factory(ctx)
		done <- createResult{res, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			p.finishCreate()
			return nil, r.err
		}
		l := &Lease{pool: p, res: r.res}
		p.mu.Lock()
		p.creating--
		if p.closed {
			p.signalLocked()
			p.mu.Unlock()
			p.destroy(r.res)
			return nil, ErrPoolClosed
		}
		p.inUse[l] = struct{}{}
		p.signalLocked()
		p.mu.Unlock()
		return l, nil
	case <-ctx.Done():
		// The factory may still be running; reap its result so the
		// slot is freed and a late resource is destroyed, not leaked.
		go p.reap(done)
		return nil, ctx.Err()
	}
}

// finishCreate frees a creation slot and wakes waiters.
func (p *Pool) finishCreate() {
	p.mu.Lock()
	p.creating--
	p.signalLocked()
	p.mu.Unlock()
}

// reap waits for a cancelled creation and destroys its product.
func (p *Pool) reap(done <-chan createResult) {
	r := <-done
	p.finishCreate()
	if r.err == nil && r.res != nil {
		p.destroy(r.res)
	}
}

// Close shuts the pool down. Idle resources are destroyed immediately.
// With WaitForReturn it blocks until every borrowed lease is released (or
// ctx is done); with Force it returns at once and outstanding resources
// are destroyed as they come back.
func (p *Pool) Close(ctx context.Context, policy ClosePolicy) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	p.closed = true
	close(p.closedCh)
	idle := p.idle
	p.idle = nil
	p.signalLocked()
	drained := p.drained
	p.mu.Unlock()

	for _, res := range idle {
		p.destroy(res)
	}
	if policy == Force {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
