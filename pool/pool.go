// Package pool implements a bounded pool of expensive resources.
//
// The pool limits the total number of resources that are being created plus
// the number currently borrowed by callers. Creation is slow and may fail;
// the factory receives a context so an abandoned acquire can cancel an
// in-flight creation. A resource whose creation finishes after its acquirer
// has given up is destroyed rather than handed out in the background.
package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrClosed is returned when acquiring from a closed pool, or releasing a
	// handle that a forced close has already reclaimed.
	ErrClosed = errors.New("pool: closed")

	// ErrDoubleRelease is returned when the same handle is released twice.
	ErrDoubleRelease = errors.New("pool: resource released more than once")
)

// CreateFunc creates one resource. It must honor ctx cancellation so that
// callers can abandon slow creations.
type CreateFunc func(ctx context.Context) (resource any, err error)

// CloseFunc destroys one resource. It is invoked exactly once for every
// resource that was successfully created.
type CloseFunc func(resource any) error

// Config configures a Pool.
type Config struct {
	// MaxOpen bounds in-flight creations plus borrowed resources. Required.
	MaxOpen int
	// Create builds a new resource. Required.
	Create CreateFunc
	// Close destroys a resource. A nil Close is a no-op.
	Close CloseFunc
}

const (
	stateBorrowed int32 = iota
	stateReleased
	stateReclaimed // force close took it back while it was borrowed
)

// Handle is an acquired resource. Release it exactly once.
type Handle struct {
	// Resource is the borrowed value.
	Resource any

	p     *Pool
	slot  *slot
	state atomic.Int32
}

// MarkBad marks the borrowed resource as defective. A bad resource is
// destroyed on Release rather than returned for reuse.
func (h *Handle) MarkBad() {
	p := h.p
	p.mu.Lock()
	if _, ok := p.out[h.slot]; ok {
		h.slot.bad = true
	}
	p.mu.Unlock()
}

type slot struct {
	resource any
	bad      bool
}

type acquireResult struct {
	handle *Handle
	err    error
}

// waiter tracks one blocked Acquire call.
type waiter struct {
	ctx context.Context
	ch  chan acquireResult // exactly one terminal result, buffered

	ready    bool // a terminal result was produced
	creating bool // a create goroutine owns this waiter
}

// Pool is a bounded, concurrency-safe resource pool.
type Pool struct {
	mu sync.Mutex
	cv *sync.Cond

	create CreateFunc
	close  CloseFunc
	max    int

	idle     []*slot
	out      map[*slot]*Handle
	creating int
	closed   bool

	// forceCtx is cancelled by CloseForce; every create ctx watches it.
	forceCtx  context.Context
	stopForce context.CancelFunc

	waiters []*waiter

	destroyErrs []error
}

// Stats is a point-in-time occupancy snapshot.
type Stats struct {
	Idle     int
	InUse    int
	Creating int
	Closed   bool
}

// New constructs a Pool.
func New(cfg Config) *Pool {
	if cfg.Create == nil {
		panic("pool: nil create function")
	}
	if cfg.MaxOpen <= 0 {
		panic("pool: MaxOpen must be greater than zero")
	}
	p := &Pool{
		create: cfg.Create,
		close:  cfg.Close,
		max:    cfg.MaxOpen,
		out:    make(map[*slot]*Handle),
	}
	p.cv = sync.NewCond(&p.mu)
	p.forceCtx, p.stopForce = context.WithCancel(context.Background())
	return p
}

// active returns the consumed quota: in-flight creations plus borrowed
// resources. Idle resources consume no quota.
func (p *Pool) active() int { return p.creating + len(p.out) }

// Acquire borrows a resource, creating one if necessary.
//
// If ctx is cancelled while a resource is being created, Acquire returns the
// context error immediately and the create is cancelled; if the factory still
// produces a resource, that resource is destroyed.
func (p *Pool) Acquire(ctx context.Context) (*Handle, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	w := &waiter{ctx: ctx, ch: make(chan acquireResult, 1)}

	// Wake the scheduler when the caller's context fires.
	var stopWatcher func() bool
	if done := ctx.Done(); done != nil {
		stopWatcher = context.AfterFunc(ctx, func() {
			p.mu.Lock()
			p.cv.Broadcast()
			p.mu.Unlock()
		})
	}

	for {
		p.schedule(w)
		if w.ready {
			break
		}
		p.cv.Wait()
	}
	res := <-w.ch
	p.mu.Unlock()

	if stopWatcher != nil {
		stopWatcher()
	}
	return res.handle, res.err
}

// finish records the terminal result for a waiter and removes it from the
// waiting queue so it can never block later waiters. Caller holds p.mu.
func (p *Pool) finish(w *waiter, res acquireResult) {
	if w.ready {
		panic("pool: waiter finished twice")
	}
	w.ready = true
	w.ch <- res
	for i, q := range p.waiters {
		if q == w {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			break
		}
	}
}

// schedule performs one scheduling pass for a waiter: hand out an idle
// resource, start a creation, finalize a cancelled/closed waiter, or leave it
// blocked. Caller must hold p.mu.
func (p *Pool) schedule(w *waiter) {
	switch {
	case w.ready:
		// Terminal result already produced on an earlier pass.

	case p.closed:
		p.finish(w, acquireResult{err: ErrClosed})

	case w.ctx.Err() != nil:
		// The acquirer gave up. When a create is in flight its goroutine
		// sees w.ready and destroys anything it still produces.
		p.finish(w, acquireResult{err: w.ctx.Err()})

	case w.creating:
		// Create in flight; this waiter cannot take any other path.

	case len(p.idle) > 0:
		s := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		h := &Handle{Resource: s.resource, p: p, slot: s}
		p.out[s] = h
		p.finish(w, acquireResult{handle: h})

	case p.active() < p.max:
		p.creating++
		w.creating = true
		ctx, cancel := context.WithCancel(w.ctx)
		// A forced shutdown cancels every create even if the caller's own
		// context is still alive.
		stopForceWatch := context.AfterFunc(p.forceCtx, cancel)
		go p.runCreate(w, ctx, cancel, stopForceWatch)

	default:
		p.enqueue(w)
	}
}

