package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// Driver executes single page-table operations against the real debugger
// host. Implementations must be atomic with respect to one call: a returned
// error means the operation did not take effect. If the host may be in an
// unknown state after an error, the caller must reconcile the real page table
// with Engine.State before resuming.
type Driver interface {
	Map(v VirtPage, p PhysPage, perm Perm) error
	Unmap(v VirtPage) error
	Reprotect(v VirtPage, perm Perm) error
}

// StepFailure records the step at which a driver call failed.
type StepFailure struct {
	Step  Step
	Cause error
}

func (f *StepFailure) Error() string {
	return fmt.Sprintf("netsec: driver failed at %s: %v", f.Step, f.Cause)
}

func (f *StepFailure) Unwrap() error { return f.Cause }

// Job records a transaction and, after a driver failure, its recovery plans.
// A job is pending while ResumePlan or RollbackPlan is non-nil.
type Job struct {
	// Plan is the original full step plan.
	Plan *Plan
	// Failure is the most recent driver failure, or nil once resolved.
	Failure *StepFailure
	// ResumePlan holds the remaining forward steps from the last known good
	// state to the original target. Its first step is the failed step, which
	// must be retried.
	ResumePlan *Plan
	// RollbackPlan holds the steps from the last known good state back to the
	// state before the original transaction.
	RollbackPlan *Plan
}

// Pending reports whether a driver failure still has to be resolved.
func (j *Job) Pending() bool {
	return j.ResumePlan != nil || j.RollbackPlan != nil
}

// Result is returned by every engine entry point.
type Result struct {
	// Applied counts steps successfully applied during the call.
	Applied int
	// Failure is the first driver failure, or nil on success.
	Failure *StepFailure
	// Job describes recovery options when Failure is non-nil.
	Job *Job
}

// Engine tracks the authoritative mapping state and drives a Driver. The
// engine state only advances after a driver call succeeds, so after any
// failure the tracked state is the last known good state and is guaranteed to
// satisfy the invariant.
type Engine struct {
	mu     sync.Mutex
	driver Driver
	state  *State
	job    *Job
}

// NewEngine starts an engine with no mappings.
func NewEngine(driver Driver) *Engine {
	return &Engine{driver: driver, state: NewState()}
}

// NewEngineWithState starts an engine from an existing invariant-safe state.
func NewEngineWithState(driver Driver, initial *State) (*Engine, error) {
	if initial == nil {
		return nil, errors.New("netsec: nil initial state")
	}
	if err := initial.CheckInvariant(); err != nil {
		return nil, fmt.Errorf("netsec: initial state violates invariant: %w", err)
	}
	return &Engine{driver: driver, state: initial.Clone()}, nil
}

// State returns a copy of the authoritative last known good state.
func (e *Engine) State() *State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.Clone()
}

// PendingJob returns the active failed/in-flight job, or nil.
func (e *Engine) PendingJob() *Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.job
}

// applyStep performs one driver call and, on success, updates the tracked
// state. The post-update state is re-checked defensively; a violation means
// the driver lied about the operation and the state change is rolled back in
// the engine bookkeeping so the exposed state never violates the invariant.
func (e *Engine) applyStep(step Step) error {
	op := step.Op
	var err error
	switch op.Kind {
	case OpMap:
		err = e.driver.Map(op.Virt, op.Phys, op.Perm)
	case OpUnmap:
		err = e.driver.Unmap(op.Virt)
	case OpReprotect:
		err = e.driver.Reprotect(op.Virt, op.Perm)
	default:
		return fmt.Errorf("netsec: unknown operation kind %d", op.Kind)
	}
	if err != nil {
		return err
	}

	prev := e.state.Clone()
	switch op.Kind {
	case OpMap:
		e.state.Mappings[op.Virt] = Mapping{Virt: op.Virt, Phys: op.Phys, Perm: op.Perm}
	case OpUnmap:
		delete(e.state.Mappings, op.Virt)
	case OpReprotect:
		m := e.state.Mappings[op.Virt]
		m.Perm = op.Perm
		e.state.Mappings[op.Virt] = m
	}
	if invErr := e.state.CheckInvariant(); invErr != nil {
		e.state = prev
		return fmt.Errorf("netsec: driver step %s produced violating state: %w", step, invErr)
	}
	return nil
}

