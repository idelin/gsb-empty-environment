// Package lifecycle provides a dependency-aware container that shuts
// resources down in reverse dependency order.
//
// Resources are registered with Add together with the names of the
// resources they depend on. Dependencies must be registered before their
// dependents, which makes the dependency graph acyclic by construction.
// Close shuts every resource down exactly once: dependents are closed
// before the resources they depend on, independent resources are closed
// concurrently, and a slow or failing resource never prevents unrelated
// resources from being closed. Close errors are aggregated into a single
// error ordered by resource creation sequence, and repeated Close calls
// share the result of the first shutdown.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// CloseFunc releases a single resource. It should stop work promptly when
// ctx is done; if it keeps blocking past the container's close timeout the
// shutdown moves on without it and reports a timeout error.
type CloseFunc func(ctx context.Context) error

// ErrClosed is returned by Add after the container has started closing.
var ErrClosed = errors.New("lifecycle: container is closed")

// Container tracks resources and closes them in reverse dependency order.
type Container struct {
	timeout time.Duration

	mu        sync.Mutex
	resources []*resource
	names     map[string]int
	closed    bool

	closeOnce sync.Once
	done      chan struct{}
	closeErr  error
}

type resource struct {
	name  string
	deps  []int // creation indexes of resources this one depends on
	close CloseFunc
}

// Option configures a Container.
type Option func(*Container)

// WithCloseTimeout bounds how long Close waits for each individual
// resource. A resource that exceeds the timeout is reported as failed but
// never blocks the shutdown of other resources. A zero timeout (the
// default) waits indefinitely.
func WithCloseTimeout(d time.Duration) Option {
	return func(c *Container) { c.timeout = d }
}

// New returns an empty Container.
func New(opts ...Option) *Container {
	c := &Container{
		names: make(map[string]int),
		done:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Add registers a resource under name. dependsOn lists the names of
// resources that must outlive this one; each of them must already be
// registered. The registration order defines the creation sequence used
// for stable error aggregation.
func (c *Container) Add(name string, closeFn CloseFunc, dependsOn ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}
	if name == "" {
		return errors.New("lifecycle: resource name must not be empty")
	}
	if closeFn == nil {
		return fmt.Errorf("lifecycle: resource %q: close func must not be nil", name)
	}
	if _, exists := c.names[name]; exists {
		return fmt.Errorf("lifecycle: resource %q is already registered", name)
	}
	deps := make([]int, 0, len(dependsOn))
	for _, depName := range dependsOn {
		idx, ok := c.names[depName]
		if !ok {
			return fmt.Errorf("lifecycle: resource %q depends on unknown resource %q", name, depName)
		}
		deps = append(deps, idx)
	}

	c.names[name] = len(c.resources)
	c.resources = append(c.resources, &resource{name: name, deps: deps, close: closeFn})
	return nil
}

// Close shuts every registered resource down and returns the aggregated
// close errors, or nil if all resources closed cleanly. Concurrent and
// repeated calls wait for the same single shutdown and all receive its
// identical result; the ctx of later calls is ignored.
func (c *Container) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.closeErr = c.shutdown(ctx)
		close(c.done)
	})
	<-c.done
	return c.closeErr
}

// shutdown closes every resource exactly once. Each resource waits for its
// dependents to finish before closing, so dependents always shut down
// first while independent resources run concurrently. A failure or timeout
// marks the resource done but never skips anything else.
func (c *Container) shutdown(ctx context.Context) error {
	c.mu.Lock()
	resources := make([]*resource, len(c.resources))
	copy(resources, c.resources)
	c.mu.Unlock()

	n := len(resources)
	dependents := make([][]int, n)
	for i, r := range resources {
		for _, dep := range r.deps {
			dependents[dep] = append(dependents[dep], i)
		}
	}

	results := make([]error, n)
	finished := make([]chan struct{}, n)
	for i := range finished {
		finished[i] = make(chan struct{})
	}

	var wg sync.WaitGroup
	for i, r := range resources {
		wg.Add(1)
		go func(i int, r *resource) {
			defer wg.Done()
			for _, dependent := range dependents[i] {
				<-finished[dependent]
			}
			results[i] = c.runClose(ctx, r)
			close(finished[i])
		}(i, r)
	}
	wg.Wait()

	// Aggregate in creation-sequence order so the combined error is
	// stable regardless of the order closes actually completed in.
	var errs []error
	for i, r := range resources {
		if results[i] != nil {
			errs = append(errs, fmt.Errorf("lifecycle: close %q (#%d): %w", r.name, i, results[i]))
		}
	}
	return errors.Join(errs...)
}

// runClose invokes r.close, giving up with a timeout error if the
// container's close timeout elapses first.
func (c *Container) runClose(ctx context.Context, r *resource) error {
	closeCtx := ctx
	cancel := context.CancelFunc(func() {})
	if c.timeout > 0 {
		closeCtx, cancel = context.WithTimeout(ctx, c.timeout)
	}
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.close(closeCtx) }()

	select {
	case err := <-done:
		return err
	case <-closeCtx.Done():
		return fmt.Errorf("close did not finish in time: %w", closeCtx.Err())
	}
}
