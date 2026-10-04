// Package saga orchestrates long-lived flows as a sequence of steps with
// reverse-order compensating actions, guaranteeing final consistency and
// idempotent execution.
//
// State machine:
//
//	Pending -> Running -> Completed            (all steps succeeded)
//	                   -> Compensating -> Compensated  (rolled back cleanly)
//	                                 -> Failed        (compensation gave up)
//
// Guarantees:
//   - Exactly-once step execution per Saga instance, even under concurrent
//     Run callers (idempotency): every caller observes the same terminal
//     state and result.
//   - On any step failure, previously executed steps are compensated in
//     strict reverse order.
//   - Compensation is retried with backoff and always runs on a background
//     context, so caller cancellation cannot abort the rollback.
//   - Terminal states are final: a finished Saga never re-executes.
package saga

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status is the lifecycle state of a Saga.
type Status int

const (
	StatusPending Status = iota
	StatusRunning
	StatusCompleted
	StatusCompensating
	StatusCompensated
	StatusFailed
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

// Terminal reports whether the status is a final state.
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusCompensated || s == StatusFailed
}

// ErrNilAction marks a step whose Action func is nil.
var ErrNilAction = errors.New("saga: step action is nil")

// Step is one unit of work plus its compensating (undo) action.
type Step struct {
	Name       string
	Action     func(ctx context.Context) error
	Compensate func(ctx context.Context) error // nil means "nothing to undo"
}

// Transition records one state change of the orchestrator.
type Transition struct {
	From, To Status
	Reason   string
	At       time.Time
}

// StepRecord is the execution journal of a single step.
type StepRecord struct {
	Name            string
	ActionCalls     int
	ActionErr       error
	CompensateCalls int
	Compensated     bool
	CompensateErr   error // non-nil if compensation gave up
}

// Saga orchestrates a fixed list of steps with reverse compensation.
type Saga struct {
	mu      sync.Mutex
	changed *sync.Cond

	status       Status
	steps        []Step
	records      []StepRecord
	executed     int // length of the successfully executed action prefix
	transitions  []Transition
	result       error
	compensating bool // rollback in progress (guards against re-entry)

	// MaxCompensateRetries bounds compensation attempts per step (default 3).
	MaxCompensateRetries int
	// Backoff, if set, returns the sleep before compensation attempt n (n>=1).
	Backoff func(attempt int) time.Duration
}

// New builds a Saga for the given steps.
func New(steps ...Step) *Saga {
	s := &Saga{
		status:               StatusPending,
		steps:                steps,
		records:              make([]StepRecord, len(steps)),
		MaxCompensateRetries: 3,
	}
	for i, st := range steps {
		s.records[i].Name = st.Name
	}
	s.changed = sync.NewCond(&s.mu)
	return s
}

// Status returns the current lifecycle state.
func (s *Saga) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Transitions returns a copy of the state transition journal.
func (s *Saga) Transitions() []Transition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Transition, len(s.transitions))
	copy(out, s.transitions)
	return out
}

// Records returns a copy of the per-step execution journal.
func (s *Saga) Records() []StepRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StepRecord, len(s.records))
	copy(out, s.records)
	return out
}

// Uncompensated lists the names of steps that executed but could not be
// rolled back (non-empty only in StatusFailed).
func (s *Saga) Uncompensated() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for i := 0; i < s.executed && i < len(s.records); i++ {
		if !s.records[i].Compensated {
			out = append(out, s.records[i].Name)
		}
	}
	return out
}

func (s *Saga) transitionLocked(to Status, reason string) {
	s.transitions = append(s.transitions, Transition{
		From: s.status, To: to, Reason: reason, At: time.Now(),
	})
	s.status = to
	s.changed.Broadcast()
}

// Run executes the saga to a terminal state and returns its result:
// nil on Completed, the rollback-wrapped cause on Compensated, and a
// compensation-incomplete error on Failed. Run is idempotent and safe for
// concurrent use: steps execute exactly once and every caller observes the
// same terminal result.
func (s *Saga) Run(ctx context.Context) error {
	s.mu.Lock()
	first := s.status == StatusPending
	if first {
		s.transitionLocked(StatusRunning, "start")
	}
	for !s.status.Terminal() {
		if first {
			s.mu.Unlock()
			s.execute(ctx)
			s.mu.Lock()
			first = false
		} else {
			s.changed.Wait()
		}
	}
	defer s.mu.Unlock()
	return s.result
}

// execute runs the action prefix; the caller must not hold s.mu.
func (s *Saga) execute(ctx context.Context) {
	failIdx := -1
	var failErr error
	for i, st := range s.steps {
		if st.Action == nil {
			s.mu.Lock()
			s.records[i].ActionErr = ErrNilAction
			s.mu.Unlock()
			failIdx, failErr = i, ErrNilAction
			break
		}
		s.mu.Lock()
		s.records[i].ActionCalls++
		s.mu.Unlock()
		if err := st.Action(ctx); err != nil {
			s.mu.Lock()
			s.records[i].ActionErr = err
			s.mu.Unlock()
			failIdx, failErr = i, err
			break
		}
		s.mu.Lock()
		s.executed = i + 1
		s.mu.Unlock()
	}

	s.mu.Lock()
	if failIdx < 0 {
		s.result = nil
		s.transitionLocked(StatusCompleted, "all steps succeeded")
		s.mu.Unlock()
		return
	}
	if s.compensating {
		// Defensive: rollback must never run twice.
		s.mu.Unlock()
		return
	}
	s.compensating = true
	s.transitionLocked(StatusCompensating,
		fmt.Sprintf("step %q failed: %v", s.steps[failIdx].Name, failErr))
	s.mu.Unlock()

	s.compensate(failErr)
}

// compensate rolls back the executed prefix in strict reverse order.
func (s *Saga) compensate(cause error) {
	// Rollback must survive caller cancellation: never use the caller's ctx.
	cctx := context.Background()

	s.mu.Lock()
	n := s.executed
	s.mu.Unlock()

	allOK := true
	for i := n - 1; i >= 0; i-- {
		st := s.steps[i]
		if st.Compensate == nil {
			s.mu.Lock()
			s.records[i].Compensated = true // nothing to undo
			s.mu.Unlock()
			continue
		}
		attempts := s.MaxCompensateRetries
		if attempts < 1 {
			attempts = 1
		}
		var err error
		for a := 0; a < attempts; a++ {
			if a > 0 && s.Backoff != nil {
				time.Sleep(s.Backoff(a))
			}
			s.mu.Lock()
			s.records[i].CompensateCalls++
			s.mu.Unlock()
			if err = st.Compensate(cctx); err == nil {
				break
			}
		}
		s.mu.Lock()
		if err != nil {
			s.records[i].CompensateErr = err
			allOK = false
		} else {
			s.records[i].Compensated = true
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if allOK {
		s.result = fmt.Errorf("saga rolled back after step failure: %v", cause)
		s.transitionLocked(StatusCompensated, "reverse compensation completed")
	} else {
		s.result = fmt.Errorf("saga compensation incomplete after step failure: %v", cause)
		s.transitionLocked(StatusFailed, "compensation exhausted retries")
	}
}
