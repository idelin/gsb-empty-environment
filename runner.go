// Package taskgraph runs a graph of lazily constructed tasks. Tasks are
// deduplicated by the canonical encoding of their name and parameters:
// concurrent or repeated requests for the same key share a single
// construction and its result. A dependency that points back at an
// ancestor still being constructed is reported as a cycle instead of
// deadlocking on that ancestor's future.
package taskgraph

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Spec identifies a task: a constructor name plus its parameters.
type Spec struct {
	Name   string
	Params map[string]interface{}
}

// Key returns the canonical deduplication key for the spec.
func (s Spec) Key() string { return canonicalKey(s.Name, s.Params) }

// Constructor builds the value for a spec. It may request dependencies
// through the provided runner; those requests observe the caller's
// construction chain via ctx.
type Constructor func(ctx context.Context, r *Runner, spec Spec) (interface{}, error)

// CycleError reports a dependency that reaches an ancestor currently
// being constructed. Path lists the canonical keys from the outermost
// ancestor down to (and including) the repeated key.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "taskgraph: dependency cycle detected: " + strings.Join(e.Path, " -> ")
}

type task struct {
	done chan struct{}
	val  interface{}
	err  error
}

type chainContextKey struct{}

// Runner constructs and caches task values.
type Runner struct {
	mu     sync.Mutex
	tasks  map[string]*task // in-flight or successfully constructed tasks
	ctors  map[string]Constructor
}

func NewRunner() *Runner {
	return &Runner{
		tasks: make(map[string]*task),
		ctors: make(map[string]Constructor),
	}
}

// Register binds a constructor to a task name.
func (r *Runner) Register(name string, c Constructor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctors[name] = c
}

// Get returns the value for spec, constructing it if necessary. Requests
// for the same canonical key share one construction. A failed
// construction is evicted, so a later Get retries; a successful one is
// cached. If spec depends (transitively) on an ancestor that is still
// being constructed on this call chain, Get returns a *CycleError
// immediately rather than waiting on that ancestor's future.
func (r *Runner) Get(ctx context.Context, spec Spec) (interface{}, error) {
	key := spec.Key()
	if chain, ok := ctx.Value(chainContextKey{}).([]string); ok {
		for _, ancestor := range chain {
			if ancestor == key {
				path := append(append([]string(nil), chain...), key)
				return nil, &CycleError{Path: path}
			}
		}
	}

	r.mu.Lock()
	if t, ok := r.tasks[key]; ok {
		r.mu.Unlock()
		select {
		case <-t.done:
			return t.val, t.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctor, ok := r.ctors[spec.Name]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("taskgraph: no constructor registered for %q", spec.Name)
	}
	t := &task{done: make(chan struct{})}
	r.tasks[key] = t
	r.mu.Unlock()

	chain, _ := ctx.Value(chainContextKey{}).([]string)
	childChain := append(append([]string(nil), chain...), key)
	childCtx := context.WithValue(ctx, chainContextKey{}, childChain)

	val, err := ctor(childCtx, r, spec)

	r.mu.Lock()
	if err != nil {
		// Do not cache failures: a later request must retry construction.
		delete(r.tasks, key)
	}
	t.val, t.err = val, err
	close(t.done)
	r.mu.Unlock()
	return val, err
}
