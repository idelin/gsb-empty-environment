// Package dagscheduler executes named tasks arranged as a directed acyclic graph.
//
// A task runs only after every dependency has succeeded. A failure (or a
// dependency that was blocked) marks the task itself Blocked without invoking
// its function, and that blocked state cascades to all descendants. Tasks on
// unrelated branches keep running. Cycles are rejected before execution and
// when edges are added dynamically; the scheduler never waits on a cycle.
package dagscheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Status is the execution state of a task.
type Status uint8

const (
	// Pending means the task is registered and waiting to run.
	Pending Status = iota
	// Running means the task function is currently executing.
	Running
	// Succeeded means the task function returned without error.
	Succeeded
	// Failed means the task function returned an error.
	Failed
	// Blocked means a prerequisite (directly or transitively) failed or was
	// blocked, so the task function was never invoked.
	Blocked
)

// String returns the human-readable name of the status.
func (st Status) String() string {
	switch st {
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
		return fmt.Sprintf("STATUS(%d)", int(st))
	}
}

// TaskFunc is the work associated with a task. It receives the context passed
// to Scheduler.Run.
type TaskFunc func(ctx context.Context) error

// Result is the terminal outcome of a task.
type Result struct {
	// Name is the task name.
	Name string
	// Status is Succeeded, Failed, or Blocked after Run returns.
	Status Status
	// Err is the error returned by the task function for Failed tasks, and is
	// nil otherwise.
	Err error
}

var (
	// ErrNotFound indicates that a referenced task name is not registered.
	ErrNotFound = errors.New("dagscheduler: task not found")
	// ErrDuplicate indicates that Add was called twice with the same name.
	ErrDuplicate = errors.New("dagscheduler: duplicate task")
	// ErrRunning indicates an edge was added to a task whose function has
	// already started.
	ErrRunning = errors.New("dagscheduler: task already running")
	// ErrAlreadyRun indicates Run was invoked more than once.
	ErrAlreadyRun = errors.New("dagscheduler: scheduler has already run")
	// ErrEmptyName indicates that an empty task name was supplied.
	ErrEmptyName = errors.New("dagscheduler: task name must not be empty")
)

// CycleError reports a dependency cycle. Chain lists the nodes along the
// closing path with the first node repeated at the end, for example
// ["A", "B", "C", "A"] for the cycle A -> B -> C -> A.
type CycleError struct {
	Chain []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("dagscheduler: dependency cycle detected: %v", e.Chain)
}

// Is supports errors.Is(err, &CycleError{}) by type.
func (e *CycleError) Is(target error) bool {
	_, ok := target.(*CycleError)
	return ok
}

type node struct {
	name   string
	fn     TaskFunc
	deps   map[string]struct{}
	status Status
	err    error
	// done is closed when the task reaches a terminal status.
	done chan struct{}
}

// Scheduler owns a set of named tasks and runs them respecting dependencies.
//
// A Scheduler is safe for concurrent use: task functions may call
// AddDependency from within other running tasks, subject to the restriction
// documented on AddDependency.
type Scheduler struct {
	mu      sync.Mutex
	cond    *sync.Cond
	nodes   map[string]*node
	order   []string
	started bool
}

// New creates an empty Scheduler.
func New() *Scheduler {
	s := &Scheduler{nodes: make(map[string]*node)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Add registers a task that depends on deps. All prerequisites must already be
// registered. A nil fn denotes a no-op task. Add fails with ErrDuplicate if
// name exists, ErrEmptyName for an empty name, and ErrNotFound if any
// prerequisite is missing.
func (s *Scheduler) Add(name string, fn TaskFunc, deps ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "" {
		return ErrEmptyName
	}
	if _, exists := s.nodes[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicate, name)
	}
	for _, dep := range deps {
		if _, ok := s.nodes[dep]; !ok {
			return fmt.Errorf("%w: prerequisite %q of %q", ErrNotFound, dep, name)
		}
	}
	n := &node{
		name: name,
		fn:   fn,
		deps: make(map[string]struct{}, len(deps)),
		done: make(chan struct{}),
	}
	for _, dep := range deps {
		n.deps[dep] = struct{}{}
	}
	s.nodes[name] = n
	s.order = append(s.order, name)
	return nil
}

// AddDependency adds the edge name -> dependsOn (name waits for dependsOn).
//
// The restricted dynamic rule is:
//
//   - Before Run the edge is always accepted.
//   - During Run the edge is accepted only while name is still Pending; it is
//     rejected with ErrRunning once name has started executing, and with
//     ErrNotFound if either node is unknown.
//   - Adding an edge that closes a cycle fails with *CycleError and leaves the
//     graph unchanged. Adding an existing edge is a no-op.
func (s *Scheduler) AddDependency(name, dependsOn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.nodes[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	if _, ok := s.nodes[dependsOn]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, dependsOn)
	}
	if s.started && n.status != Pending {
		return fmt.Errorf("%w: %q is %s", ErrRunning, name, n.status)
	}
	if _, exists := n.deps[dependsOn]; exists {
		return nil
	}

	n.deps[dependsOn] = struct{}{}
	if chain, cyclic := detectCycle(s.nodes, s.order); cyclic {
		delete(n.deps, dependsOn)
		return &CycleError{Chain: chain}
	}
	// Wake waiters so newly added prerequisites are observed.
	s.cond.Broadcast()
	return nil
}

