// Package saga implements a transaction orchestration engine for
// long-lived workflows with reverse compensation (rollback) semantics.
//
// A workflow is an ordered list of steps. Each step has a forward Action
// and a backward Compensate. Steps are executed sequentially; when a step
// fails (or the context is cancelled), every already-completed step is
// compensated in strict reverse order, restoring eventual consistency.
//
// The orchestrator guarantees:
//   - Idempotency: a transaction ID executes its actions at most once,
//     no matter how many times Run is called (sequentially or concurrently).
//   - At-most-once compensation: each compensation runs at most once.
//   - Concurrency safety: all state is mutex-protected; duplicate Run
//     calls for the same transaction wait for the in-flight execution and
//     observe the same terminal result.
//   - No goroutine leaks: the engine spawns no background goroutines.
package saga

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Status is the lifecycle state of a transaction.
type Status int

const (
	StatusRunning      Status = iota // steps are being executed
	StatusCommitted                  // all steps completed successfully
	StatusCompensating               // a failure occurred; rollback in progress
	StatusRolledBack                 // rollback finished; state is consistent
	StatusFailed                     // rollback itself failed; manual repair needed
)

// String renders a human-readable status name.
func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusCommitted:
		return "COMMITTED"
	case StatusCompensating:
		return "COMPENSATING"
	case StatusRolledBack:
		return "ROLLED_BACK"
	case StatusFailed:
		return "FAILED"
	}
	return "UNKNOWN"
}

// ErrEmptyTransaction is returned for an empty transaction ID.
var ErrEmptyTransaction = errors.New("saga: empty transaction id")

// Step is one unit of work plus its inverse.
type Step struct {
	Name string
	// Action performs the forward operation. It must be safe to call once.
	Action func(ctx context.Context) error
	// Compensate undoes Action. It is invoked at most once, only if Action
	// previously succeeded, and must itself be idempotent-safe.
	Compensate func(ctx context.Context) error
}

// EventKind classifies audit log entries.
type EventKind int

const (
	EventActionOK EventKind = iota
	EventActionFailed
	EventCompensateOK
	EventCompensateFailed
	EventCommitted
	EventRolledBack
	EventRollbackFailed
)

// Event is one audit record emitted during a transaction's lifecycle.
type Event struct {
	TxID  string
	Step  string
	Kind  EventKind
	Err   error
	At    time.Time
	Order int // monotonically increasing per orchestrator
}

// txState tracks one in-flight or finished transaction.
type txState struct {
	status    Status
	err       error
	completed []int // indexes of steps whose Action succeeded
	done      chan struct{}
}

// Orchestrator executes a fixed workflow definition for many transactions.
// It is safe for concurrent use.
type Orchestrator struct {
	steps []Step

	mu       sync.Mutex
	txs      map[string]*txState
	acted    map[string]bool // idempotency keys: txID/step -> action done
	comped   map[string]bool // idempotency keys: txID/step -> compensation done
	events   []Event
	eventSeq int
}

// New builds an Orchestrator for the given ordered workflow.
func New(steps ...Step) *Orchestrator {
	cp := make([]Step, len(steps))
	copy(cp, steps)
	return &Orchestrator{
		steps:  cp,
		txs:    make(map[string]*txState),
		acted:  make(map[string]bool),
		comped: make(map[string]bool),
	}
}

// key builds the idempotency key for one step of one transaction.
func key(txID string, idx int) string { return fmt.Sprintf("%s#%d", txID, idx) }

// Run executes the workflow for txID. It is idempotent: calling Run again
// with the same txID (even concurrently) never re-executes actions and
// returns the original outcome.
func (o *Orchestrator) Run(ctx context.Context, txID string) error {
	if txID == "" {
		return ErrEmptyTransaction
	}

	o.mu.Lock()
	if st, ok := o.txs[txID]; ok {
		done := st.done
		o.mu.Unlock()
		<-done // wait for the in-flight execution, then report its result
		o.mu.Lock()
		defer o.mu.Unlock()
		return st.err
	}
	st := &txState{status: StatusRunning, done: make(chan struct{})}
	o.txs[txID] = st
	o.mu.Unlock()

	err := o.execute(ctx, txID, st)

	o.mu.Lock()
	st.err = err
	close(st.done)
	o.mu.Unlock()
	return err
}

