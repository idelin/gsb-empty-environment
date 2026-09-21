// Package wfq implements a weighted fair queuing task scheduler.
//
// Each tenant owns a FIFO task queue. Execution capacity is distributed
// among tenants proportional to their weights while a single global
// concurrency limit bounds the number of simultaneously running tasks.
//
// The selection policy is a cursor based weighted round robin (deficit
// round robin without explicit round boundaries): every time the cursor
// reaches a tenant that has waiting work, its deficit is topped up by its
// current weight and tasks are dispatched while the deficit lasts.
// Dynamic weight changes therefore only affect subsequent top ups, never
// capacity that was already granted. Tasks cancelled while waiting are
// removed before they ever reach the dispatcher, so they never consume
// any execution capacity.
package wfq

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	// ErrClosed is returned when the scheduler has been closed.
	ErrClosed = errors.New("wfq: scheduler is closed")
	// ErrUnknownTenant is returned for operations on an unregistered tenant.
	ErrUnknownTenant = errors.New("wfq: unknown tenant")
	// ErrNilTask is returned when Submit receives a nil task function.
	ErrNilTask = errors.New("wfq: nil task")
	// ErrInvalidConfig is returned for non-positive configuration values.
	ErrInvalidConfig = errors.New("wfq: invalid configuration")
	// ErrInvalidWeight is returned for non-positive weights.
	ErrInvalidWeight = errors.New("wfq: invalid weight")
)

// TaskFunc is user supplied work. The context is cancelled when the task
// is cancelled while running or when the scheduler stops while the task
// is waiting to start.
type TaskFunc func(ctx context.Context) error

// Task is a handle for a submitted task.
type Task interface {
	// Done returns a channel that is closed once the task has finished,
	// either successfully, with an error, or because it was cancelled.
	Done() <-chan struct{}
	// Err returns nil while the task is not finished. Afterwards it
	// returns the task error or ErrTaskCancelled.
	Err() error
	// Cancel removes a waiting task (it consumes no capacity) or requests
	// cooperative cancellation of a running task via its context.
	Cancel()
	// Tenant returns the tenant the task was submitted to.
	Tenant() string
}

// TaskPanicError wraps a value recovered from a panicking task.
type TaskPanicError struct{ Value any }

func (e TaskPanicError) Error() string { return "wfq: task panicked" }

// ErrTaskCancelled is reported by tasks cancelled before completion.
var ErrTaskCancelled = errors.New("wfq: task cancelled")

// Stats is a snapshot of scheduler counters.
type Stats struct {
	Submitted    uint64
	Succeeded    uint64
	Failed       uint64
	Cancelled    uint64
	Inflight     int
	PeakInflight int
	Queued       int
	Tenants      int
}

// Completed returns the number of tasks that have reached a terminal state.
func (s Stats) Completed() uint64 { return s.Succeeded + s.Failed + s.Cancelled }

// Scheduler distributes execution capacity across tenants.
type Scheduler struct {
	mu   sync.Mutex
	cond *sync.Cond

	tenants map[string]*tenant
	order   []string
	cursor  int

	concurrency int
	slots       chan struct{}

	closed bool

	queued      int
	inflight    int
	maxInflight int

	submitted uint64
	succeeded uint64
	failed    uint64
	cancelled uint64

	wg        sync.WaitGroup
	closeOnce sync.Once

	// Test hooks (nil in production), set before the dispatcher starts.
	onDispatch func(e *entry)
	onStart    func(e *entry)
}

type tenant struct {
	name    string
	weight  int
	queue   []*entry
	deficit int

	// gate is released (closed) when the most recently dispatched task
	// starts. The next dispatched task waits on its own captured gate,
	// which enforces per-tenant FIFO start order while still allowing
	// tasks of the same tenant to run concurrently.
	gate chan struct{}
}

type entry struct {
	sched  *Scheduler
	id     uint64
	tenant string
	fn     TaskFunc

	done chan struct{}

	ctx    context.Context
	cancel context.CancelCauseFunc

	queued bool // still sitting in the tenant queue

	// gate is waited on before fn runs; next is closed by this task when
	// it starts, releasing the following task of the same tenant.
	gate chan struct{}
	next chan struct{}

	finished bool
	err      error
}

func (e *entry) Done() <-chan struct{} { return e.done }

func (e *entry) Err() error {
	e.sched.mu.Lock()
	defer e.sched.mu.Unlock()
	if !e.finished {
		return nil
	}
	return e.err
}

