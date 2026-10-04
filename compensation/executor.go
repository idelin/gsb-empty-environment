package compensation

import (
	"context"
)

// Executor runs the steps of a dependency graph and, on failure,
// produces a resumable compensation plan covering every completed
// step — ancestors of the failed step as well as completed parallel
// branches.
type Executor struct {
	graph *Graph
	retry RetryPolicy
	sleep Sleeper
}

// Option configures an Executor.
type Option func(*Executor)

// WithRetryPolicy sets the retry policy used by generated plans.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(e *Executor) { e.retry = p }
}

// WithSleeper overrides the backoff sleeper (mainly for tests).
func WithSleeper(s Sleeper) Option {
	return func(e *Executor) { e.sleep = s }
}

// NewExecutor creates an Executor for g.
func NewExecutor(g *Graph, opts ...Option) *Executor {
	e := &Executor{graph: g, sleep: DefaultSleeper}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Result is the outcome of a forward execution.
type Result struct {
	// Completed lists step IDs that ran successfully, in execution order.
	Completed []string
	// FailedStep is the step whose Run returned RunErr, if any.
	FailedStep string
	// RunErr is the forward execution or graph validation error.
	RunErr error
	// Plan compensates all completed steps. It is nil when the forward
	// execution succeeded or no step had completed before the failure.
	Plan *Plan
}

// Execute runs all steps in topological order. When a step fails, it
// builds a compensation plan over every completed step (ancestors and
// parallel branches alike), ordered in reverse dependency order so
// dependents are compensated before their dependencies.
func (e *Executor) Execute(ctx context.Context) *Result {
	order, err := e.graph.topoOrder()
	if err != nil {
		return &Result{RunErr: err}
	}
	var completed []*Step
	for _, s := range order {
		if s.Run != nil {
			if err := s.Run(ctx); err != nil {
				return &Result{
					Completed:  stepIDs(completed),
					FailedStep: s.ID,
					RunErr:     err,
					Plan:       e.buildPlan(completed),
				}
			}
		}
		completed = append(completed, s)
	}
	return &Result{Completed: stepIDs(completed)}
}

// buildPlan creates a plan over the completed steps in reverse
// execution order. Because execution order is topological, reversing
// it guarantees dependents are compensated before their dependencies.
func (e *Executor) buildPlan(completed []*Step) *Plan {
	entries := make([]*Entry, 0, len(completed))
	registry := HandlerRegistry{}
	for i := len(completed) - 1; i >= 0; i-- {
		s := completed[i]
		entries = append(entries, &Entry{
			StepID:        s.ID,
			Compensatable: s.Compensatable,
			Status:        EntryPending,
		})
		if s.Compensate != nil {
			registry[s.ID] = s.Compensate
		}
	}
	return newPlan(entries, registry, e.retry, e.sleep)
}

func stepIDs(steps []*Step) []string {
	ids := make([]string, len(steps))
	for i, s := range steps {
		ids[i] = s.ID
	}
	return ids
}
