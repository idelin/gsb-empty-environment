// Package dag implements a concurrent task scheduler over a dependency graph.
//
// Scheduling rules:
//   - A task starts only after all of its dependencies have succeeded.
//   - A failed (or blocked) dependency marks the dependent task as Blocked;
//     blocking propagates transitively through the graph.
//   - Branches unrelated to a failure keep running concurrently.
//   - The graph is checked for missing dependencies and cycles before any
//     task runs. A cycle is reported as a *CycleError containing the node
//     chain that closes the loop, and Run never blocks waiting in that case.
//   - Dependencies may be added dynamically (before Run, or concurrently
//     while Run is in progress) under restricted rules: both nodes must be
//     registered, the source task must not have started yet, and the new
//     edge must not introduce a cycle.
package dag

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Status is the lifecycle state of a task.
type Status int

const (
	Pending Status = iota
	Running
	Succeeded
	Failed
	Blocked
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "PENDING"
	case Running:
		return "RUNNING"
	case Succeeded:
		return "SUCCEEDED"
	case Failed:
		return "FAILED"
	case Blocked:
		return "BLOCKED"
	default:
		return "UNKNOWN"
	}
}

// TaskFunc is a unit of work. A non-nil error means failure; a panic is
// recovered and converted into a failure.
type TaskFunc func(ctx context.Context) error

// Sentinel errors, usable with errors.Is.
var (
	// ErrTaskExists is returned when a task id is registered twice.
	ErrTaskExists = errors.New("dag: task already exists")
	// ErrNotFound is returned when a referenced task id is unknown.
	ErrNotFound = errors.New("dag: task not found")
	// ErrSchedulerStarted is returned when the graph is modified after Run.
	ErrSchedulerStarted = errors.New("dag: scheduler has already started")
	// ErrTaskNotPending is returned when an edge is added to a task that has
	// already started or reached a terminal state.
	ErrTaskNotPending = errors.New("dag: dependency can only be added to a task that has not started")
	// ErrMissingDependency is returned when a dependency id was never added.
	ErrMissingDependency = errors.New("dag: task references an undeclared dependency")
)

// CycleError describes a directed cycle found in the graph. Chain lists the
// nodes along the cycle in dependency order, with the first node repeated at
// the end, e.g. [a b c a].
type CycleError struct {
	Chain []string
}

func (e *CycleError) Error() string {
	return "dag: cycle detected: " + strings.Join(e.Chain, " -> ")
}

type task struct {
	id     string
	fn     TaskFunc
	deps   []string
	hasDep map[string]struct{}
	status Status
	err    error
}

// Scheduler executes registered tasks according to their dependencies.
// The zero value is not usable; create one with New.
type Scheduler struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tasks   map[string]*task
	order   []string
	started bool
	running sync.WaitGroup
}

// New creates an empty Scheduler.
func New() *Scheduler {
	s := &Scheduler{tasks: make(map[string]*task)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Add registers a task. deps are the ids that must succeed before this task
// runs; dependency ids may be registered later as long as they exist by Run.
// Adding a task after Run has started returns ErrSchedulerStarted.
func (s *Scheduler) Add(id string, fn TaskFunc, deps ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("%w: cannot add %q", ErrSchedulerStarted, id)
	}
	if _, ok := s.tasks[id]; ok {
		return fmt.Errorf("%w: %q", ErrTaskExists, id)
	}
	t := &task{id: id, fn: fn, hasDep: make(map[string]struct{})}
	for _, d := range deps {
		if _, ok := t.hasDep[d]; !ok {
			t.deps = append(t.deps, d)
			t.hasDep[d] = struct{}{}
		}
	}
	s.tasks[id] = t
	s.order = append(s.order, id)
	return nil
}

// AddDependency adds an edge meaning "from depends on to": to must succeed
// before from is allowed to run.
//
// Restricted rules:
//   - both nodes must already be registered (ErrNotFound otherwise);
//   - from must not have started and must not be in a terminal state
//     (ErrTaskNotPending otherwise);
//   - the new edge must not close a cycle (*CycleError otherwise);
//   - a duplicate edge is a no-op returning nil.
//
// It may be called before Run or from another goroutine while Run is
// executing. In the latter case from must still be Pending.
func (s *Scheduler) AddDependency(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.tasks[from]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, from)
	}
	if _, ok := s.tasks[to]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, to)
	}
	if src.status != Pending {
		return fmt.Errorf("%w: %q is %s", ErrTaskNotPending, from, src.status)
	}
	if _, dup := src.hasDep[to]; dup {
		return nil
	}
	if cyc := s.findCycleLocked(from, to); cyc != nil {
		return cyc
	}
	src.deps = append(src.deps, to)
	src.hasDep[to] = struct{}{}
	s.cond.Broadcast()
	return nil
}

