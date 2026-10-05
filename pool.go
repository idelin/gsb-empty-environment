// Package pool implements a bounded worker pool where every submission
// returns an independent, cancellable ticket. Cancellation, task start and
// result delivery may race; a single CAS-guarded state machine guarantees
// each ticket reaches exactly one terminal state and that a delivered
// result can never be overwritten by cancellation.
package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClosed   = errors.New("pool: closed")
	ErrCanceled = errors.New("pool: ticket canceled")
	ErrTimeout  = errors.New("pool: wait timed out")
)

// State is the ticket lifecycle state.
type State int32

const (
	Pending State = iota
	Running
	Succeeded // terminal
	Failed    // terminal
	Canceled  // terminal
)

func (s State) Terminal() bool { return s >= Succeeded }

func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	case Canceled:
		return "Canceled"
	}
	return "Unknown"
}

// Hooks are test-only observation points inside the worker, used to place
// barriers between the CAS steps and deterministically enumerate races.
type Hooks struct {
	BeforeStart   func(*Ticket) // after dequeue, before Pending->Running CAS
	AfterStart    func(*Ticket) // after Pending->Running CAS succeeds
	BeforeDeliver func(*Ticket) // after fn returns, before Running->Succeeded/Failed CAS
}

// Pool is a bounded worker pool. Capacity bounds the number of tickets
// holding a slot (queued + running); a slot is returned exactly once, when
// the ticket reaches its terminal state.
type Pool struct {
	clock  Clock
	hooks  Hooks
	slots  chan struct{}
	queue  chan *Ticket
	quit   chan struct{}
	closed atomic.Bool
	wg     sync.WaitGroup

	mu     sync.Mutex
	live   map[*Ticket]struct{}
	nextID atomic.Uint64
}

type Option func(*Pool)

func WithClock(c Clock) Option { return func(p *Pool) { p.clock = c } }
func WithHooks(h Hooks) Option { return func(p *Pool) { p.hooks = h } }

// New creates a pool with the given worker count and capacity.
func New(workers, capacity int, opts ...Option) *Pool {
	p := &Pool{
		clock: realClock{},
		slots: make(chan struct{}, capacity),
		queue: make(chan *Ticket, capacity),
		quit:  make(chan struct{}),
		live:  make(map[*Ticket]struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// Submit enqueues fn and returns its ticket. It blocks while the pool is at
// capacity, until a slot frees up, ctx is done, or the pool is closed.
func (p *Pool) Submit(ctx context.Context, fn func(context.Context) (any, error)) (*Ticket, error) {
	if p.closed.Load() {
		return nil, ErrClosed
	}
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	tctx, cancel := context.WithCancel(context.Background())
	t := &Ticket{
		id:     p.nextID.Add(1),
		pool:   p,
		fn:     fn,
		ctx:    tctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	p.mu.Lock()
	if p.closed.Load() {
		p.mu.Unlock()
		<-p.slots
		cancel()
		return nil, ErrClosed
	}
	p.live[t] = struct{}{}
	p.mu.Unlock()
	// Never blocks: a held slot guarantees room in the buffered queue.
	p.queue <- t
	return t, nil
}

// Close marks the pool closed, force-cancels every non-terminal ticket (so
// all waiters exit) and cancels running tasks' contexts. Submit calls after
// Close fail with ErrClosed. Results already delivered are unaffected.
func (p *Pool) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	close(p.quit)
	p.mu.Lock()
	tickets := make([]*Ticket, 0, len(p.live))
	for t := range p.live {
		tickets = append(tickets, t)
	}
	p.mu.Unlock()
	for _, t := range tickets {
		t.terminate(Canceled)
	}
}

// Wait blocks until all workers have exited (after Close).
func (p *Pool) Wait() { p.wg.Wait() }

// Available reports how many capacity slots are currently free.
func (p *Pool) Available() int { return cap(p.slots) - len(p.slots) }

func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case t := <-p.queue:
			p.run(t)
		case <-p.quit:
			return
		}
	}
}

func (p *Pool) run(t *Ticket) {
	if p.hooks.BeforeStart != nil {
		p.hooks.BeforeStart(t)
	}
	if !t.state.CompareAndSwap(int32(Pending), int32(Running)) {
		return // canceled before start; cancel path already released the slot
	}
	if p.hooks.AfterStart != nil {
		p.hooks.AfterStart(t)
	}
	res, err := t.fn(t.ctx)
	if p.hooks.BeforeDeliver != nil {
		p.hooks.BeforeDeliver(t)
	}
	next := Succeeded
	if err != nil {
		next = Failed
	}
	t.result, t.err = res, err
	// If cancellation won the race while fn was running, the result is
	// discarded and the canceled state stands.
	if t.state.CompareAndSwap(int32(Running), int32(next)) {
		t.finish()
	}
}

// Ticket is the independent handle returned by every Submit.
type Ticket struct {
	id     uint64
	pool   *Pool
	fn     func(context.Context) (any, error)
	ctx    context.Context
	cancel context.CancelFunc

	state  atomic.Int32
	done   chan struct{} // closed exactly once, at the terminal transition
	result any
	err    error
}

func (t *Ticket) ID() uint64            { return t.id }
func (t *Ticket) State() State          { return State(t.state.Load()) }
func (t *Ticket) Done() <-chan struct{} { return t.done }

// Cancel moves the ticket to Canceled unless it already reached a terminal
// state. It reports whether this call performed the transition. A delivered
// result (Succeeded/Failed) can never be overwritten: Cancel then returns
// false and the result stays observable.
func (t *Ticket) Cancel() bool { return t.terminate(Canceled) }

// terminate performs the single terminal transition; only the CAS winner
// releases the slot and closes done, so both happen exactly once.
func (t *Ticket) terminate(target State) bool {
	for {
		s := State(t.state.Load())
		if s.Terminal() {
			return false
		}
		if t.state.CompareAndSwap(int32(s), int32(target)) {
			t.cancel()
			t.finish()
			return true
		}
	}
}

func (t *Ticket) finish() {
	p := t.pool
	p.mu.Lock()
	delete(p.live, t)
	p.mu.Unlock()
	<-p.slots // return the capacity slot; never blocks, the ticket holds one
	close(t.done)
}

// Wait blocks until the ticket reaches a terminal state or ctx is done.
// A canceled ticket reports ErrCanceled; a failed one reports fn's error.
func (t *Ticket) Wait(ctx context.Context) (any, error) {
	select {
	case <-t.done:
		return t.outcome()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WaitTimeout is Wait bounded by the pool's clock, which tests can drive
// with a ManualClock for deterministic timeout enumeration.
func (t *Ticket) WaitTimeout(d time.Duration) (any, error) {
	select {
	case <-t.done:
		return t.outcome()
	case <-t.pool.clock.After(d):
		return nil, ErrTimeout
	}
}

func (t *Ticket) outcome() (any, error) {
	if State(t.state.Load()) == Canceled {
		return nil, ErrCanceled
	}
	return t.result, t.err
}