func (e *entry) Tenant() string { return e.tenant }

func (e *entry) Cancel() { e.sched.cancelEntry(e) }

// Config configures a Scheduler.
type Config struct {
	// Concurrency is the global maximum number of simultaneously running
	// tasks. Must be positive.
	Concurrency int
	// Tenants maps tenant name to positive weight.
	Tenants map[string]int
}

// New creates a scheduler and starts its dispatcher goroutine.
func New(cfg Config) (*Scheduler, error) {
	s, err := build(cfg)
	if err != nil {
		return nil, err
	}
	s.start()
	return s, nil
}

// build constructs scheduler state without launching the dispatcher.
func build(cfg Config) (*Scheduler, error) {
	if cfg.Concurrency <= 0 {
		return nil, ErrInvalidConfig
	}
	if len(cfg.Tenants) == 0 {
		return nil, ErrInvalidConfig
	}
	s := &Scheduler{
		tenants:     make(map[string]*tenant, len(cfg.Tenants)),
		concurrency: cfg.Concurrency,
		slots:       make(chan struct{}, cfg.Concurrency),
	}
	s.cond = sync.NewCond(&s.mu)
	for name, w := range cfg.Tenants {
		if w <= 0 {
			return nil, ErrInvalidWeight
		}
		if _, exists := s.tenants[name]; exists {
			continue
		}
		g := make(chan struct{})
		close(g)
		s.tenants[name] = &tenant{name: name, weight: w, gate: g}
		s.order = append(s.order, name)
	}
	sort.Strings(s.order)
	return s, nil
}

func (s *Scheduler) start() {
	s.wg.Add(1)
	go s.dispatch()
}

// Submit enqueues fn for tenant. The returned Task can be used to wait for
// or cancel the work.
func (s *Scheduler) Submit(tenantName string, fn TaskFunc) (Task, error) {
	if fn == nil {
		return nil, ErrNilTask
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	t, ok := s.tenants[tenantName]
	if !ok {
		return nil, ErrUnknownTenant
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	s.submitted++
	e := &entry{
		sched:  s,
		id:     s.submitted - 1,
		tenant: tenantName,
		fn:     fn,
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		queued: true,
	}
	t.queue = append(t.queue, e)
	s.queued++
	s.cond.Broadcast()
	return e, nil
}

// UpdateWeight changes a tenant's weight. The new weight only affects
// execution capacity granted after the change.
func (s *Scheduler) UpdateWeight(tenantName string, weight int) error {
	if weight <= 0 {
		return ErrInvalidWeight
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenantName]
	if !ok {
		return ErrUnknownTenant
	}
	if s.closed {
		return ErrClosed
	}
	t.weight = weight
	return nil
}

// Cancel cancels a task. A waiting task is removed from its tenant queue
// and consumes no execution capacity; a running task receives context
// cancellation. Cancel is idempotent.
func (s *Scheduler) Cancel(t Task) {
	if e, ok := t.(*entry); ok && e.sched == s {
		s.cancelEntry(e)
	}
}

func (s *Scheduler) cancelEntry(e *entry) {
	s.mu.Lock()
	if e.finished {
		s.mu.Unlock()
		return
	}
	if e.queued {
		t := s.tenants[e.tenant]
		for i, q := range t.queue {
			if q == e {
				t.queue = append(t.queue[:i], t.queue[i+1:]...)
				break
			}
		}
		s.queued--
		if len(t.queue) == 0 {
			// Going idle banks no capacity: leftover quota is discarded.
			t.deficit = 0
		}
		e.queued = false
		e.finished = true
		e.err = ErrTaskCancelled
		s.cancelled++
		close(e.done)
		s.mu.Unlock()
		e.cancel(ErrTaskCancelled)
		return
	}
	s.mu.Unlock()
	// Already dispatched: ask it cooperatively to stop.
	e.cancel(ErrTaskCancelled)
}

// Close stops accepting new tasks. Already queued and running tasks are
// drained. Close is idempotent.
func (s *Scheduler) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cond.Broadcast()
		s.mu.Unlock()
	})
}

// Shutdown closes the scheduler and waits for every queued and running
// task to finish draining.
func (s *Scheduler) Shutdown() {
	s.Close()
	s.Wait()
}

// Wait blocks until Close has been called and all tasks have drained.
func (s *Scheduler) Wait() { s.wg.Wait() }

