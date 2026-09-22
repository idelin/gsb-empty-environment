// Package scheduler provides a multi-tenant task scheduler.
//
// The scheduler enforces:
//
//   - Per-tenant FIFO ordering (a tenant's tasks start in submission order).
//   - A single global concurrency limit shared by all tenants.
//   - Weighted fair sharing of execution slots across saturated tenants
//     (long-run service counts are proportional to tenant weights).
//   - Work-conserving dispatch: idle tenants never hold unused slots.
//   - Dynamic weight updates that only influence subsequent selections.
//   - Clean shutdown: after Shutdown no new tasks are accepted, while tasks
//     already queued or running are drained.
//
// All synchronization uses only the Go standard library: a single dispatcher
// goroutine owns all mutable state and communicates through channels, which
// keeps the design free of data races (verified with `go test -race`).
package scheduler

import (
	"context"
	"errors"
	"sort"
)

// Errors returned by the scheduler.
var (
	// ErrInvalidConcurrency is returned when the concurrency limit is not positive.
	ErrInvalidConcurrency = errors.New("scheduler: concurrency must be positive")
	// ErrInvalidWeight is returned when a tenant weight is not positive.
	ErrInvalidWeight = errors.New("scheduler: weight must be positive")
	// ErrTenantNotFound is returned for an operation on an unknown tenant.
	ErrTenantNotFound = errors.New("scheduler: tenant not found")
	// ErrTenantExists is returned when adding a tenant that already exists.
	ErrTenantExists = errors.New("scheduler: tenant already exists")
	// ErrClosed is returned when submitting to (or reconfiguring) a shut-down scheduler.
	ErrClosed = errors.New("scheduler: scheduler is closed")
)

// TaskFunc is a unit of work. The returned error is supplied by the caller
// (and therefore fully injectable from tests) and later exposed by Task.Wait.
type TaskFunc func(ctx context.Context) error

// Task represents a submitted task.
type Task struct {
	tenant   string
	fn       TaskFunc
	done     chan struct{}
	err      error
	canceled bool
}

// Done returns a channel closed once the task has finished or was canceled
// while waiting.
func (t *Task) Done() <-chan struct{} { return t.done }

// Wait blocks until the task completes (or is canceled while waiting) and
// returns the error produced by its TaskFunc. A canceled waiting task returns
// nil; inspect IsCanceled to distinguish that case.
func (t *Task) Wait() error {
	<-t.done
	return t.err
}

// IsCanceled reports whether the task was removed from its tenant's waiting
// queue before it ever started executing.
func (t *Task) IsCanceled() bool { return t.canceled }

// TenantStats is a point-in-time snapshot for one tenant.
type TenantStats struct {
	ID      string
	Weight  int
	Served  int64 // number of tasks that have started executing
	Waiting int   // number of tasks currently queued
}

// Scheduler schedules tasks across tenants under a global concurrency limit.
type Scheduler struct {
	concurrency int
	events      chan any
	drained     chan struct{}
	finalStats  []TenantStats
}

type tenant struct {
	id     string
	weight int
	served int64
	queue  []*Task
}

type dstate struct {
	tenants     map[string]*tenant
	completions chan *Task
}

// pickTenant selects the non-empty tenant with the smallest normalized service
// counter (served / weight), breaking ties by tenant ID. Returning nil means no
// queued work exists. Weights are read live, so updating a weight affects only
// subsequent picks.
func (d *dstate) pickTenant() *tenant {
	var best *tenant
	ids := make([]string, 0, len(d.tenants))
	for id := range d.tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := d.tenants[id]
		if len(t.queue) == 0 {
			continue
		}
		if best == nil || t.served*int64(best.weight) < best.served*int64(t.weight) {
			best = t
		}
	}
	return best
}

func (t *tenant) dequeue() *Task {
	task := t.queue[0]
	t.queue[0] = nil
	t.queue = t.queue[1:]
	return task
}

func (d *dstate) snapshot() []TenantStats {
	ids := make([]string, 0, len(d.tenants))
	for id := range d.tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	snap := make([]TenantStats, 0, len(ids))
	for _, id := range ids {
		t := d.tenants[id]
		snap = append(snap, TenantStats{
			ID:      id,
			Weight:  t.weight,
			Served:  t.served,
			Waiting: len(t.queue),
		})
	}
	return snap
}

func (d *dstate) allQueuesEmpty() bool {
	for _, t := range d.tenants {
		if len(t.queue) > 0 {
			return false
		}
	}
	return true
}

// Event messages sent to the dispatcher goroutine. Reply channels are buffered
// so the dispatcher never blocks even if the caller gave up.
type addTenantEvent struct {
	id     string
	weight int
	reply  chan error
}

type setWeightEvent struct {
	id     string
	weight int
	reply  chan error
}

type submitEvent struct {
	tenant string
	task   *Task
	reply  chan error
}

type cancelEvent struct {
	task  *Task
	reply chan bool
}

type statsEvent struct {
	reply chan []TenantStats
}

type shutdownEvent struct {
	ack chan struct{}
}

// New creates a Scheduler allowing at most concurrency tasks to run at once.
// The initial tenants map maps tenant ID to its positive weight.
func New(concurrency int, tenants map[string]int) (*Scheduler, error) {
	if concurrency <= 0 {
		return nil, ErrInvalidConcurrency
	}
	ids := make([]string, 0, len(tenants))
	for id := range tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if tenants[id] <= 0 {
			return nil, ErrInvalidWeight
		}
	}
	s := &Scheduler{
		concurrency: concurrency,
		events:      make(chan any, concurrency),
		drained:     make(chan struct{}),
	}
	go s.run(tenants, ids)
	return s, nil
}

