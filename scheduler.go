// Package dagsched runs a DAG of independent build tasks.
//
// Semantics:
//   - A task runs only after all of its dependencies have succeeded.
//   - A failed task skips exactly its transitive dependents; sibling
//     branches keep running.
//   - Resource requests are granted all-or-nothing: a task starts only
//     when its full request can be satisfied, and holds the grant until
//     it finishes.
//   - Cancellation stops admitting new tasks and waits for already
//     started tasks to clean up before Run returns.
package dagsched

import (
	"context"
	"fmt"
)

// ResourceMap names resource quantities, e.g. {"cpu": 2, "memMB": 512}.
type ResourceMap map[string]int64

// State is the lifecycle state of a task.
type State int

const (
	StatePending State = iota
	StateRunning
	StateSucceeded
	StateFailed
	StateSkipped
	StateCanceled
)

var stateNames = map[State]string{
	StatePending:   "pending",
	StateRunning:   "running",
	StateSucceeded: "succeeded",
	StateFailed:    "failed",
	StateSkipped:   "skipped",
	StateCanceled:  "canceled",
}

func (s State) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("state(%d)", int(s))
}

// Terminal reports whether the state is final.
func (s State) Terminal() bool { return s >= StateSucceeded }

// Result is the terminal outcome of a task.
type Result struct {
	State State
	Err   error // nil unless Failed, Skipped, or Canceled
}

// Task is a unit of work in the DAG.
type Task struct {
	ID    string
	Deps  []string
	Needs ResourceMap
	Run   func(ctx context.Context) error
}

// DepError explains that Task was skipped because Dep did not succeed.
// Unwrap returns the dependency's own error, so the full reason chain
// from a skipped leaf back to the root failure is inspectable with
// errors.Is / errors.As.
type DepError struct {
	Task     string
	Dep      string
	DepState State
	Err      error
}

func (e *DepError) Error() string {
	return fmt.Sprintf("task %q skipped: dependency %q %s: %v", e.Task, e.Dep, e.DepState, e.Err)
}

func (e *DepError) Unwrap() error { return e.Err }

// completion carries a finished task's outcome back to the run loop.
type completion struct {
	id  string
	err error
}

// Scheduler runs a fixed set of tasks against a resource capacity.
type Scheduler struct {
	tasks    map[string]*Task
	order    []string // registration order, for deterministic dispatch
	capacity ResourceMap
}

// NewScheduler returns a Scheduler with the given total resource capacity.
func NewScheduler(capacity ResourceMap) *Scheduler {
	capCopy := make(ResourceMap, len(capacity))
	for name, qty := range capacity {
		capCopy[name] = qty
	}
	return &Scheduler{tasks: make(map[string]*Task), capacity: capCopy}
}

// Add registers a task. Resource requests are validated against capacity
// here; dependency edges are validated in Run.
func (s *Scheduler) Add(t Task) error {
	if t.ID == "" {
		return fmt.Errorf("dagsched: task ID must not be empty")
	}
	if t.Run == nil {
		return fmt.Errorf("dagsched: task %q has nil Run", t.ID)
	}
	if _, dup := s.tasks[t.ID]; dup {
		return fmt.Errorf("dagsched: duplicate task ID %q", t.ID)
	}
	for name, qty := range t.Needs {
		if qty < 0 {
			return fmt.Errorf("dagsched: task %q requests negative quantity of %q", t.ID, name)
		}
		capQty, ok := s.capacity[name]
		if !ok {
			return fmt.Errorf("dagsched: task %q requests unknown resource %q", t.ID, name)
		}
		if qty > capQty {
			return fmt.Errorf("dagsched: task %q requests %d of %q, capacity is %d", t.ID, qty, name, capQty)
		}
	}
	task := t
	s.tasks[t.ID] = &task
	s.order = append(s.order, t.ID)
	return nil
}

