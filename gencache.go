// Package gencache implements a generation-scoped task cache.
//
// Tasks read the current generation when they start, compute their value
// without holding any lock, and publish the result afterwards. A
// generation switch is linearized against every publish: a result computed
// by a task from an older generation is either observed entirely before the
// switch (and stored in the old generation) or rejected entirely (and never
// written to any cache, in particular not to the new generation's cache).
//
// A rejected task may still run an injected cleanup function to release the
// resources it allocated while computing. Cleanup runs without holding the
// coordinator lock and never delays the start of tasks from a newer
// generation.
package gencache

import (
	"errors"
	"sync"
)

// Status is the outcome of a task execution.
type Status int

const (
	// StatusPublished means the task succeeded and its result is cached in
	// the generation it was started in.
	StatusPublished Status = iota
	// StatusFailed means the task function returned an error. Nothing is
	// cached.
	StatusFailed
	// StatusRejected means the task succeeded but its generation had been
	// switched away before the result was linearized; the cleanup function
	// (if any) was run and nothing was cached.
	StatusRejected
)

func (s Status) String() string {
	switch s {
	case StatusPublished:
		return "published"
	case StatusFailed:
		return "failed"
	case StatusRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// Outcome reports what happened to a task after Execute returns.
type Outcome struct {
	Status Status
	// Value is the computed value when Status is StatusPublished, nil
	// otherwise.
	Value interface{}
	// Err is the task function's error when Status is StatusFailed, nil
	// otherwise.
	Err error
}

// Cleanup releases resources produced by a task whose result has been
// rejected because the generation moved on.
type Cleanup func()

// Task computes a value. The returned cleanup function may be nil; when
// non-nil it is called exactly once if and only if the result is rejected
// due to a generation switch. Timing, blocking and errors are entirely under
// the caller's (in tests: the injected function's) control.
type Task func() (value interface{}, cleanup Cleanup, err error)

type generation struct {
	id    int
	cache map[string]interface{}
	// wg tracks tasks that started in this generation and have not yet
	// finished publish-reject linearization and (possibly) cleanup.
	wg sync.WaitGroup
}

// Coordinator serializes generation switches against result publication.
type Coordinator struct {
	mu      sync.Mutex
	current *generation
	gens    map[int]*generation
}

// NewCoordinator starts a coordinator at generation 0 with an empty cache.
func NewCoordinator() *Coordinator {
	gen := &generation{id: 0, cache: make(map[string]interface{})}
	return &Coordinator{
		current: gen,
		gens:    map[int]*generation{0: gen},
	}
}

// Current returns the generation id a task should capture at start time.
func (c *Coordinator) Current() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current.id
}

// Start atomically snapshots the current generation id. The task must pass
// the returned id to Execute. This is the generation read performed at task
// start.
func (c *Coordinator) Start() int {
	return c.Current()
}

// Switch atomically advances the current generation to a new empty
// generation. Tasks from older generations that have not yet published will
// be rejected; the switch does not wait for them or for their cleanup. It is
// safe to call Switch concurrently with running tasks.
func (c *Coordinator) Switch() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := &generation{id: c.current.id + 1, cache: make(map[string]interface{})}
	c.gens[next.id] = next
	c.current = next
	return next.id
}

// Execute runs task on behalf of the generation captured at start time.
//
// The computation itself runs without holding the coordinator lock. Only
// the decision to publish or reject is made under the lock, linearized with
// Switch. Rejected cleanup runs outside the lock.
func (c *Coordinator) Execute(genID int, key string, task Task) Outcome {
	gen, ok := c.generation(genID)
	if !ok {
		return Outcome{Status: StatusFailed, Err: errors.New("gencache: unknown generation")}
	}
	gen.wg.Add(1)
	defer gen.wg.Done()

	value, cleanup, err := task()
	if err != nil {
		return Outcome{Status: StatusFailed, Err: err}
	}

	c.mu.Lock()
	if c.current.id != genID {
		// Linearized after the switch: reject without touching any cache.
		c.mu.Unlock()
		// Outside the lock: old generations may clean up without blocking
		// switches or new-generation task starts.
		if cleanup != nil {
			cleanup()
		}
		return Outcome{Status: StatusRejected}
	} else {
		// Linearized before the switch: publish into the generation the
		// task started in.
		gen.cache[key] = value
		val := value
		c.mu.Unlock()
		return Outcome{Status: StatusPublished, Value: val}
	}
}

func (c *Coordinator) generation(id int) (*generation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gen, ok := c.gens[id]
	return gen, ok
}

// Result returns the cached value for key in the given generation.
func (c *Coordinator) Result(genID int, key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gen, ok := c.gens[genID]
	if !ok {
		return nil, false
	}
	v, ok := gen.cache[key]
	return v, ok
}

// WaitInflight blocks until all tasks started in the given generation have
// published or been rejected and finished their cleanup. It is intended for
// deterministic test synchronization (cleanup itself is never waited on by
// Switch or by tasks from newer generations).
func (c *Coordinator) WaitInflight(genID int) error {
	gen, ok := c.generation(genID)
	if !ok {
		return errors.New("gencache: unknown generation")
	}
	gen.wg.Wait()
	return nil
}
