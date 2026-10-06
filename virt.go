// Virtual executor for deterministic tests and simulations. It tracks
// held resources independently of the scheduler, so tests can verify
// resource conservation (held never exceeds capacity, everything is
// released at the end) and observe an exact event log.
package sched

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Script describes how the virtual executor performs one task.
type Script struct {
	Work          time.Duration // simulated work before returning
	Err           error         // result error; nil means success
	WaitForCancel bool          // block until ctx is canceled before finishing
	Cleanup       time.Duration // cleanup delay before releasing resources
}

// Event is one entry in the executor's deterministic event log.
type Event struct {
	Kind string // "start", "cleanup", "end"
	Task string
}

// Executor simulates task execution and records what happened.
type Executor struct {
	mu         sync.Mutex
	capacity   Resources
	needs      map[string]Resources
	scripts    map[string]Script
	held       Resources
	maxHeld    Resources
	events     []Event
	violations []string
	cleaned    map[string]chan struct{}
}

// NewExecutor returns a virtual executor limited to capacity.
func NewExecutor(capacity Resources) *Executor {
	return &Executor{
		capacity: cloneResources(capacity),
		needs:    map[string]Resources{},
		scripts:  map[string]Script{},
		held:     Resources{},
		maxHeld:  Resources{},
		cleaned:  map[string]chan struct{}{},
	}
}

// Handle registers how task id (with the given resource needs) behaves.
func (e *Executor) Handle(id string, needs Resources, sc Script) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.needs[id] = cloneResources(needs)
	e.scripts[id] = sc
	e.cleaned[id] = make(chan struct{})
}

// RunFunc returns the Run function for task id, for use with Scheduler.Add.
func (e *Executor) RunFunc(id string) func(context.Context) error {
	return func(ctx context.Context) error {
		e.mu.Lock()
		sc := e.scripts[id]
		needs := e.needs[id]
		addTo(e.held, needs)
		keys := make([]string, 0, len(e.held))
		for k := range e.held {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if e.held[k] > e.maxHeld[k] {
				e.maxHeld[k] = e.held[k]
			}
			if e.held[k] > e.capacity[k] {
				e.violations = append(e.violations,
					fmt.Sprintf("task %q: held %s=%d exceeds capacity %d", id, k, e.held[k], e.capacity[k]))
			}
		}
		e.events = append(e.events, Event{Kind: "start", Task: id})
		e.mu.Unlock()

		if sc.Work > 0 {
			time.Sleep(sc.Work)
		}
		if sc.WaitForCancel {
			<-ctx.Done()
		}
		if sc.Cleanup > 0 {
			time.Sleep(sc.Cleanup)
		}

		e.mu.Lock()
		if sc.Cleanup > 0 || sc.WaitForCancel {
			e.events = append(e.events, Event{Kind: "cleanup", Task: id})
			close(e.cleaned[id])
		}
		subFrom(e.held, needs)
		e.events = append(e.events, Event{Kind: "end", Task: id})
		e.mu.Unlock()
		return sc.Err
	}
}

// Events returns the recorded event log in order.
func (e *Executor) Events() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.events...)
}

// Held returns currently held resources; zero when nothing runs.
func (e *Executor) Held() Resources {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneResources(e.held)
}

// MaxHeld returns the peak concurrently held resources.
func (e *Executor) MaxHeld() Resources {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneResources(e.maxHeld)
}

// Violations lists every moment held resources exceeded capacity.
func (e *Executor) Violations() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.violations...)
}

// CleanedUp returns a channel closed when the task's cleanup finished.
func (e *Executor) CleanedUp(id string) <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cleaned[id]
}

// Started reports whether the task has started executing.
func (e *Executor) Started(id string) bool {
	for _, ev := range e.Events() {
		if ev.Kind == "start" && ev.Task == id {
			return true
		}
	}
	return false
}
