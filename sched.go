// Package sched runs a DAG of independent tasks with all-or-nothing
// resource admission. A failed task only skips its transitive
// dependents; unrelated branches keep running. Cancellation stops new
// admissions and waits for already-started tasks to clean up.
package sched

import (
	"context"
	"fmt"
	"sort"
)

// Resources names countable capacities, e.g. {"cpu": 4, "mem": 8}.
type Resources map[string]int64

func cloneResources(r Resources) Resources {
	out := make(Resources, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

func fits(avail, need Resources) bool {
	for k, v := range need {
		if avail[k] < v {
			return false
		}
	}
	return true
}

func addTo(dst, src Resources) {
	for k, v := range src {
		dst[k] += v
	}
}

func subFrom(dst, src Resources) {
	for k, v := range src {
		dst[k] -= v
	}
}

// State is the final, terminal state of a task.
type State int

const (
	StatePending State = iota
	StateRunning
	StateSucceeded
	StateFailed
	StateSkipped
	StateCanceled
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateRunning:
		return "running"
	case StateSucceeded:
		return "succeeded"
	case StateFailed:
		return "failed"
	case StateSkipped:
		return "skipped"
	case StateCanceled:
		return "canceled"
	}
	return "unknown"
}

// Outcome is the deterministic terminal result of a task.
type Outcome struct {
	State State
	Err   error // failure reason for Failed, *DepError chain for Skipped
}

// DepError explains why a task was skipped: a dependency did not
// succeed. Cause chains to the dependency's own error, so the full
// reason chain leads back to the root failure.
type DepError struct {
	Task string // skipped task
	Dep  string // dependency that did not succeed
	Err  error  // the dependency's error
}

func (e *DepError) Error() string {
	return fmt.Sprintf("task %q skipped: dependency %q did not succeed", e.Task, e.Dep)
}

// Cause returns the dependency's error.
func (e *DepError) Cause() error { return e.Err }

// Chain flattens a reason chain, outermost skip first, root cause last.
func Chain(err error) []string {
	var out []string
	for err != nil {
		out = append(out, err.Error())
		causer, ok := err.(interface{ Cause() error })
		if !ok {
			break
		}
		err = causer.Cause()
	}
	return out
}

// RootCause unwraps a reason chain down to the originating error.
func RootCause(err error) error {
	for {
		causer, ok := err.(interface{ Cause() error })
		if !ok {
			return err
		}
		err = causer.Cause()
	}
}

// Task is one node in the DAG.
type Task struct {
	ID    string
	Deps  []string
	Needs Resources
	Run   func(ctx context.Context) error
}

// Scheduler admits tasks only when their full resource request can be
// granted. A Scheduler is single-use: call Run once.
type Scheduler struct {
	capacity Resources
	tasks    map[string]*Task
}

// New returns a Scheduler with the given total resource capacity.
func New(capacity Resources) *Scheduler {
	return &Scheduler{capacity: cloneResources(capacity), tasks: map[string]*Task{}}
}

// Add registers a task. It rejects duplicates, nil Run funcs, and
// requests that can never fit in the total capacity.
func (s *Scheduler) Add(t Task) error {
	if t.ID == "" {
		return fmt.Errorf("sched: task with empty ID")
	}
	if _, dup := s.tasks[t.ID]; dup {
		return fmt.Errorf("sched: duplicate task %q", t.ID)
	}
	if t.Run == nil {
		return fmt.Errorf("sched: task %q has nil Run", t.ID)
	}
	for name, q := range t.Needs {
		if q < 0 {
			return fmt.Errorf("sched: task %q requests negative %s", t.ID, name)
		}
		if q > s.capacity[name] {
			return fmt.Errorf("sched: task %q requests %d %s, capacity is %d", t.ID, q, name, s.capacity[name])
		}
	}
	cp := t
	cp.Deps = append([]string(nil), t.Deps...)
	cp.Needs = cloneResources(t.Needs)
	s.tasks[t.ID] = &cp
	return nil
}

func (s *Scheduler) sortedIDs() []string {
	ids := make([]string, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func dedupSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	n := 0
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			out[n] = v
			n++
		}
	}
	return out[:n]
}

