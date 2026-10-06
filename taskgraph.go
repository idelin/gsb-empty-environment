// Package taskgraph runs a graph of memoized tasks. Tasks are
// deduplicated by a canonical key derived from their parameters:
// concurrent or independent requests for the same key share a single
// construction and its result. A dependency that points back at a task
// still being constructed by one of its ancestors is reported as a
// cycle instead of deadlocking on its own future.
package taskgraph

import (
	"context"
	"strings"
	"sync"
)

// Constructor builds the value for one task key. The ctx it receives
// carries the chain of keys currently under construction, so nested
// Do calls made with that ctx participate in cycle detection.
type Constructor func(ctx context.Context) (interface{}, error)

// CycleError is returned when a task (transitively) depends on a task
// that is already being constructed further up the same call chain.
type CycleError struct {
	// Path is the chain of task keys from the outermost ancestor down
	// to (and including) the repeated key that closes the cycle.
	Path []string
}

func (e *CycleError) Error() string {
	return "taskgraph: dependency cycle: " + strings.Join(e.Path, " -> ")
}

// AsCycle reports whether err is a *CycleError, returning it if so.
func AsCycle(err error) (*CycleError, bool) {
	c, ok := err.(*CycleError)
	return c, ok
}

// chainContextKey carries the stack of keys under construction.
type chainContextKey struct{}

// call is the shared future for one key.
type call struct {
	done  chan struct{}
	value interface{}
	err   error
}

// Runner memoizes task constructions by key. The zero value is ready
// to use, but a Runner must not be copied after first use.
type Runner struct {
	mu    sync.Mutex
	calls map[string]*call
}

// New returns an empty Runner.
func New() *Runner {
	return &Runner{calls: make(map[string]*call)}
}

// Do returns the value for key, constructing it with build if needed.
//
// At most one construction runs per key at a time: concurrent callers
// wait on the same future and share its outcome. A successful result
// is memoized, so later independent requests for the same key reuse it
// without rebuilding. A failed construction is evicted once all current
// waiters observe the error, so a subsequent Do retries the constructor.
//
// If key is already being constructed by an ancestor on this call
// chain (tracked via ctx), Do returns a *CycleError immediately rather
// than waiting on a future that the current chain itself must complete.
func (r *Runner) Do(ctx context.Context, key string, build Constructor) (interface{}, error) {
	chain, _ := ctx.Value(chainContextKey{}).([]string)
	for _, ancestor := range chain {
		if ancestor == key {
			path := make([]string, len(chain)+1)
			copy(path, chain)
			path[len(chain)] = key
			return nil, &CycleError{Path: path}
		}
	}

	r.mu.Lock()
	if r.calls == nil {
		r.calls = make(map[string]*call)
	}
	if c, ok := r.calls[key]; ok {
		r.mu.Unlock()
		select {
		case <-c.done:
			return c.value, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	r.calls[key] = c
	r.mu.Unlock()

	child := make([]string, len(chain)+1)
	copy(child, chain)
	child[len(chain)] = key
	buildCtx := context.WithValue(ctx, chainContextKey{}, child)

	value, err := build(buildCtx)

	r.mu.Lock()
	c.value, c.err = value, err
	if err != nil {
		// Failures are not memoized: evict before releasing waiters
		// so the next request reconstructs.
		delete(r.calls, key)
	}
	close(c.done)
	r.mu.Unlock()
	return value, err
}