// Stats returns a snapshot of scheduler counters.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Submitted:    s.submitted,
		Succeeded:    s.succeeded,
		Failed:       s.failed,
		Cancelled:    s.cancelled,
		Inflight:     s.inflight,
		PeakInflight: s.maxInflight,
		Queued:       s.queued,
		Tenants:      len(s.tenants),
	}
}

func (s *Scheduler) dispatch() {
	defer s.wg.Done()
	s.mu.Lock()
	for {
		for s.queued == 0 {
			if s.closed {
				s.mu.Unlock()
				return
			}
			s.cond.Wait()
		}
		// Reserve one unit of global capacity before choosing a task.
		// Because the reservation happens before dequeue, the number of
		// dispatched (and therefore running) tasks can never exceed the
		// configured concurrency, even for a single heavyweight tenant.
		s.mu.Unlock()
		s.slots <- struct{}{}
		s.mu.Lock()

		if s.queued == 0 {
			// Every waiting task was cancelled while we reserved a slot.
			s.mu.Unlock()
			<-s.slots
			s.mu.Lock()
			continue
		}
		e := s.popNextLocked()
		if e == nil {
			// Unreachable for positive weights; stay safe anyway.
			s.mu.Unlock()
			<-s.slots
			s.mu.Lock()
			continue
		}
		t := s.tenants[e.tenant]
		e.queued = false
		// Attach the task to the per-tenant FIFO start chain.
		e.gate = t.gate
		e.next = make(chan struct{})
		t.gate = e.next

		s.queued--
		s.inflight++
		if s.inflight > s.maxInflight {
			s.maxInflight = s.inflight
		}
		if hook := s.onDispatch; hook != nil {
			hook(e)
		}
		s.wg.Add(1)
		s.mu.Unlock()

		go s.runTask(e)

		s.mu.Lock()
	}
}

// popNextLocked selects the next task with cursor based weighted round
// robin. The caller holds s.mu and must ensure s.queued > 0.
func (s *Scheduler) popNextLocked() *entry {
	n := len(s.order)
	for pass := 0; pass < 2; pass++ {
		for steps := 0; steps < n; steps++ {
			idx := (s.cursor + steps) % n
			t := s.tenants[s.order[idx]]
			if len(t.queue) == 0 {
				// Idle tenants bank no capacity; idle time is never
				// charged against anyone, so slots are never wasted.
				t.deficit = 0
				continue
			}
			if t.deficit <= 0 {
				t.deficit += t.weight
			}
			if t.deficit <= 0 {
				continue
			}
			e := t.queue[0]
			t.queue = t.queue[1:]
			t.deficit--
			if t.deficit <= 0 {
				t.deficit = 0
				s.cursor = (idx + 1) % n
			} else {
				s.cursor = idx
			}
			if len(t.queue) == 0 {
				t.deficit = 0
			}
			return e
		}
		s.cursor = 0
	}
	return nil
}

func (s *Scheduler) runTask(e *entry) {
	defer s.wg.Done()
	var slotReleased bool
	releaseSlot := func() {
		if !slotReleased {
			slotReleased = true
			<-s.slots
		}
	}
	defer func() {
		if r := recover(); r != nil {
			s.finish(e, TaskPanicError{Value: r})
		}
		// Release the follower (if any) and the global slot even on panic.
		select {
		case <-e.next:
			// already released (should not happen; guarded anyway)
		default:
			close(e.next)
		}
		releaseSlot()
	}()

	// Wait for the per-tenant turn so starts follow FIFO order.
	<-e.gate
	if hook := s.onStart; hook != nil {
		hook(e)
	}
	// Let the next same-tenant task start immediately; tasks may overlap.
	close(e.next)

	if context.Cause(e.ctx) == ErrTaskCancelled {
		s.finish(e, ErrTaskCancelled)
		return
	}
	err := e.fn(e.ctx)
	s.finish(e, err)
}

func (s *Scheduler) finish(e *entry, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.finished {
		return
	}
	e.finished = true
	switch {
	case err != nil && context.Cause(e.ctx) == ErrTaskCancelled:
		e.err = ErrTaskCancelled
		s.cancelled++
	case err != nil:
		e.err = err
		s.failed++
	default:
		s.succeeded++
	}
	s.inflight--
	close(e.done)
	// Wake the dispatcher: it may be waiting for new work and must
	// re-check the closed && queued == 0 termination condition.
	s.cond.Broadcast()
}
