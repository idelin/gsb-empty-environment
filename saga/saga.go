// Package saga implements long-lived transaction orchestration with reverse
// compensation (rollback) for failure recovery, idempotent replay, and
// final-consistency guarantees.
//
// State machine (per saga):
//
//	PENDING -> RUNNING -> COMPLETED
//	                   -> COMPENSATING -> COMPENSATED   (all compensations ok)
//	                                   -> FAILED        (a compensation failed)
//
// Per saga+step, Execute and Compensate each run at most once, no matter how
// many times Run is called (concurrently or sequentially) with the same saga
// ID. All state transitions happen under the saga's lock; no background
// goroutines are spawned, so the orchestrator is leak-free by construction.
package saga

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Status is the lifecycle state of a saga or a single step.
type Status string

const (
	StatusPending      Status = "PENDING"
	StatusRunning      Status = "RUNNING"
	StatusCompleted    Status = "COMPLETED"
	StatusCompensating Status = "COMPENSATING"
	StatusCompensated  Status = "COMPENSATED"
	StatusFailed       Status = "FAILED"
)

// terminal reports whether s is a terminal state.
func (s Status) terminal() bool {
	return s == StatusCompleted || s == StatusCompensated || s == StatusFailed
}

// Step is one unit of work with its reverse compensation.
type Step struct {
	Name       string
	Execute    func(ctx context.Context) error
	Compensate func(ctx context.Context) error // may be nil (nothing to undo)
}

// StepRecord is the persisted state of one step inside a saga.
type StepRecord struct {
	Name          string
	Status        Status
	Executions    int // times Execute completed successfully (idempotency: <= 1)
	Compensations int // times Compensate completed successfully (idempotency: <= 1)
}

// SagaRecord is the persisted state of one saga instance.
type SagaRecord struct {
	ID     string
	Status Status
	Steps  []*StepRecord
	Err    string
}

// sagaState couples the record with its serialization lock.
type sagaState struct {
	mu  sync.Mutex
	rec *SagaRecord
}

// Store is an in-memory, goroutine-safe saga state store.
type Store struct {
	mu    sync.Mutex
	sagas map[string]*sagaState
}

// NewStore creates an empty Store.
func NewStore() *Store { return &Store{sagas: make(map[string]*sagaState)} }

func (s *Store) loadOrCreate(id string, steps []Step) *sagaState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sagas[id]; ok {
		return st
	}
	rec := &SagaRecord{ID: id, Status: StatusPending, Steps: make([]*StepRecord, len(steps))}
	for i, st := range steps {
		rec.Steps[i] = &StepRecord{Name: st.Name, Status: StatusPending}
	}
	st := &sagaState{rec: rec}
	s.sagas[id] = st
	return st
}

// AssertConsistent verifies strict final consistency: every saga and every
// step is in a terminal state (nothing left running, pending or half
// compensated). It returns an error describing the first leak found.
func (s *Store) AssertConsistent() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.sagas {
		st.mu.Lock()
		if !st.rec.Status.terminal() {
			st.mu.Unlock()
			return fmt.Errorf("saga: leak detected, saga %q stuck in %s", id, st.rec.Status)
		}
		for _, sr := range st.rec.Steps {
			if !stepTerminal(sr.Status) {
				st.mu.Unlock()
				return fmt.Errorf("saga: leak detected, saga %q step %q stuck in %s", id, sr.Name, sr.Status)
			}
		}
		st.mu.Unlock()
	}
	return nil
}

func stepTerminal(s Status) bool {
	return s == StatusCompleted || s == StatusCompensated || s == StatusFailed
}

// Orchestrator runs sagas against a Store.
type Orchestrator struct {
	store *Store
}

// New creates an Orchestrator backed by a fresh in-memory Store.
func New() *Orchestrator { return &Orchestrator{store: NewStore()} }

// NewWithStore creates an Orchestrator over an existing Store.
func NewWithStore(store *Store) *Orchestrator { return &Orchestrator{store: store} }

// Record returns a snapshot of the persisted record for a saga ID.
func (o *Orchestrator) Record(id string) (SagaRecord, bool) {
	o.store.mu.Lock()
	st, ok := o.store.sagas[id]
	o.store.mu.Unlock()
	if !ok {
		return SagaRecord{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	cp := *st.rec
	cp.Steps = make([]*StepRecord, len(st.rec.Steps))
	for i, sr := range st.rec.Steps {
		c := *sr
		cp.Steps[i] = &c
	}
	return cp, true
}

// AssertConsistent delegates to the underlying store.
func (o *Orchestrator) AssertConsistent() error { return o.store.AssertConsistent() }

// Run executes the saga identified by sagaID. It is safe to call Run
// concurrently from many goroutines with the same sagaID: the saga body runs
// exactly once and every caller observes the same terminal result
// (idempotent replay). On step failure, previously completed steps are
// compensated in strict reverse order.
func (o *Orchestrator) Run(ctx context.Context, sagaID string, steps []Step) error {
	if sagaID == "" {
		return errors.New("saga: empty saga ID")
	}
	if len(steps) == 0 {
		return errors.New("saga: no steps")
	}
	st := o.store.loadOrCreate(sagaID, steps)

	// The per-saga lock serializes runners: exactly one goroutine drives the
	// state machine; later callers replay the recorded terminal result.
	st.mu.Lock()
	defer st.mu.Unlock()
	rec := st.rec

	switch rec.Status {
	case StatusCompleted:
		return nil
	case StatusCompensated, StatusFailed:
		return errors.New(rec.Err)
	}

	rec.Status = StatusRunning
	completed := 0
	var runErr error

	for i, step := range steps {
		sr := rec.Steps[i]
		if sr.Status == StatusCompleted { // resume: skip already-done steps
			completed = i + 1
			continue
		}
		if err := ctx.Err(); err != nil {
			runErr = err
			break
		}
		sr.Status = StatusRunning
		if err := safeCall(ctx, step.Execute); err != nil {
			runErr = fmt.Errorf("saga %q step %q: %w", sagaID, step.Name, err)
			sr.Status = StatusFailed
			break
		}
		sr.Status = StatusCompleted
		sr.Executions++
		completed = i + 1
	}

	if runErr == nil {
		rec.Status = StatusCompleted
		return nil
	}

	// Reverse compensation of completed steps, strictly LIFO.
	rec.Status = StatusCompensating
	var compErrs []string
	for i := completed - 1; i >= 0; i-- {
		sr := rec.Steps[i]
		if sr.Status != StatusCompleted {
			continue
		}
		if steps[i].Compensate == nil {
			sr.Status = StatusCompensated
			continue
		}
		sr.Status = StatusCompensating
		if err := safeCall(ctx, steps[i].Compensate); err != nil {
			sr.Status = StatusFailed
			compErrs = append(compErrs, fmt.Sprintf("compensate %q: %v", sr.Name, err))
			continue
		}
		sr.Status = StatusCompensated
		sr.Compensations++
	}

	if len(compErrs) > 0 {
		rec.Status = StatusFailed
		rec.Err = fmt.Sprintf("%v; compensation failures: %s", runErr, strings.Join(compErrs, "; "))
		return errors.New(rec.Err)
	}
	rec.Status = StatusCompensated
	rec.Err = runErr.Error()
	return runErr
}

// safeCall invokes fn, converting panics into errors so a faulty step never
// escapes the state machine or leaks a half-updated record.
func safeCall(ctx context.Context, fn func(context.Context) error) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(ctx)
}
