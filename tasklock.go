// Package tasklock implements task-level mutexes with priority inheritance.
//
// A Scheduler owns a set of Tasks and Mutexes. When a low-priority task
// holds a mutex that a higher-priority task blocks on, the holder's
// effective priority is temporarily boosted to the waiter's priority.
// The boost propagates transitively across multiple locks (if A waits on
// a lock held by B, and B waits on a lock held by C, C inherits A's
// priority). Cancellation or timeout of a waiter revokes the boost, and
// releasing one lock never erases inheritance contributed by waiters on
// other locks the holder still owns. Lock attempts that would close a
// wait-for cycle are rejected with a DeadlockError describing the cycle.
//
// All protocol state is guarded by a single sync.Mutex; blocked waiters
// park on per-task channels. Only the Go standard library is used.
package tasklock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrNotOwner is returned by Unlock when the task does not hold the mutex.
var ErrNotOwner = errors.New("tasklock: unlock by non-owner")

// DeadlockError reports a lock attempt that would close a wait-for cycle.
// Cycle lists the involved task names in wait-for order, with the first
// element repeated at the end (e.g. [a b a] means a waits on b's lock
// while b waits on a's lock).
type DeadlockError struct {
	Cycle []string
}

func (e *DeadlockError) Error() string {
	return "tasklock: deadlock detected: " + strings.Join(e.Cycle, " -> ")
}

// Scheduler creates tasks and mutexes and serializes the inheritance
// protocol. The zero value is ready to use.
type Scheduler struct {
	mu sync.Mutex
}

// Task is a schedulable unit with a fixed base priority. Higher numeric
// priority means more urgent. A Task's effective priority is its base
// priority raised by priority inheritance while it blocks other tasks.
type Task struct {
	sched *Scheduler
	name  string
	base  int

	// guarded by sched.mu
	waitingOn *Mutex             // mutex this task is blocked on, if any
	donors    map[*Task]struct{} // tasks directly blocked on locks this task holds
	grant     chan struct{}      // buffered(1); signaled when a blocked Lock is granted
}

// Mutex is a task-level lock owned by at most one Task at a time.
type Mutex struct {
	sched *Scheduler
	name  string

	// guarded by sched.mu
	owner   *Task
	waiters map[*Task]struct{}
}

// NewTask creates a task with the given name and base priority.
func (s *Scheduler) NewTask(name string, basePriority int) *Task {
	return &Task{
		sched:  s,
		name:   name,
		base:   basePriority,
		donors: make(map[*Task]struct{}),
		grant:  make(chan struct{}, 1),
	}
}

// NewMutex creates a mutex with the given diagnostic name.
func (s *Scheduler) NewMutex(name string) *Mutex {
	return &Mutex{sched: s, name: name, waiters: make(map[*Task]struct{})}
}

// Name returns the task's name.
func (t *Task) Name() string { return t.name }

// BasePriority returns the task's fixed base priority.
func (t *Task) BasePriority() int { return t.base }

// EffectivePriority returns the task's current priority including
// inherited boosts from tasks blocked (directly or transitively) on
// mutexes this task holds.
func (t *Task) EffectivePriority() int {
	t.sched.mu.Lock()
	defer t.sched.mu.Unlock()
	return effectivePrio(t, make(map[*Task]int))
}

// BlockedOn returns the mutex the task is currently blocked on, or nil.
func (t *Task) BlockedOn() *Mutex {
	t.sched.mu.Lock()
	defer t.sched.mu.Unlock()
	return t.waitingOn
}

// Name returns the mutex's diagnostic name.
func (m *Mutex) Name() string { return m.name }

// Owner returns the task currently holding the mutex, or nil.
func (m *Mutex) Owner() *Task {
	m.sched.mu.Lock()
	defer m.sched.mu.Unlock()
	return m.owner
}

// NumWaiters returns the number of tasks blocked on the mutex.
func (m *Mutex) NumWaiters() int {
	m.sched.mu.Lock()
	defer m.sched.mu.Unlock()
	return len(m.waiters)
}

// effectivePrio computes max(base, max effective priority of donors).
// The donor graph is acyclic because cycle-closing waits are rejected,
// so plain recursion with a memo terminates.
func effectivePrio(t *Task, memo map[*Task]int) int {
	if p, ok := memo[t]; ok {
		return p
	}
	p := t.base
	for d := range t.donors {
		if dp := effectivePrio(d, memo); dp > p {
			p = dp
		}
	}
	memo[t] = p
	return p
}

// findCycle walks the wait-for chain starting at m's owner. If adding
// "waiter waits on m" would close a cycle, it returns the cycle path
// (task names, first repeated at the end); otherwise it returns nil.
func findCycle(waiter *Task, m *Mutex) []string {
	path := []string{waiter.name}
	for cur := m.owner; cur != nil; {
		path = append(path, cur.name)
		if cur == waiter {
			return path
		}
		if cur.waitingOn == nil {
			return nil
		}
		cur = cur.waitingOn.owner
	}
	return nil
}

// Lock acquires m for task t, blocking until the mutex is free, the
// context is canceled, or its deadline expires. While blocked, t donates
// its effective priority to the holder chain. A canceled/timed-out wait
// revokes the donation and returns ctx.Err(). A wait that would close a
// wait-for cycle fails immediately with *DeadlockError.
func (s *Scheduler) Lock(ctx context.Context, t *Task, m *Mutex) error {
	s.mu.Lock()
	switch {
	case m.owner == nil:
		m.owner = t
		s.mu.Unlock()
		return nil
	case m.owner == t:
		cycle := []string{t.name, t.name}
		s.mu.Unlock()
		return &DeadlockError{Cycle: cycle}
	}
	if cycle := findCycle(t, m); cycle != nil {
		s.mu.Unlock()
		return &DeadlockError{Cycle: cycle}
	}
	t.waitingOn = m
	m.waiters[t] = struct{}{}
	m.owner.donors[t] = struct{}{}
	s.mu.Unlock()

	select {
	case <-t.grant:
		// Ownership was transferred to t under s.mu by Unlock.
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		if t.waitingOn != m {
			// The grant raced with cancellation and won; t owns the lock.
			s.mu.Unlock()
			return nil
		}
		delete(m.waiters, t)
		delete(m.owner.donors, t)
		t.waitingOn = nil
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Unlock releases m, which must be held by t. If tasks are waiting, the
// highest-effective-priority waiter is granted ownership; ties are broken
// arbitrarily. Inheritance from waiters on other locks t still holds is
// unaffected.
func (s *Scheduler) Unlock(t *Task, m *Mutex) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.owner != t {
		return fmt.Errorf("%w: task %q does not hold mutex %q", ErrNotOwner, t.name, m.name)
	}
	var next *Task
	for w := range m.waiters {
		if next == nil || effectivePrio(w, make(map[*Task]int)) > effectivePrio(next, make(map[*Task]int)) {
			next = w
		}
	}
	if next == nil {
		m.owner = nil
		return nil
	}
	delete(m.waiters, next)
	delete(t.donors, next)
	next.waitingOn = nil
	m.owner = next
	next.grant <- struct{}{} // buffered; a task waits on at most one mutex
	return nil
}