func (p *Pool) enqueue(w *waiter) {
	for _, q := range p.waiters {
		if q == w {
			return
		}
	}
	p.waiters = append(p.waiters, w)
}

// runCreate executes the factory outside the pool lock.
func (p *Pool) runCreate(w *waiter, ctx context.Context, cancel context.CancelFunc, stopForceWatch func() bool) {
	resource, err := p.create(ctx)
	stopForceWatch()
	cancel() // release ctx resources even when the parent never ends

	p.mu.Lock()
	p.creating--

	switch {
	case w.ready:
		// Acquirer already has its terminal result; never hand a resource
		// out in the background.
		if err == nil {
			p.destroyLocked(&slot{resource: resource})
		}
	case p.closed:
		if err == nil {
			p.destroyLocked(&slot{resource: resource})
		}
		p.finish(w, acquireResult{err: ErrClosed})
	case w.ctx.Err() != nil:
		// Cancellation raced with completion: reclaim the resource instead
		// of delivering it, and report the cancellation.
		if err == nil {
			p.destroyLocked(&slot{resource: resource})
		}
		p.finish(w, acquireResult{err: w.ctx.Err()})
	case err != nil:
		p.finish(w, acquireResult{err: err})
	default:
		s := &slot{resource: resource}
		h := &Handle{Resource: resource, p: p, slot: s}
		p.out[s] = h
		p.finish(w, acquireResult{handle: h})
	}

	p.pumpWaiters()
	p.cv.Broadcast()
	p.mu.Unlock()
}

// pumpWaiters advances queued waiters FIFO, starting creates or handing out
// idle resources that appeared. Caller must hold p.mu.
func (p *Pool) pumpWaiters() {
	queued := p.waiters
	p.waiters = nil
	for _, w := range queued {
		// A waiter whose create completed or whose context fired is already
		// finished (finish removed it); never re-schedule it here.
		if w.ready || w.creating {
			continue
		}
		p.schedule(w)
	}
}

// Release returns a healthy resource for reuse or destroys a bad one. It is an
// error to release a handle twice, or after CloseForce reclaimed it.
func (h *Handle) Release() error {
	p := h.p
	if !h.state.CompareAndSwap(stateBorrowed, stateReleased) {
		if h.state.Load() == stateReclaimed {
			return ErrClosed
		}
		return ErrDoubleRelease
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.out[h.slot]; !ok {
		return ErrDoubleRelease
	}
	delete(p.out, h.slot)

	switch {
	case h.slot.bad || p.closed:
		p.destroyLocked(h.slot)
	default:
		if !p.handOff(h.slot) {
			p.idle = append(p.idle, h.slot)
		}
	}

	p.pumpWaiters()
	p.cv.Broadcast()
	return nil
}

// handOff transfers a released slot directly to the longest-waiting acquirer,
// bypassing the idle list. Caller must hold p.mu.
func (p *Pool) handOff(s *slot) bool {
	for len(p.waiters) > 0 {
		w := p.waiters[0]
		p.waiters = p.waiters[1:]
		if w.ready || w.creating || w.ctx.Err() != nil {
			continue
		}
		h := &Handle{Resource: s.resource, p: p, slot: s}
		p.out[s] = h
		p.finish(w, acquireResult{handle: h})
		return true
	}
	return false
}

// Close waits for all in-flight creations and all borrowed resources to be
// returned, then destroys every resource. Creations are not cancelled.
func (p *Pool) Close() error {
	return p.shutdown(false)
}

// CloseForce cancels in-flight creations and immediately reclaims borrowed
// resources instead of waiting for their borrowers.
func (p *Pool) CloseForce() error {
	return p.shutdown(true)
}

func (p *Pool) shutdown(force bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.Join(p.destroyErrs...)
	}
	p.closed = true
	if force {
		p.stopForce() // cancel every in-flight create
	}

	// Reject waiters that have not started a create yet.
	for _, w := range p.waiters {
		if !w.ready {
			p.finish(w, acquireResult{err: ErrClosed})
		}
	}
	p.waiters = nil

	// Idle resources go away under both policies.
	for _, s := range p.idle {
		p.destroyLocked(s)
	}
	p.idle = nil

	if force {
		// Reclaim borrowed resources out from under their handles.
		for s, h := range p.out {
			if h.state.CompareAndSwap(stateBorrowed, stateReclaimed) {
				p.destroyLocked(s)
				delete(p.out, s)
			}
		}
	}

	// Graceful: wait for creates and borrowers. Forced: wait for cancelled
	// creates to wind down; borrowers that release concurrently disappear.
	for p.creating > 0 || len(p.out) > 0 {
		p.cv.Wait()
	}
	return errors.Join(p.destroyErrs...)
}

// destroyLocked destroys one slot's resource exactly once. Caller holds p.mu.
func (p *Pool) destroyLocked(s *slot) {
	if p.close == nil {
		return
	}
	if err := p.close(s.resource); err != nil {
		p.destroyErrs = append(p.destroyErrs, fmt.Errorf("pool: close resource: %w", err))
	}
}

// Stats returns a point-in-time occupancy snapshot.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Idle:     len(p.idle),
		InUse:    len(p.out),
		Creating: p.creating,
		Closed:   p.closed,
	}
}
