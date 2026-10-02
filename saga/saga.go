// Package saga implements long-lived transaction orchestration with reverse
// compensation: each step carries an idempotent action and an idempotent
// compensation. When a step fails, completed steps are compensated in exact
// reverse order, driving the saga to a terminal state and preserving eventual
// consistency.
//
// State machine:
//
//	PENDING -> RUNNING -> COMPLETED
//	                   -> COMPENSATING -> COMPENSATED
//	                                   -> FAILED (compensation exhausted retries)
//
// Idempotency: every action/compensation executes under a durable idempotency
// key (sagaID/step/kind). A key is marked only after its effect succeeds, so
// retries are safe and re-running a finished saga is a no-op.
package saga

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status is the saga lifecycle state.
type Status string

const (
	StatusPending      Status = "PENDING"
	StatusRunning      Status = "RUNNING"
	StatusCompleted    Status = "COMPLETED"
	StatusCompensating Status = "COMPENSATING"
	StatusCompensated  Status = "COMPENSATED"
	StatusFailed       Status = "FAILED"
)

// ErrCompensationFailed is returned when rollback could not complete.
var ErrCompensationFailed = errors.New("saga: compensation failed")

// Step is one unit of work with its reverse compensation.
type Step struct {
	Name       string
	Action     func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

// Store durably records idempotency keys, saga states and the ordered list of
// completed steps per saga. The in-memory implementation is safe for
// concurrent use; a production deployment would back it with the replicated
// log from the paxos package.
type Store struct {
	mu       sync.Mutex
	executed map[string]bool
	status   map[string]Status
	history  map[string][]Status
	done     map[string][]string // sagaID -> completed step names, in order
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{
		executed: make(map[string]bool),
		status:   make(map[string]Status),
		history:  make(map[string][]Status),
		done:     make(map[string][]string),
	}
}

// IsExecuted reports whether the idempotency key has completed.
func (s *Store) IsExecuted(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executed[key]
}

// markExecuted records a completed idempotency key.
func (s *Store) markExecuted(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executed[key] = true
}

func (s *Store) setStatus(id string, st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[id] = st
	s.history[id] = append(s.history[id], st)
}

// Status returns the current state of a saga (PENDING if unknown).
func (s *Store) Status(id string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.status[id]; ok {
		return st
	}
	return StatusPending
}

// History returns the ordered state transitions of a saga.
func (s *Store) History(id string) []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, len(s.history[id]))
	copy(out, s.history[id])
	return out
}

func (s *Store) recordDone(id, step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done[id] = append(s.done[id], step)
}

// CompletedSteps returns the successfully executed step names, in order.
func (s *Store) CompletedSteps(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.done[id]))
	copy(out, s.done[id])
	return out
}

// Orchestrator runs sagas with retries, reverse compensation and
// per-saga serialization. It is safe for concurrent use.
type Orchestrator struct {
	store   *Store
	retries int           // max attempts per action/compensation
	backoff time.Duration // base backoff between attempts (doubled each retry)
	now     func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Option configures an Orchestrator.
type Option func(*Orchestrator)

// WithRetries sets the max attempts per step (default 3).
func WithRetries(n int) Option {
	return func(o *Orchestrator) {
		if n > 0 {
			o.retries = n
		}
	}
}

// WithBackoff sets the base backoff between attempts (default 5ms).
func WithBackoff(d time.Duration) Option {
	return func(o *Orchestrator) { o.backoff = d }
}

// NewOrchestrator builds an Orchestrator on top of store.
func NewOrchestrator(store *Store, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		store:   store,
		retries: 3,
		backoff: 5 * time.Millisecond,
		now:     time.Now,
		locks:   make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *Orchestrator) lockFor(id string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	l, ok := o.locks[id]
	if !ok {
		l = &sync.Mutex{}
		o.locks[id] = l
	}
	return l
}

// Run executes the saga identified by id. Concurrent or repeated Run calls
// with the same id are serialized and idempotent: already-executed steps are
// skipped, so a crashed saga can be resumed by simply calling Run again.
func (o *Orchestrator) Run(ctx context.Context, id string, steps []Step) error {
	l := o.lockFor(id)
	l.Lock()
	defer l.Unlock()

	switch o.store.Status(id) {
	case StatusCompleted, StatusCompensated:
		return nil // terminal success states: nothing to do
	}

	o.store.setStatus(id, StatusRunning)
	completed := o.store.CompletedSteps(id)

	for i, st := range steps {
		if err := ctx.Err(); err != nil {
			o.store.setStatus(id, StatusCompensating)
			return o.rollback(ctx, id, steps, completed, err)
		}
		key := id + "/" + st.Name + "/action"
		if o.store.IsExecuted(key) {
			if i >= len(completed) {
				completed = append(completed, st.Name)
			}
			continue // idempotent resume: effect already applied
		}
		if st.Action == nil {
			o.store.markExecuted(key)
			o.store.recordDone(id, st.Name)
			completed = append(completed, st.Name)
			continue
		}
		if err := o.execWithRetry(ctx, key, st.Action); err != nil {
			o.store.setStatus(id, StatusCompensating)
			return o.rollback(ctx, id, steps, completed, err)
		}
		o.store.recordDone(id, st.Name)
		completed = append(completed, st.Name)
	}

	o.store.setStatus(id, StatusCompleted)
	return nil
}

// rollback compensates completed steps in exact reverse order.
func (o *Orchestrator) rollback(ctx context.Context, id string, steps []Step, completed []string, cause error) error {
	byName := make(map[string]Step, len(steps))
	for _, st := range steps {
		byName[st.Name] = st
	}
	for i := len(completed) - 1; i >= 0; i-- {
		st := byName[completed[i]]
		if st.Compensate == nil {
			continue
		}
		key := id + "/" + st.Name + "/compensate"
		if o.store.IsExecuted(key) {
			continue
		}
		if err := o.execWithRetry(ctx, key, st.Compensate); err != nil {
			o.store.setStatus(id, StatusFailed)
			return fmt.Errorf("%w: step %q: %v (cause: %v)", ErrCompensationFailed, st.Name, err, cause)
		}
	}
	o.store.setStatus(id, StatusCompensated)
	return fmt.Errorf("saga %q rolled back: %w", id, cause)
}

// execWithRetry runs fn until success or the retry budget is exhausted,
// marking the idempotency key only after a successful effect.
func (o *Orchestrator) execWithRetry(ctx context.Context, key string, fn func(context.Context) error) error {
	var err error
	backoff := o.backoff
	for attempt := 1; attempt <= o.retries; attempt++ {
		if err = fn(ctx); err == nil {
			o.store.markExecuted(key)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt < o.retries {
			t := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			backoff *= 2
		}
	}
	return err
}
