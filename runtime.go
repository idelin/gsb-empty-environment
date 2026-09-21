// Package tasklock implements task-level mutual exclusion locks with
// priority inheritance: when a low-priority task holds a lock that a
// high-priority task waits on, the holder temporarily runs at the
// waiter's priority. Inheritance propagates transitively across
// lock-to-lock wait chains, is revoked on timeout/cancel, and is
// tracked per lock so releasing one lock never erases inheritance
// contributed by another. Deadlock cycles are detected and reported.
//
// Only the Go standard library is used. Task functions, the clock and
// failures are all injected by the caller (typically tests).
package tasklock

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrTimeout is returned by Acquire when the timeout elapses before
	// the lock is granted. Any priority boost the waiter caused is
	// revoked.
	ErrTimeout = errors.New("tasklock: acquire timed out")
	// ErrCanceled is returned by Acquire when the waiting task is
	// canceled via Runtime.Cancel. Any priority boost the waiter
	// caused is revoked.
	ErrCanceled = errors.New("tasklock: acquire canceled")
	// ErrNotOwner is returned by Release when the task does not hold
	// the lock.
	ErrNotOwner = errors.New("tasklock: release by non-owner")
	// ErrReentrant is returned by Acquire when a task tries to acquire
	// a lock it already holds (a trivial self-deadlock).
	ErrReentrant = errors.New("tasklock: reentrant acquire of held lock")
)

// DeadlockError reports a wait-for cycle. Cycle lists the tasks in
// wait order: Cycle[i] waits on a lock held by Cycle[i+1], and the
// last waits on a lock held by the first.
type DeadlockError struct {
	Cycle []*Task
}

func (e *DeadlockError) Error() string {
	names := make([]string, 0, len(e.Cycle)+1)
	for _, t := range e.Cycle {
		names = append(names, t.Name)
	}
	if len(e.Cycle) > 0 {
		names = append(names, e.Cycle[0].Name)
	}
	return "tasklock: deadlock cycle: " + strings.Join(names, " -> ")
}

// Runtime owns the protocol state. A single mutex guards the whole
// inheritance graph, which keeps the protocol easy to reason about;
// tasks never block while holding it.
type Runtime struct {
	mu    sync.Mutex
	clock Clock
}

// NewRuntime creates a Runtime. A nil clock selects RealClock; tests
// inject a ManualClock to control timeouts.
func NewRuntime(clock Clock) *Runtime {
	if clock == nil {
		clock = RealClock{}
	}
	return &Runtime{clock: clock}
}

// Task is a schedulable unit of work with a base priority. Higher
// numbers mean more important. Its effective priority is the maximum
// of its base priority and the priorities inherited from tasks waiting
// on locks it holds.
type Task struct {
	Name string

	rt        *Runtime
	base      int
	eff       int
	held      map[*Lock]struct{}
	waitingOn *Lock
}

// NewTask registers a task with the given base priority.
func (rt *Runtime) NewTask(name string, basePriority int) *Task {
	return &Task{
		Name: name,
		rt:   rt,
		base: basePriority,
		eff:  basePriority,
		held: make(map[*Lock]struct{}),
	}
}

// BasePriority returns the task's own, non-inherited priority.
func (t *Task) BasePriority() int {
	t.rt.mu.Lock()
	defer t.rt.mu.Unlock()
	return t.base
}

// Priority returns the task's effective priority (base plus
// inheritance). This is the value a scheduler would use.
func (t *Task) Priority() int {
	t.rt.mu.Lock()
	defer t.rt.mu.Unlock()
	return t.eff
}

// BlockedOn returns the lock the task is currently waiting for, or
// nil. Useful for diagnostics and tests.
func (t *Task) BlockedOn() *Lock {
	t.rt.mu.Lock()
	defer t.rt.mu.Unlock()
	return t.waitingOn
}

// Lock is a task-level mutex participating in priority inheritance.
type Lock struct {
	name    string
	rt      *Runtime
	owner   *Task
	waiters []*waiter
}

type waiter struct {
	task *Task
	ch   chan error
}

// NewLock creates a lock bound to this runtime.
func (rt *Runtime) NewLock(name string) *Lock {
	return &Lock{name: name, rt: rt}
}

// Name returns the lock's diagnostic name.
func (l *Lock) Name() string { return l.name }

// Owner returns the task currently holding the lock, or nil.
func (l *Lock) Owner() *Task {
	l.rt.mu.Lock()
	defer l.rt.mu.Unlock()
	return l.owner
}