// execute runs the forward path and, on failure, the reverse compensation.
// Callers hold no lock; state mutations go through o.mu.
func (o *Orchestrator) execute(ctx context.Context, txID string, st *txState) error {
	for i, step := range o.steps {
		if err := ctx.Err(); err != nil {
			o.rollback(ctx, txID, st)
			return fmt.Errorf("saga: tx %s aborted before step %q: %w", txID, step.Name, err)
		}
		if o.markActed(txID, i) {
			// Action already ran in a previous attempt: treat as success.
			o.recordCompleted(st, i)
			continue
		}
		if err := step.Action(ctx); err != nil {
			o.emit(Event{TxID: txID, Step: step.Name, Kind: EventActionFailed, Err: err})
			o.rollback(ctx, txID, st)
			return fmt.Errorf("saga: tx %s failed at step %q: %w", txID, step.Name, err)
		}
		o.emit(Event{TxID: txID, Step: step.Name, Kind: EventActionOK})
		o.recordCompleted(st, i)
	}
	o.setStatus(st, StatusCommitted)
	o.emit(Event{TxID: txID, Kind: EventCommitted})
	return nil
}

// rollback compensates every completed step in strict reverse order.
// Compensation failures do not stop the remaining compensations; they are
// aggregated and flip the transaction to StatusFailed.
func (o *Orchestrator) rollback(ctx context.Context, txID string, st *txState) {
	o.setStatus(st, StatusCompensating)

	o.mu.Lock()
	completed := make([]int, len(st.completed))
	copy(completed, st.completed)
	o.mu.Unlock()

	var compErrs []string
	for i := len(completed) - 1; i >= 0; i-- {
		idx := completed[i]
		step := o.steps[idx]
		if step.Compensate == nil {
			continue
		}
		if o.markComped(txID, idx) {
			continue // already compensated in a previous attempt
		}
		if err := step.Compensate(ctx); err != nil {
			compErrs = append(compErrs, fmt.Sprintf("%s: %v", step.Name, err))
			o.emit(Event{TxID: txID, Step: step.Name, Kind: EventCompensateFailed, Err: err})
			continue
		}
		o.emit(Event{TxID: txID, Step: step.Name, Kind: EventCompensateOK})
	}

	if len(compErrs) > 0 {
		o.setStatus(st, StatusFailed)
		o.emit(Event{TxID: txID, Kind: EventRollbackFailed, Err: errors.New(strings.Join(compErrs, "; "))})
		return
	}
	o.setStatus(st, StatusRolledBack)
	o.emit(Event{TxID: txID, Kind: EventRolledBack})
}

// markActed atomically records that step idx of txID has executed.
// It reports whether the key was already present.
func (o *Orchestrator) markActed(txID string, idx int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	k := key(txID, idx)
	if o.acted[k] {
		return true
	}
	o.acted[k] = true
	return false
}

// markComped atomically records that step idx of txID has been compensated.
func (o *Orchestrator) markComped(txID string, idx int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	k := key(txID, idx)
	if o.comped[k] {
		return true
	}
	o.comped[k] = true
	return false
}

// recordCompleted appends idx to the transaction's completed list.
func (o *Orchestrator) recordCompleted(st *txState, idx int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	st.completed = append(st.completed, idx)
}

// setStatus updates the transaction status under lock.
func (o *Orchestrator) setStatus(st *txState, s Status) {
	o.mu.Lock()
	defer o.mu.Unlock()
	st.status = s
}

// emit appends an audit event under lock.
func (o *Orchestrator) emit(e Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e.At = time.Now()
	e.Order = o.eventSeq
	o.eventSeq++
	o.events = append(o.events, e)
}

// Status reports the current status of txID.
func (o *Orchestrator) Status(txID string) (Status, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	st, ok := o.txs[txID]
	if !ok {
		return 0, false
	}
	return st.status, true
}

// Events returns a snapshot of the audit log.
func (o *Orchestrator) Events() []Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Event, len(o.events))
	copy(out, o.events)
	return out
}

// TxCount reports how many transactions the orchestrator has seen.
func (o *Orchestrator) TxCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.txs)
}
