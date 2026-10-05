// Package pool implements a bounded worker pool with cancellable
// submissions.
//
// Every call to Submit returns an independent Ticket. A Ticket reaches
// exactly one terminal state: StateDone (a result was delivered) or
// StateCancelled. Cancellation, task start and result collection may
// happen concurrently; all state transitions are serialized so that a
// delivered result is never overwritten by a cancellation, and a
// cancellation that wins the race causes the eventual result to be
// discarded.
package pool

import (
	"errors"
	"sync"
	"time"
)

// State is the lifecycle state of a Ticket.
type State int

const (
	// StatePending: a slot was acquired but the task has not started.
	StatePending State = iota
	// StateRunning: the task function is executing.
	StateRunning
	// StateDone: terminal state, a result was delivered.
	StateDone
	// StateCancelled: terminal state, the ticket was cancelled.
	StateCancelled
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateRunning:
		return "running"
	case StateDone:
		return "done"
	case StateCancelled:
		return "cancelled"
	}
	return "unknown"
}

var (
	// ErrShutdown is returned by Submit and SubmitWithin once the pool
	// has been shut down.
	ErrShutdown = errors.New("pool: shut down")
	// ErrCancelled is returned by Ticket.Wait when the ticket was
	// cancelled before its result was delivered.
	ErrCancelled = errors.New("pool: ticket cancelled")
	// ErrTimeout is returned by SubmitWithin when no slot became
	// available within the timeout.
	ErrTimeout = errors.New("pool: submit timed out")
)

// Task is the unit of work executed by the pool.
type Task func() (interface{}, error)

// Ticket tracks one submitted task. It has exactly one terminal state.
type Ticket struct {
	mu     sync.Mutex
	state  State
	result interface{}
	err    error
	done   chan struct{} // closed exactly once, on terminal transition
	pool   *Pool
}

// Cancel attempts to move the ticket to StateCancelled. It reports
// whether the cancellation won the race: it returns false if a result
// was already delivered (StateDone) or the ticket was already
// cancelled. A delivered result is never overwritten.
//
// Cancelling a pending ticket prevents the task from starting and
// releases its capacity slot. Cancelling a running ticket discards the
// eventual result; the slot is released when the task function returns,
// since goroutines cannot be killed.
func (t *Ticket) Cancel() bool {
	t.mu.Lock()
	if t.state == StateDone || t.state == StateCancelled {
		t.mu.Unlock()
		return false
	}
	t.state = StateCancelled
	close(t.done)
	t.mu.Unlock()
	if t.pool != nil {
		t.pool.removePending(t)
	}
	return true
}

// Wait blocks until the ticket reaches its terminal state and returns
// the delivered result, or ErrCancelled if the ticket was cancelled.
func (t *Ticket) Wait() (interface{}, error) {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == StateCancelled {
		return nil, ErrCancelled
	}
	return t.result, t.err
}

// State returns the current state of the ticket.
func (t *Ticket) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// markRunning transitions pending -> running. It fails if the ticket
// was cancelled before the task started.
func (t *Ticket) markRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != StatePending {
		return false
	}
	t.state = StateRunning
	return true
}

// complete delivers the task result. It fails if the ticket already
// reached a terminal state, in which case the result is discarded.
func (t *Ticket) complete(result interface{}, err error) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == StateDone || t.state == StateCancelled {
		return false
	}
	t.state = StateDone
	t.result, t.err = result, err
	close(t.done)
	return true
}

// Pool is a bounded worker pool. At most capacity tasks hold a slot at
// any time; slots are returned when a task finishes or its pending
// ticket is cancelled.
type Pool struct {
	slots    chan struct{}
	clock    Clock
	shutdown chan struct{}

	mu      sync.Mutex
	closed  bool
	pending map[*Ticket]struct{}

	wg sync.WaitGroup // in-flight run goroutines

	shutdownOnce sync.Once

	// Test hooks (nil in production). Set before submitting.
	hookPreSubmit   func()
	hookPreStart    func()
	hookPostStart   func()
	hookPreComplete func()
}

// Option configures a Pool.
type Option func(*Pool)

// WithClock sets the clock used by SubmitWithin. Defaults to the real
// clock; tests should inject a FakeClock.
func WithClock(c Clock) Option {
	return func(p *Pool) { p.clock = c }
}

// New returns a Pool that runs at most capacity tasks concurrently.
func New(capacity int, opts ...Option) *Pool {
	if capacity <= 0 {
		panic("pool: capacity must be positive")
	}
	p := &Pool{
		slots:    make(chan struct{}, capacity),
		clock:    realClock{},
		shutdown: make(chan struct{}),
		pending:  make(map[*Ticket]struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Submit blocks until a capacity slot is available, then returns a
// Ticket for the submitted task. It returns ErrShutdown if the pool is
// or becomes shut down while waiting.
func (p *Pool) Submit(task Task) (*Ticket, error) {
	return p.submit(task, nil)
}

// SubmitWithin is like Submit but gives up with ErrTimeout if no slot
// becomes available within the timeout (measured on the pool's clock).
func (p *Pool) SubmitWithin(task Task, timeout time.Duration) (*Ticket, error) {
	return p.submit(task, p.clock.After(timeout))
}

func (p *Pool) submit(task Task, timeout <-chan time.Time) (*Ticket, error) {
	if p.hookPreSubmit != nil {
		p.hookPreSubmit()
	}
	select {
	case p.slots <- struct{}{}:
	case <-p.shutdown:
		return nil, ErrShutdown
	case <-timeout: // nil channel blocks forever
		return nil, ErrTimeout
	}
	t := &Ticket{state: StatePending, done: make(chan struct{}), pool: p}
	p.mu.Lock()
	if p.closed {
		// Shutdown won the race against slot acquisition.
		p.mu.Unlock()
		<-p.slots
		return nil, ErrShutdown
	}
	p.pending[t] = struct{}{}
	p.wg.Add(1) // under mu: cannot race with Shutdown's wg.Wait
	p.mu.Unlock()
	go p.run(t, task)
	return t, nil
}

func (p *Pool) run(t *Ticket, task Task) {
	defer p.wg.Done()
	defer func() { <-p.slots }() // always return the capacity slot
	if p.hookPreStart != nil {
		p.hookPreStart()
	}
	if !t.markRunning() {
		return // cancelled before start
	}
	p.removePending(t)
	if p.hookPostStart != nil {
		p.hookPostStart()
	}
	result, err := task()
	if p.hookPreComplete != nil {
		p.hookPreComplete()
	}
	t.complete(result, err) // discarded if cancellation won
}

func (p *Pool) removePending(t *Ticket) {
	p.mu.Lock()
	delete(p.pending, t)
	p.mu.Unlock()
}

// Shutdown stops the pool: blocked and future Submit calls fail with
// ErrShutdown, pending tickets are cancelled (their Wait callers return
// ErrCancelled), and Shutdown blocks until all running tasks finish and
// every capacity slot is returned. It is safe to call more than once.
func (p *Pool) Shutdown() {
	p.shutdownOnce.Do(func() {
		close(p.shutdown)
		p.mu.Lock()
		p.closed = true
		pending := make([]*Ticket, 0, len(p.pending))
		for t := range p.pending {
			pending = append(pending, t)
		}
		p.mu.Unlock()
		for _, t := range pending {
			t.Cancel()
		}
		p.wg.Wait()
	})
}