// Acquire blocks until the task owns the lock, the timeout elapses, or
// the task is canceled. A timeout <= 0 waits forever. While blocked,
// the waiter's effective priority is inherited by the lock's owner and,
// transitively, by every task the owner is (in)directly waiting on.
func (t *Task) Acquire(l *Lock, timeout time.Duration) error {
	rt := t.rt
	rt.mu.Lock()
	switch l.owner {
	case nil:
		l.owner = t
		t.held[l] = struct{}{}
		rt.mu.Unlock()
		return nil
	case t:
		rt.mu.Unlock()
		return ErrReentrant
	}
	if cycle := waitCycle(t, l.owner); cycle != nil {
		rt.mu.Unlock()
		return &DeadlockError{Cycle: cycle}
	}
	ch := make(chan error, 1)
	l.waiters = append(l.waiters, &waiter{task: t, ch: ch})
	t.waitingOn = l
	rt.propagate(l.owner)
	rt.mu.Unlock()

	var timer <-chan time.Time
	if timeout > 0 {
		timer = rt.clock.After(timeout)
	}
	select {
	case err := <-ch:
		rt.mu.Lock()
		t.waitingOn = nil
		rt.mu.Unlock()
		return err
	case <-timer:
		rt.mu.Lock()
		if removeWaiter(l, t) {
			// Still queued: the wait is over and the boost is revoked.
			t.waitingOn = nil
			rt.propagate(l.owner)
			rt.mu.Unlock()
			return ErrTimeout
		}
		// Lost the race with a grant or cancel: the outcome is already
		// buffered on ch.
		t.waitingOn = nil
		rt.mu.Unlock()
		return <-ch
	}
}

// Release hands the lock to the highest-priority waiter (earliest
// arrival breaks ties), or frees it. Inheritance contributed by this
// lock's remaining waiters moves to the new owner; inheritance the
// releaser earned through its other held locks is preserved.
func (t *Task) Release(l *Lock) error {
	rt := t.rt
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if l.owner != t {
		return ErrNotOwner
	}
	delete(t.held, l)
	if best := bestWaiter(l); best != nil {
		removeWaiter(l, best.task)
		l.owner = best.task
		best.task.held[l] = struct{}{}
		best.task.waitingOn = nil
		best.ch <- nil
		rt.propagate(best.task)
	} else {
		l.owner = nil
	}
	rt.propagate(t)
	return nil
}

// Cancel wakes the task's pending Acquire with ErrCanceled and revokes
// the priority boost it was contributing. Canceling a task that is not
// waiting is a no-op.
func (rt *Runtime) Cancel(t *Task) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	l := t.waitingOn
	if l == nil {
		return
	}
	for i, w := range l.waiters {
		if w.task == t {
			l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
			t.waitingOn = nil
			w.ch <- ErrCanceled
			rt.propagate(l.owner)
			return
		}
	}
}

// Go runs an injected task function in its own goroutine and reports
// its (possibly injected) failure on the returned channel.
func (rt *Runtime) Go(t *Task, fn func(t *Task) error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- fn(t) }()
	return done
}

// propagate recomputes t's effective priority and walks up the
// wait-for chain, so a change ripples through every task that
// (in)directly depends on t. Callers must hold rt.mu. The chain is
// acyclic because edges are only added after a deadlock check.
func (rt *Runtime) propagate(t *Task) {
	for t != nil {
		eff := t.base
		for l := range t.held {
			for _, w := range l.waiters {
				if w.task.eff > eff {
					eff = w.task.eff
				}
			}
		}
		if eff == t.eff {
			return
		}
		t.eff = eff
		if t.waitingOn == nil {
			return
		}
		t = t.waitingOn.owner
	}
}

// waitCycle reports whether t waiting on a lock held by owner would
// close a wait-for cycle, and if so returns the cycle starting at t.
func waitCycle(t, owner *Task) []*Task {
	path := []*Task{t}
	for cur := owner; cur != nil; {
		if cur == t {
			return path
		}
		path = append(path, cur)
		if cur.waitingOn == nil {
			return nil
		}
		cur = cur.waitingOn.owner
	}
	return nil
}

func removeWaiter(l *Lock, t *Task) bool {
	for i, w := range l.waiters {
		if w.task == t {
			l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
			return true
		}
	}
	return false
}

func bestWaiter(l *Lock) *waiter {
	var best *waiter
	for _, w := range l.waiters {
		if best == nil || w.task.eff > best.task.eff {
			best = w
		}
	}
	return best
}

func (t *Task) String() string {
	return fmt.Sprintf("%s(base=%d,eff=%d)", t.Name, t.base, t.eff)
}