// Run executes the DAG and returns the terminal result of every task.
// It returns a non-nil error only for validation failures or cancellation
// (ctx.Err()); individual task failures are reported per-task in Results.
func (s *Scheduler) Run(ctx context.Context) (map[string]Result, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}

	n := len(s.tasks)
	results := make(map[string]Result, n)
	remaining := make(map[string]int, n) // unmet dependency count
	dependents := make(map[string][]string, n)
	for _, id := range s.order {
		t := s.tasks[id]
		remaining[id] = len(t.Deps)
		for _, dep := range t.Deps {
			dependents[dep] = append(dependents[dep], id)
		}
	}

	avail := make(ResourceMap, len(s.capacity))
	for name, qty := range s.capacity {
		avail[name] = qty
	}

	var ready []string
	for _, id := range s.order {
		if remaining[id] == 0 {
			ready = append(ready, id)
		}
	}

	done := make(chan completion)
	runningSet := make(map[string]bool, n)
	running := 0
	canceled := false
	ctxDone := ctx.Done()

	// skip marks a task terminal and cascades StateSkipped to every
	// transitive dependent, threading the reason chain through DepError.
	var skip func(id string, res Result)
	skip = func(id string, res Result) {
		if _, ok := results[id]; ok {
			return
		}
		results[id] = res
		for _, dep := range dependents[id] {
			skip(dep, Result{
				State: StateSkipped,
				Err:   &DepError{Task: dep, Dep: id, DepState: res.State, Err: res.Err},
			})
		}
	}

	for len(results) < n {
		if !canceled {
			var waiting []string
			for _, id := range ready {
				t := s.tasks[id]
				if !fits(t.Needs, avail) {
					waiting = append(waiting, id)
					continue
				}
				grant(avail, t.Needs)
				runningSet[id] = true
				running++
				go runTask(ctx, t, done)
			}
			ready = waiting
		}

		if running == 0 && len(ready) > 0 && !canceled {
			// Unreachable: with nothing running, avail == capacity and
			// every request was validated to fit within capacity.
			return nil, fmt.Errorf("dagsched: scheduler stalled with %d ready tasks", len(ready))
		}

		var doneCh chan completion
		if running > 0 {
			doneCh = done
		}
		select {
		case c := <-doneCh:
			running--
			delete(runningSet, c.id)
			release(avail, s.tasks[c.id].Needs)
			if c.err == nil {
				results[c.id] = Result{State: StateSucceeded}
				for _, dep := range dependents[c.id] {
					remaining[dep]--
					if remaining[dep] == 0 {
						ready = append(ready, dep)
					}
				}
			} else {
				skip(c.id, Result{
					State: StateFailed,
					Err:   fmt.Errorf("task %q failed: %w", c.id, c.err),
				})
			}
		case <-ctxDone:
			canceled = true
			ctxDone = nil
			// Admit no new work; every task not yet started is canceled.
			for _, id := range s.order {
				if _, ok := results[id]; ok || runningSet[id] {
					continue
				}
				results[id] = Result{
					State: StateCanceled,
					Err:   fmt.Errorf("task %q canceled: %w", id, ctx.Err()),
				}
			}
			ready = nil
		}
	}

	if canceled {
		return results, ctx.Err()
	}
	return results, nil
}

// validate checks dependency edges and acyclicity.
func (s *Scheduler) validate() error {
	for _, id := range s.order {
		for _, dep := range s.tasks[id].Deps {
			if dep == id {
				return fmt.Errorf("dagsched: task %q depends on itself", id)
			}
			if _, ok := s.tasks[dep]; !ok {
				return fmt.Errorf("dagsched: task %q depends on unknown task %q", id, dep)
			}
		}
	}
	const (
		white = iota // unvisited
		gray         // on DFS stack
		black        // done
	)
	color := make(map[string]int, len(s.tasks))
	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		for _, dep := range s.tasks[id].Deps {
			switch color[dep] {
			case gray:
				return fmt.Errorf("dagsched: dependency cycle involving %q and %q", id, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for _, id := range s.order {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// runTask executes one task, converting panics into errors.
func runTask(ctx context.Context, t *Task, done chan<- completion) {
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		err = t.Run(ctx)
	}()
	done <- completion{id: t.ID, err: err}
}

// fits reports whether need can be fully satisfied from avail.
func fits(need, avail ResourceMap) bool {
	for name, qty := range need {
		if avail[name] < qty {
			return false
		}
	}
	return true
}

// grant deducts need from avail. Call only when fits(need, avail).
func grant(avail, need ResourceMap) {
	for name, qty := range need {
		avail[name] -= qty
	}
}

// release returns a grant to avail.
func release(avail, need ResourceMap) {
	for name, qty := range need {
		avail[name] += qty
	}
}
