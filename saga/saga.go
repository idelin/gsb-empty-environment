// Package saga implements long-lived transaction orchestration with reverse
// compensation: steps run in order, and on failure the completed steps are
// compensated in exact reverse order. Execution is idempotent (replaying a
// transaction ID returns the stored outcome without re-running actions),
// concurrency-safe, and spawns no goroutines, so nothing leaks.
package saga

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Status is the lifecycle state of a transaction.
type Status int

const (
	StatusPending      Status = iota // registered, not started
	StatusRunning                    // actions executing
	StatusCompleted                  // all actions succeeded
	StatusCompensating               // a failure occurred, rollback in progress
	StatusCompensated                // rollback finished cleanly (terminal)
	StatusFailed                     // rollback itself hit errors (terminal)
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "Pending"
	case StatusRunning:
		return "Running"
	case StatusCompleted:
		return "Completed"
	case StatusCompensating:
		return "Compensating"
	case StatusCompensated:
		return "Compensated"
	case StatusFailed:
		return "Failed"
	}
	return "Unknown"
}

// Step is one unit of work with its inverse operation.
type Step struct {
	Name string
	// Action performs the step. Called at most once per transaction ID.
	Action func(ctx context.Context) error
	// Compensate undoes Action. Called at most once, only if Action
	// succeeded and a later step failed. May be nil (nothing to undo).
	Compensate func(ctx context.Context) error
}

// Record is the observable outcome of a transaction.
type Record struct {
	ID               string
	Status           Status
	StepsTotal       int
	StepsCompleted   int // actions that succeeded
	StepsCompensated int // compensations that ran
	Err              error
}

// execution tracks an in-flight run so concurrent callers with the same
// transaction ID wait and replay instead of double-executing.
type execution struct {
	done chan struct{}
}

// Coordinator executes and remembers transactions. Safe for concurrent use.
type Coordinator struct {
	mu       sync.Mutex
	records  map[string]*Record
	inflight map[string]*execution
}

// NewCoordinator returns an empty Coordinator.
func NewCoordinator() *Coordinator {
	return &Coordinator{
		records:  make(map[string]*Record),
		inflight: make(map[string]*execution),
	}
}

// Execute runs the transaction identified by id. If a transaction with the
// same ID already finished, its stored outcome is replayed (idempotency).
// If one is in flight, the caller blocks until it completes and replays the
// outcome. Otherwise the caller becomes the executor.
func (c *Coordinator) Execute(ctx context.Context, id string, steps []Step) error {
	c.mu.Lock()
	if rec, ok := c.records[id]; ok && isTerminal(rec.Status) {
		err := rec.Err
		c.mu.Unlock()
		return err
	}
	if ex, ok := c.inflight[id]; ok {
		c.mu.Unlock()
		select {
		case <-ex.done:
			c.mu.Lock()
			rec := c.records[id]
			c.mu.Unlock()
			if rec == nil {
				return fmt.Errorf("saga: tx %q finished without record", id)
			}
			return rec.Err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ex := &execution{done: make(chan struct{})}
	c.inflight[id] = ex
	rec := &Record{ID: id, Status: StatusRunning, StepsTotal: len(steps)}
	c.records[id] = rec
	c.mu.Unlock()

	err := c.run(ctx, rec, steps)

	c.mu.Lock()
	delete(c.inflight, id)
	rec.Err = err
	c.mu.Unlock()
	close(ex.done)
	return err
}

// run executes actions forward and, on failure, compensations in reverse.
func (c *Coordinator) run(ctx context.Context, rec *Record, steps []Step) error {
	for i, st := range steps {
		if err := ctx.Err(); err != nil {
			return c.rollback(rec, steps, i, fmt.Errorf("saga: tx %q aborted before step %q: %v", rec.ID, st.Name, err))
		}
		if st.Action == nil {
			return c.rollback(rec, steps, i, fmt.Errorf("saga: tx %q step %q has nil Action", rec.ID, st.Name))
		}
		if err := st.Action(ctx); err != nil {
			return c.rollback(rec, steps, i, fmt.Errorf("saga: tx %q step %q failed: %v", rec.ID, st.Name, err))
		}
		c.mu.Lock()
		rec.StepsCompleted = i + 1
		c.mu.Unlock()
	}
	c.mu.Lock()
	rec.Status = StatusCompleted
	c.mu.Unlock()
	return nil
}

// rollback compensates the first `upto` steps (indices 0..upto-1) in exact
// reverse order. Every compensation runs even if earlier ones fail, so the
// system converges to eventual consistency; compensation failures are
// aggregated into the returned error.
func (c *Coordinator) rollback(rec *Record, steps []Step, upto int, cause error) error {
	c.mu.Lock()
	rec.Status = StatusCompensating
	c.mu.Unlock()

	var compErrs []string
	for i := upto - 1; i >= 0; i-- {
		st := steps[i]
		if st.Compensate == nil {
			continue
		}
		if err := st.Compensate(context.Background()); err != nil {
			compErrs = append(compErrs, fmt.Sprintf("%q: %v", st.Name, err))
			continue
		}
		c.mu.Lock()
		rec.StepsCompensated++
		c.mu.Unlock()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(compErrs) > 0 {
		rec.Status = StatusFailed
		return fmt.Errorf("%v; rollback incomplete: %s", cause, strings.Join(compErrs, "; "))
	}
	rec.Status = StatusCompensated
	return cause
}

// Record returns a snapshot of a transaction's record, or nil if unknown.
func (c *Coordinator) Record(id string) *Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[id]
	if !ok {
		return nil
	}
	cp := *rec
	return &cp
}

// Forget drops the stored outcome of a terminal transaction, allowing its
// ID to be reused. Returns false if the ID is unknown or still in flight.
func (c *Coordinator) Forget(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, busy := c.inflight[id]; busy {
		return false
	}
	rec, ok := c.records[id]
	if !ok || !isTerminal(rec.Status) {
		return false
	}
	delete(c.records, id)
	return true
}

func isTerminal(s Status) bool {
	return s == StatusCompleted || s == StatusCompensated || s == StatusFailed
}