func (s *Scheduler) run(initial map[string]int, ids []string) {
	d := &dstate{
		tenants:     make(map[string]*tenant, len(initial)),
		completions: make(chan *Task, s.concurrency),
	}
	for _, id := range ids {
		d.tenants[id] = &tenant{id: id, weight: initial[id]}
	}

	running := 0
	closed := false
	var shutdownAcks []chan struct{}

	// pump starts as many queued tasks as free slots allow.
	pump := func() {
		for running < s.concurrency {
			t := d.pickTenant()
			if t == nil {
				return
			}
			task := t.dequeue()
			t.served++
			running++
			go s.execute(task, d.completions)
		}
	}

	for {
		pump()
		if closed && running == 0 && d.allQueuesEmpty() {
			s.finalStats = d.snapshot()
			for _, ack := range shutdownAcks {
				close(ack)
			}
			close(s.drained)
			return
		}

		select {
		case ev := <-s.events:
			switch e := ev.(type) {
			case addTenantEvent:
				if closed {
					e.reply <- ErrClosed
				} else if d.tenants[e.id] != nil {
					e.reply <- ErrTenantExists
				} else if e.weight <= 0 {
					e.reply <- ErrInvalidWeight
				} else {
					d.tenants[e.id] = &tenant{id: e.id, weight: e.weight}
					e.reply <- nil
				}
			case setWeightEvent:
				t := d.tenants[e.id]
				switch {
				case closed:
					e.reply <- ErrClosed
				case t == nil:
					e.reply <- ErrTenantNotFound
				case e.weight <= 0:
					e.reply <- ErrInvalidWeight
				default:
					t.weight = e.weight
					e.reply <- nil
				}
			case submitEvent:
				t := d.tenants[e.tenant]
				switch {
				case closed:
					e.reply <- ErrClosed
				case t == nil:
					e.reply <- ErrTenantNotFound
				default:
					t.queue = append(t.queue, e.task)
					e.reply <- nil
				}
			case cancelEvent:
				removed := false
				if t := d.tenants[e.task.tenant]; t != nil {
					for i, qt := range t.queue {
						if qt == e.task {
							t.queue = append(t.queue[:i], t.queue[i+1:]...)
							removed = true
							break
						}
					}
				}
				// A waiting task is dropped without ever occupying a slot,
				// so it cannot consume any execution quota.
				if removed {
					e.task.canceled = true
					close(e.task.done)
				}
				e.reply <- removed
			case statsEvent:
				e.reply <- d.snapshot()
			case shutdownEvent:
				if !closed {
					closed = true
				}
				shutdownAcks = append(shutdownAcks, e.ack)
			}
		case task := <-d.completions:
			// Finishing (success or failure) releases the global slot.
			_ = task
			running--
		}
	}
}

func (s *Scheduler) execute(task *Task, completions chan<- *Task) {
	task.err = task.fn(context.Background())
	close(task.done)
	completions <- task
}

// AddTenant registers a new tenant with the given positive weight.
func (s *Scheduler) AddTenant(id string, weight int) error {
	reply := make(chan error, 1)
	select {
	case s.events <- addTenantEvent{id: id, weight: weight, reply: reply}:
	case <-s.drained:
		return ErrClosed
	}
	select {
	case err := <-reply:
		return err
	case <-s.drained:
		return ErrClosed
	}
}

// SetWeight changes a tenant's weight. The new weight only affects subsequent
// scheduling decisions.
func (s *Scheduler) SetWeight(id string, weight int) error {
	reply := make(chan error, 1)
	select {
	case s.events <- setWeightEvent{id: id, weight: weight, reply: reply}:
	case <-s.drained:
		return ErrClosed
	}
	select {
	case err := <-reply:
		return err
	case <-s.drained:
		return ErrClosed
	}
}

// Submit enqueues fn for the given tenant. Submission is FIFO per tenant and
// does not block on available slots.
func (s *Scheduler) Submit(tenantID string, fn TaskFunc) (*Task, error) {
	task := &Task{tenant: tenantID, fn: fn, done: make(chan struct{})}
	reply := make(chan error, 1)
	select {
	case s.events <- submitEvent{tenant: tenantID, task: task, reply: reply}:
	case <-s.drained:
		return nil, ErrClosed
	}
	select {
	case err := <-reply:
		if err != nil {
			return nil, err
		}
		return task, nil
	case <-s.drained:
		return nil, ErrClosed
	}
}

// Cancel removes a task that is still waiting in its tenant's queue. It returns
// true if the task was canceled before starting (no execution slot/quota was
// consumed) and false if the task is already running, already finished, or
// unknown. Running tasks are never interrupted.
func (s *Scheduler) Cancel(task *Task) bool {
	reply := make(chan bool, 1)
	select {
	case s.events <- cancelEvent{task: task, reply: reply}:
	case <-s.drained:
		return false
	}
	select {
	case removed := <-reply:
		return removed
	case <-s.drained:
		return false
	}
}

// Stats returns a snapshot of all tenants, ordered by tenant ID.
func (s *Scheduler) Stats() ([]TenantStats, error) {
	reply := make(chan []TenantStats, 1)
	select {
	case s.events <- statsEvent{reply: reply}:
	case <-s.drained:
		return s.finalStats, nil
	}
	select {
	case snap := <-reply:
		return snap, nil
	case <-s.drained:
		return s.finalStats, nil
	}
}

// Shutdown stops accepting new tasks and drains all queued and running tasks.
// It blocks until draining completes, the context expires, or the scheduler
// has already drained. Shutdown is safe to call multiple times.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	ack := make(chan struct{})
	select {
	case s.events <- shutdownEvent{ack: ack}:
	case <-s.drained:
		return nil
	}
	select {
	case <-ack:
		return nil
	case <-s.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
