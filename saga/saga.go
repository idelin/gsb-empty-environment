// Package saga implements long-lived transaction orchestration with reverse
// compensation: when a step fails, previously completed steps are undone in
// reverse order. Execution is idempotent (re-running the same transaction ID
// resumes instead of duplicating side effects), compensation is retried for
// eventual consistency, and all state transitions are concurrency-safe.
package saga

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Status is the lifecycle state of a transaction.
type Status int

const (
	StatusPending           Status = iota // created, nothing executed yet
	StatusRunning                         // forward steps in progress
	StatusCompleted                       // all steps applied
	StatusCompensating                    // rolling back completed steps
	StatusRolledBack                      // compensation finished; no side effects remain
	StatusCompensationStuck               // compensation exhausted retries; needs Resume
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
	case StatusRolledBack:
		return "RolledBack"
	case StatusCompensationStuck:
		return "CompensationStuck"
	default:
		return "Unknown"
	}
}

// Step is one unit of work plus its reverse compensation.
type Step struct {
	Name string
	// Action applies the step's side effect. It must be safe to call more
	// than once for the same transaction (idempotent) because Execute may be
	// retried after a crash.
	Action func(ctx context.Context) error
	// Compensate undoes Action. It is retried until it succeeds (bounded by
	// MaxCompensateRetries per Execute/Resume call) to guarantee eventual
	// consistency.
	Compensate func(ctx context.Context) error
}

// Options tunes orchestration behaviour.
type Options struct {
	// MaxCompensateRetries bounds compensation attempts per Execute/Resume
	// call before the transaction is marked CompensationStuck. Default 3.
	MaxCompensateRetries int
	// CompensateBackoff is the delay between compensation retries.
	// Default 10ms.
	CompensateBackoff time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxCompensateRetries <= 0 {
		o.MaxCompensateRetries = 3
	}
	if o.CompensateBackoff <= 0 {
		o.CompensateBackoff = 10 * time.Millisecond
	}
	return o
}

// txState is the durable record of one transaction.
type txState struct {
	mu       sync.Mutex
	steps    []Step
	status   Status
	done     []bool // done[i]: step i's action succeeded
	compDone []bool // compDone[i]: step i's compensation succeeded
	err      error  // the forward failure that triggered rollback
}

// Orchestrator manages transactions keyed by ID.
type Orchestrator struct {
	mu   sync.Mutex
	txs  map[string]*txState
	opts Options
}

// New creates an Orchestrator.
func New(opts Options) *Orchestrator {
	return &Orchestrator{txs: make(map[string]*txState), opts: opts.withDefaults()}
}

func (o *Orchestrator) getOrCreate(id string, steps []Step) *txState {
	o.mu.Lock()
	defer o.mu.Unlock()
	st, ok := o.txs[id]
	if !ok {
		st = &txState{
			steps:    append([]Step(nil), steps...),
			status:   StatusPending,
			done:     make([]bool, len(steps)),
			compDone: make([]bool, len(steps)),
		}
		o.txs[id] = st
	}
	return st
}

// Status returns the current status of a transaction.
func (o *Orchestrator) Status(id string) (Status, bool) {
	o.mu.Lock()
	st, ok := o.txs[id]
	o.mu.Unlock()
	if !ok {
		return StatusPending, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.status, true
}

// Execute runs the transaction's steps in order. On failure of step i it
// compensates steps i-1 .. 0 in reverse order. Re-calling Execute with the
// same id is idempotent: a completed transaction returns nil, a rolled-back
// one returns the original error, and a partially executed one resumes from
// the first incomplete step without re-running completed steps.
func (o *Orchestrator) Execute(ctx context.Context, id string, steps []Step) error {
	st := o.getOrCreate(id, steps)
	st.mu.Lock()
	defer st.mu.Unlock()

	switch st.status {
	case StatusCompleted:
		return nil
	case StatusRolledBack:
		return st.err
	case StatusCompensationStuck:
		return fmt.Errorf("saga: transaction %q compensation stuck: %v", id, st.err)
	}
	st.status = StatusRunning

	for i := range st.steps {
		if st.done[i] {
			continue
		}
		if err := st.steps[i].Action(ctx); err != nil {
			st.err = err
			st.compensateLocked(ctx, i-1, o.opts)
			if st.status == StatusCompensationStuck {
				return fmt.Errorf("saga: step %q failed (%v) and compensation is stuck", st.steps[i].Name, err)
			}
			return fmt.Errorf("saga: step %q failed, rolled back: %v", st.steps[i].Name, err)
		}
		st.done[i] = true
	}
	st.status = StatusCompleted
	return nil
}

// compensateLocked undoes steps from..0 in reverse order. Steps never
// completed are skipped; steps already compensated are not repeated.
func (st *txState) compensateLocked(ctx context.Context, from int, opts Options) {
	st.status = StatusCompensating
	for i := from; i >= 0; i-- {
		if !st.done[i] || st.compDone[i] {
			continue
		}
		attempts := 0
		for {
			if err := st.steps[i].Compensate(ctx); err == nil {
				st.compDone[i] = true
				break
			}
			attempts++
			if attempts >= opts.MaxCompensateRetries {
				st.status = StatusCompensationStuck
				return
			}
			select {
			case <-ctx.Done():
				st.status = StatusCompensationStuck
				return
			case <-time.After(opts.CompensateBackoff):
			}
		}
	}
	st.status = StatusRolledBack
}

// Resume continues a CompensationStuck transaction, retrying the remaining
// compensations. It is a no-op (returns nil) for transactions in any other
// state.
func (o *Orchestrator) Resume(ctx context.Context, id string) error {
	o.mu.Lock()
	st, ok := o.txs[id]
	o.mu.Unlock()
	if !ok {
		return fmt.Errorf("saga: unknown transaction %q", id)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.status != StatusCompensationStuck {
		return nil
	}
	// find the highest uncompensated completed step
	from := -1
	for i := len(st.steps) - 1; i >= 0; i-- {
		if st.done[i] && !st.compDone[i] {
			from = i
			break
		}
	}
	if from == -1 {
		st.status = StatusRolledBack
		return nil
	}
	st.compensateLocked(ctx, from, o.opts)
	if st.status == StatusCompensationStuck {
		return fmt.Errorf("saga: transaction %q still stuck", id)
	}
	return nil
}

// Progress reports per-step forward/compensation completion for diagnostics.
func (o *Orchestrator) Progress(id string) (done []bool, compensated []bool, ok bool) {
	o.mu.Lock()
	st, found := o.txs[id]
	o.mu.Unlock()
	if !found {
		return nil, nil, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]bool(nil), st.done...), append([]bool(nil), st.compDone...), true
}