// graph builds indegree and children maps in deterministic order.
func (s *Scheduler) graph() (indeg map[string]int, children map[string][]string) {
	indeg = make(map[string]int, len(s.tasks))
	children = make(map[string][]string, len(s.tasks))
	for _, id := range s.sortedIDs() {
		deps := dedupSorted(s.tasks[id].Deps)
		indeg[id] = len(deps)
		for _, d := range deps {
			children[d] = append(children[d], id)
		}
	}
	return indeg, children
}

func (s *Scheduler) validate() error {
	for _, id := range s.sortedIDs() {
		for _, d := range s.tasks[id].Deps {
			if d == id {
				return fmt.Errorf("sched: task %q depends on itself", id)
			}
			if _, ok := s.tasks[d]; !ok {
				return fmt.Errorf("sched: task %q depends on unknown task %q", id, d)
			}
		}
	}
	indeg, children := s.graph()
	var queue []string
	for _, id := range s.sortedIDs() {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	processed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processed++
		for _, ch := range children[id] {
			indeg[ch]--
			if indeg[ch] == 0 {
				queue = append(queue, ch)
			}
		}
	}
	if processed < len(s.tasks) {
		for _, id := range s.sortedIDs() {
			if indeg[id] > 0 {
				return fmt.Errorf("sched: dependency cycle involving task %q", id)
			}
		}
	}
	return nil
}

// Run executes the DAG and returns the terminal outcome of every task.
// It returns a non-nil error only if the graph itself is invalid.
//
// Admission is all-or-nothing and deterministic: ready tasks are
// considered in sorted ID order and start only if their full request
// fits in currently free capacity. If ctx is canceled, no new task
// starts, pending tasks become StateCanceled, and Run blocks until
// already-started tasks return (their cleanup) before returning.
func (s *Scheduler) Run(ctx context.Context) (map[string]Outcome, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	outcomes := make(map[string]Outcome, len(s.tasks))
	indeg, children := s.graph()
	avail := cloneResources(s.capacity)

	type completion struct {
		id  string
		err error
	}
	done := make(chan completion, len(s.tasks))
	runningSet := make(map[string]bool, len(s.tasks))
	running := 0
	unfinished := len(s.tasks)
	canceled := false

	var ready []string
	for _, id := range s.sortedIDs() {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}

	finish := func(id string, o Outcome) {
		outcomes[id] = o
		unfinished--
	}

	var skipSubtree func(id string, cause error)
	skipSubtree = func(id string, cause error) {
		if _, ok := outcomes[id]; ok {
			return
		}
		finish(id, Outcome{State: StateSkipped, Err: cause})
		for _, ch := range children[id] {
			skipSubtree(ch, &DepError{Task: ch, Dep: id, Err: cause})
		}
	}

	for unfinished > 0 {
		if !canceled {
			select {
			case <-ctx.Done():
				canceled = true
			default:
			}
		}
		if canceled {
			// Cancel everything that has not started; keep waiting
			// for running tasks to clean up.
			for _, id := range s.sortedIDs() {
				if _, ok := outcomes[id]; !ok && !runningSet[id] {
					finish(id, Outcome{State: StateCanceled})
				}
			}
			ready = nil
		} else {
			kept := ready[:0]
			for _, id := range ready {
				t := s.tasks[id]
				if fits(avail, t.Needs) {
					subFrom(avail, t.Needs)
					running++
					runningSet[id] = true
					go func(t *Task) {
						done <- completion{t.ID, t.Run(ctx)}
					}(t)
				} else {
					kept = append(kept, id)
				}
			}
			ready = kept
		}
		if unfinished == 0 {
			break
		}
		// Progress is guaranteed: with no cycle, some unfinished task
		// is ready, and free capacity only grows while nothing runs.
		c := <-done
		running--
		delete(runningSet, c.id)
		t := s.tasks[c.id]
		addTo(avail, t.Needs)
		if c.err == nil {
			finish(c.id, Outcome{State: StateSucceeded})
			for _, ch := range children[c.id] {
				if _, ok := outcomes[ch]; ok {
					continue // already skipped via another dependency
				}
				indeg[ch]--
				if indeg[ch] == 0 {
					ready = append(ready, ch)
					sort.Strings(ready)
				}
			}
		} else {
			finish(c.id, Outcome{State: StateFailed, Err: c.err})
			for _, ch := range children[c.id] {
				skipSubtree(ch, &DepError{Task: ch, Dep: c.id, Err: c.err})
			}
		}
	}
	return outcomes, nil
}