// runPlan executes a plan from its first step and returns the number of steps
// applied and the failure, if any. On failure it rebuilds the job's recovery
// plans relative to the current last known good state.
func (e *Engine) runPlan(plan *Plan, job *Job, origin *State) (int, *StepFailure) {
	applied := 0
	for _, step := range plan.Steps {
		if err := e.applyStep(step); err != nil {
			job.Failure = &StepFailure{Step: step, Cause: err}

			current := e.state.Clone()
			target := plan.To.Clone()
			resumePlan, rerr := PlanStateDiff(current, target)
			if rerr != nil {
				// Cannot happen while both states satisfy the invariant; keep
				// a defensive empty plan rather than panicking.
				resumePlan = &Plan{From: current, To: target}
			}
			rollbackPlan, berr := PlanStateDiff(current, origin.Clone())
			if berr != nil {
				rollbackPlan = &Plan{From: current, To: origin.Clone()}
			}
			job.ResumePlan = resumePlan
			job.RollbackPlan = rollbackPlan
			return applied, job.Failure
		}
		applied++
	}
	return applied, nil
}

// Apply compiles and executes a transaction. A failed transaction leaves a
// pending job that must be resolved with Resume or Rollback before another
// transaction can be applied.
func (e *Engine) Apply(txn Txn) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.job != nil && e.job.Pending() {
		return nil, errors.New("netsec: previous transaction is still pending; call Resume or Rollback")
	}

	plan, err := PlanTxn(e.state, txn)
	if err != nil {
		return nil, err
	}
	job := &Job{Plan: plan}
	e.job = job
	if plan.IsEmpty() {
		return &Result{Applied: 0, Job: job}, nil
	}

	applied, failure := e.runPlan(plan, job, plan.From)
	if failure != nil {
		return &Result{Applied: applied, Failure: job.Failure, Job: job}, nil
	}
	return &Result{Applied: applied, Job: job}, nil
}

// Resume retries the failed step and continues toward the original target.
func (e *Engine) Resume() (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.job == nil {
		return nil, errors.New("netsec: no active transaction")
	}
	job := e.job
	if !job.Pending() {
		return &Result{Applied: 0, Job: job}, nil
	}

	plan := job.ResumePlan
	origin := job.Plan.From
	applied, failure := e.runPlan(plan, job, origin)
	if failure != nil {
		return &Result{Applied: applied, Failure: job.Failure, Job: job}, nil
	}

	// Forward recovery reached the target; the original logical job is done.
	job.Failure = nil
	job.ResumePlan = nil
	job.RollbackPlan = nil
	return &Result{Applied: applied, Job: job}, nil
}

// Rollback abandons the target and returns to the state before the original
// transaction, again applying downgrades before upgrades.
func (e *Engine) Rollback() (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.job == nil {
		return nil, errors.New("netsec: no active transaction")
	}
	job := e.job
	if !job.Pending() {
		return &Result{Applied: 0, Job: job}, nil
	}

	plan := job.RollbackPlan
	if plan == nil {
		return nil, errors.New("netsec: no rollback plan available")
	}
	applied, failure := e.runPlan(plan, job, job.Plan.From)
	if failure != nil {
		return &Result{Applied: applied, Failure: job.Failure, Job: job}, nil
	}

	// Back at the original state: close the job.
	job.Failure = nil
	job.ResumePlan = nil
	job.RollbackPlan = nil
	return &Result{Applied: applied, Job: job}, nil
}