// findCycleLocked returns a cycle as a node chain, or nil if the graph is
// acyclic. When virtualFrom/virtualTo are non-empty, a hypothetical edge
// "virtualFrom depends on virtualTo" is included in the check.
func (s *Scheduler) findCycleLocked(virtualFrom, virtualTo string) *CycleError {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(s.order))
	var stack []string

	// nextLocked lists nodes that u depends on ("u runs after them"),
	// including the hypothetical virtual edge. Traversal follows the
	// dependency direction so the returned chain reads in run order.
	nextLocked := func(u string) []string {
		nodes := append([]string(nil), s.tasks[u].deps...)
		if u == virtualFrom && virtualTo != "" {
			nodes = append(nodes, virtualTo)
		}
		sort.Strings(nodes)
		return nodes
	}

	var dfs func(u string) []string
	dfs = func(u string) []string {
		color[u] = gray
		stack = append(stack, u)

		for _, p := range nextLocked(u) {
			if _, ok := s.tasks[p]; !ok {
				continue // missing dependencies are validated separately
			}
			switch color[p] {
			case white:
				if chain := dfs(p); chain != nil {
					return chain
				}
			case gray:
				// Rotate the recursion so the cycle is printed following
				// dependency edges: the current u waits on p, so the chain
				// is p, ..., u, p (p repeated at the end).
				start := 0
				for i, n := range stack {
					if n == p {
						start = i
						break
					}
				}
				chain := append([]string(nil), stack[start:]...)
				return append(chain, p)
			}
		}

		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}

	for _, id := range s.order {
		if color[id] == white {
			if chain := dfs(id); chain != nil {
				return &CycleError{Chain: chain}
			}
		}
	}
	return nil
}

// Result is the terminal outcome of a single task.
type Result struct {
	Status Status
	Err    error
}

// Report holds the outcome of a Run, in registration order.
type Report struct {
	order []string
	tasks map[string]Result
}

// StatusOf reports the status of id and whether it was part of the run.
func (r *Report) StatusOf(id string) (Status, bool) {
	res, ok := r.tasks[id]
	return res.Status, ok
}

// Err returns the failure error of id, or nil.
func (r *Report) Err(id string) error {
	return r.tasks[id].Err
}

// IDs returns all task ids in registration order.
func (r *Report) IDs() []string {
	return append([]string(nil), r.order...)
}

func (r *Report) filter(want Status) []string {
	var out []string
	for _, id := range r.order {
		if r.tasks[id].Status == want {
			out = append(out, id)
		}
	}
	return out
}

// Succeeded returns the ids of successful tasks in registration order.
func (r *Report) Succeeded() []string { return r.filter(Succeeded) }

// Failed returns the ids of failed tasks in registration order.
func (r *Report) Failed() []string { return r.filter(Failed) }

// Blocked returns the ids of blocked tasks in registration order.
func (r *Report) Blocked() []string { return r.filter(Blocked) }

// Run validates the graph and executes every task, blocking until every task
// has reached a terminal state. It returns a *CycleError (without running
// anything) if the graph contains a cycle, or an ErrMissingDependency error
// if a task depends on an unknown id.
func (s *Scheduler) Run(ctx context.Context) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: Run called more than once", ErrSchedulerStarted)
	}
	for _, id := range s.order {
		for _, d := range s.tasks[id].deps {
			if _, ok := s.tasks[d]; !ok {
				s.mu.Unlock()
				return nil, fmt.Errorf("%w: %q requires %q", ErrMissingDependency, id, d)
			}
		}
	}
	if cyc := s.findCycleLocked("", ""); cyc != nil {
		s.mu.Unlock()
		return nil, cyc
	}
	s.started = true

	// Single coordinator goroutine owns all status transitions; worker
	// goroutines only report completion through the condition variable.
	for {
		progressed := false

		// Propagate failure/blocking to direct dependents; repeated passes
		// propagate transitively across the whole graph.
		for _, id := range s.order {
			t := s.tasks[id]
			if t.status != Pending {
				continue
			}
			for _, d := range t.deps {
				switch s.tasks[d].status {
				case Failed, Blocked:
					t.status = Blocked
					progressed = true
				}
			}
		}

		// Launch every task whose dependencies have all succeeded.
		for _, id := range s.order {
			t := s.tasks[id]
			if t.status != Pending {
				continue
			}
			ready := true
			for _, d := range t.deps {
				if s.tasks[d].status != Succeeded {
					ready = false
					break
				}
			}
			if ready {
				t.status = Running
				s.running.Add(1)
				progressed = true
				go s.runTask(ctx, t)
			}
		}

		allDone := true
		for _, id := range s.order {
			if st := s.tasks[id].status; st == Pending || st == Running {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		if !progressed {
			s.cond.Wait()
		}
	}
	s.mu.Unlock()

	s.running.Wait()

	s.mu.Lock()
	report := &Report{order: append([]string(nil), s.order...), tasks: make(map[string]Result, len(s.tasks))}
	for id, t := range s.tasks {
		report.tasks[id] = Result{Status: t.status, Err: t.err}
	}
	s.mu.Unlock()
	return report, nil
}

func (s *Scheduler) runTask(ctx context.Context, t *task) {
	var err error
	if t.fn != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("dag: task %q panicked: %v", t.id, r)
				}
			}()
			err = t.fn(ctx)
		}()
	}

	s.mu.Lock()
	if err != nil {
		t.status = Failed
		t.err = err
	} else {
		t.status = Succeeded
	}
	s.running.Done()
	s.cond.Broadcast()
	s.mu.Unlock()
}