// HasCycle reports whether the dependency graph contains a cycle and, when it
// does, a node chain whose first and last elements are the same node.
func (s *Scheduler) HasCycle() (chain []string, cyclic bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return detectCycle(s.nodes, s.order)
}

// Run starts one goroutine per task and blocks until every task reaches a
// terminal status (Succeeded, Failed, or Blocked) or ctx is canceled. Results
// are returned in registration order. A cyclic graph is rejected before any
// task starts; Run may be called only once.
func (s *Scheduler) Run(ctx context.Context) ([]Result, error) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil, ErrAlreadyRun
	}
	if chain, cyclic := detectCycle(s.nodes, s.order); cyclic {
		s.mu.Unlock()
		return nil, &CycleError{Chain: chain}
	}
	s.started = true
	nodes := make([]*node, 0, len(s.order))
	for _, name := range s.order {
		nodes = append(nodes, s.nodes[name])
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n *node) {
			defer wg.Done()
			s.runNode(ctx, n)
		}(n)
	}
	wg.Wait()
	return s.Results(), nil
}

// runNode waits for all prerequisites, then either marks the node Blocked when
// any prerequisite did not succeed or executes its function exactly once.
func (s *Scheduler) runNode(ctx context.Context, n *node) {
	s.mu.Lock()
	for {
		blocked := false
		allDone := true
		for depName := range n.deps {
			dep := s.nodes[depName]
			if !isTerminal(dep.status) {
				allDone = false
				continue
			}
			if dep.status != Succeeded {
				blocked = true
			}
		}
		switch {
		case blocked:
			n.status = Blocked
			close(n.done)
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		case allDone:
			n.status = Running
			fn := n.fn
			s.mu.Unlock()

			var err error
			if fn != nil {
				err = fn(ctx)
			}

			s.mu.Lock()
			if err != nil {
				n.status = Failed
				n.err = err
			} else {
				n.status = Succeeded
			}
			close(n.done)
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		s.cond.Wait()
	}
}

// Status returns the current status of the named task.
func (s *Scheduler) Status(name string) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[name]
	if !ok {
		return Pending, false
	}
	return n.status, true
}

// Results returns the outcome of every task in registration order. It may be
// called concurrently with or after Run.
func (s *Scheduler) Results() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]Result, 0, len(s.order))
	for _, name := range s.order {
		n := s.nodes[name]
		results = append(results, Result{Name: name, Status: n.status, Err: n.err})
	}
	return results
}

func isTerminal(st Status) bool {
	return st == Succeeded || st == Failed || st == Blocked
}

// detectCycle performs an iterative DFS with tri-color marking over
// dependencies. On a cycle it returns the path from the repeated node back to
// itself, e.g. [A B C A]. It must be called with s.mu held.
func detectCycle(nodes map[string]*node, order []string) ([]string, bool) {
	const (
		white = 0 // unseen
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]uint8, len(nodes))
	var stack []string

	// nextDep returns a white or gray dependency in deterministic order. A
	// gray neighbor is the back edge that closes a cycle.
	nextDep := func(name string) (dep string, col uint8, found bool) {
		n := nodes[name]
		for _, candidate := range order {
			if _, isDep := n.deps[candidate]; !isDep {
				continue
			}
			if c := color[candidate]; c != black {
				return candidate, c, true
			}
		}
		return "", white, false
	}

	for _, start := range order {
		if color[start] != white {
			continue
		}
		color[start] = gray
		stack = append(stack, start)

		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			dep, col, found := nextDep(cur)
			if !found {
				color[cur] = black
				stack = stack[:len(stack)-1]
				continue
			}
			if col == gray {
				idx := indexOf(stack, dep)
				chain := append([]string(nil), stack[idx:]...)
				chain = append(chain, dep)
				return chain, true
			}
			color[dep] = gray
			stack = append(stack, dep)
		}
	}
	return nil, false
}

func indexOf(xs []string, target string) int {
	for i, x := range xs {
		if x == target {
			return i
		}
	}
	return -1
}
