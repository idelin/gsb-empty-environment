// Package lifecycle provides a dependency-aware shutdown container.
//
// Resources are registered with a cleanup function and optional
// dependencies on previously registered resources. Close shuts resources
// down in reverse dependency order: a resource's cleanup runs only after
// every resource that depends on it has finished closing. Resources with
// no dependency relation close concurrently. A resource that exceeds its
// timeout is reported but never blocks unrelated resources. All close
// failures are aggregated into a CloseError ordered stably by resource
// creation index. Repeated Close calls share the first call's result.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrCloseTimeout is wrapped into a resource's close error when its
// cleanup does not return before its timeout elapses.
var ErrCloseTimeout = errors.New("lifecycle: cleanup timed out")

// Cleanup releases a single resource. It should honor ctx cancellation;
// a Cleanup that ignores ctx is abandoned (but never blocks Close) once
// the resource's timeout elapses.
type Cleanup func(ctx context.Context) error

// Handle identifies a registered resource. Handles are assigned in
// creation order starting at 0.
type Handle int

// ResourceError is a single resource's close failure.
type ResourceError struct {
	Index int
	Name  string
	Err   error
}

// CloseError aggregates every failure observed during Close, ordered
// stably by resource creation index.
type CloseError struct {
	Errors []ResourceError
}

func (e *CloseError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, re := range e.Errors {
		parts[i] = fmt.Sprintf("resource %d (%s): %v", re.Index, re.Name, re.Err)
	}
	return "lifecycle: close failed: " + strings.Join(parts, "; ")
}

// Unwrap exposes each underlying failure to errors.Is / errors.As.
func (e *CloseError) Unwrap() []error {
	errs := make([]error, len(e.Errors))
	for i, re := range e.Errors {
		errs[i] = re.Err
	}
	return errs
}

type resource struct {
	index   int
	name    string
	deps    []int
	cleanup Cleanup
	timeout time.Duration
}

// Option customizes a registered resource.
type Option func(*resource)

// WithName attaches a human-readable name used in error messages.
func WithName(name string) Option {
	return func(r *resource) { r.name = name }
}

// WithTimeout bounds how long Close waits for this resource's cleanup.
// Other resources are never skipped when the timeout fires.
func WithTimeout(d time.Duration) Option {
	return func(r *resource) { r.timeout = d }
}

// Container tracks resources and closes them in reverse dependency order.
type Container struct {
	mu        sync.Mutex
	resources []*resource

	closing bool
	done    chan struct{}
	err     error
}

// New returns an empty Container.
func New() *Container {
	return &Container{}
}

// Add registers cleanup with the container. deps must be handles returned
// by earlier Add calls; each dependency's cleanup runs only after this
// resource has finished closing. Add panics if called after Close has
// started or if a dependency handle is invalid.
func (c *Container) Add(cleanup Cleanup, deps []Handle, opts ...Option) Handle {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		panic("lifecycle: Add after Close")
	}
	r := &resource{
		index:   len(c.resources),
		name:    fmt.Sprintf("resource-%d", len(c.resources)),
		cleanup: cleanup,
	}
	for _, d := range deps {
		if d < 0 || int(d) >= len(c.resources) {
			panic(fmt.Sprintf("lifecycle: invalid dependency handle %d", d))
		}
		r.deps = append(r.deps, int(d))
	}
	for _, opt := range opts {
		opt(r)
	}
	c.resources = append(c.resources, r)
	return Handle(r.index)
}

// Close shuts every resource down in reverse dependency order and
// aggregates all failures into a *CloseError (nil when everything closes
// cleanly). Concurrent and repeated calls wait for the first call and
// share its final result.
func (c *Container) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closing {
		done := c.done
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.closing = true
	c.done = make(chan struct{})
	c.mu.Unlock()

	err := c.shutdown(ctx)

	c.mu.Lock()
	c.err = err
	close(c.done)
	c.mu.Unlock()
	return err
}

func (c *Container) shutdown(ctx context.Context) error {
	c.mu.Lock()
	resources := c.resources
	c.mu.Unlock()

	n := len(resources)
	dependents := make([][]int, n)
	for _, r := range resources {
		for _, d := range r.deps {
			dependents[d] = append(dependents[d], r.index)
		}
	}

	dones := make([]chan struct{}, n)
	for i := range dones {
		dones[i] = make(chan struct{})
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i, r := range resources {
		wg.Add(1)
		go func(i int, r *resource) {
			defer wg.Done()
			defer close(dones[i])
			for _, dep := range dependents[i] {
				<-dones[dep]
			}
			errs[i] = runCleanup(ctx, r)
		}(i, r)
	}
	wg.Wait()

	var aggregated *CloseError
	for i, err := range errs {
		if err == nil {
			continue
		}
		if aggregated == nil {
			aggregated = &CloseError{}
		}
		aggregated.Errors = append(aggregated.Errors, ResourceError{
			Index: i,
			Name:  resources[i].name,
			Err:   err,
		})
	}
	if aggregated == nil {
		return nil
	}
	return aggregated
}

// runCleanup executes r.cleanup with its timeout. A cleanup that ignores
// its context is abandoned once the timeout fires; the buffered result
// channel keeps the abandoned goroutine from leaking on send.
func runCleanup(ctx context.Context, r *resource) error {
	rctx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		rctx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	result := make(chan error, 1)
	go func() { result <- r.cleanup(rctx) }()

	select {
	case err := <-result:
		return err
	case <-rctx.Done():
		if r.timeout > 0 && rctx.Err() == context.DeadlineExceeded {
			return ErrCloseTimeout
		}
		return rctx.Err()
	}
}
