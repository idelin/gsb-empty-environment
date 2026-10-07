// Package resman implements a FIFO multi-resource token manager.
//
// A request asks for a vector of integer resources and is granted only
// when every component is available (all-or-nothing, no partial grants).
// Waiting requests form a strict FIFO queue with head-of-line blocking:
// a later request is never considered while an earlier one is waiting.
// Granted tokens are held until explicitly released; cancelling the
// context of an already-granted request does not return tokens.
package resman

import (
	"container/list"
	"context"
	"fmt"
	"sync"
)

// Vector is a set of named integer resource amounts.
type Vector map[string]int64

// ImpossibleError is returned when a request can never be satisfied
// because it exceeds the manager's total capacity or names an unknown
// resource.
type ImpossibleError struct{ Reason string }

func (e *ImpossibleError) Error() string {
	return "resman: impossible request: " + e.Reason
}

// Manager hands out resource tokens. The zero value is not usable; use New.
type Manager struct {
	mu    sync.Mutex
	cap   Vector
	avail Vector
	queue *list.List // of *request, FIFO
}

type request struct {
	need    Vector
	granted chan struct{} // closed when the request is granted
	done    bool          // granted flag, guarded by mu
}

// New returns a Manager that owns the given capacity. The capacity vector
// is copied and defines the fixed set of resource names.
func New(capacity Vector) *Manager {
	m := &Manager{
		cap:   copyVector(capacity),
		avail: copyVector(capacity),
		queue: list.New(),
	}
	for name, n := range capacity {
		if n < 0 {
			panic(fmt.Sprintf("resman: negative capacity for %q", name))
		}
	}
	return m
}

// Grant represents an all-or-nothing allocation of tokens. Tokens stay
// allocated until Release is called, regardless of any context.
type Grant struct {
	m    *Manager
	vec  Vector
	once sync.Once
}

// Vector returns a copy of the granted amounts.
func (g *Grant) Vector() Vector { return copyVector(g.vec) }

// Release returns the granted tokens to the manager and wakes queued
// waiters in FIFO order. It is idempotent: only the first call has effect.
func (g *Grant) Release() {
	g.once.Do(func() {
		g.m.mu.Lock()
		defer g.m.mu.Unlock()
		addTo(g.m.avail, g.vec)
		g.m.drainLocked()
		g.m.assertConservedLocked()
	})
}

// Acquire blocks until the full need vector is granted, the context is
// done, or the request is provably impossible.
//
// If the context is cancelled while waiting, the request is dequeued and
// the context error is returned. If the grant races with cancellation and
// wins, a Grant is returned and the caller must Release it: context
// cancellation never frees tokens that are already in use.
func (m *Manager) Acquire(ctx context.Context, need Vector) (*Grant, error) {
	if err := m.validate(need); err != nil {
		return nil, err
	}
	vec := copyVector(need)

	m.mu.Lock()
	if m.queue.Len() == 0 && fits(m.avail, vec) {
		subFrom(m.avail, vec)
		m.assertConservedLocked()
		m.mu.Unlock()
		return &Grant{m: m, vec: vec}, nil
	}
	req := &request{need: vec, granted: make(chan struct{})}
	m.queue.PushBack(req)
	m.mu.Unlock()

	select {
	case <-req.granted:
		return &Grant{m: m, vec: vec}, nil
	case <-ctx.Done():
		m.mu.Lock()
		if req.done {
			// Granted concurrently with cancellation: tokens are
			// already deducted, hand them to the caller.
			m.mu.Unlock()
			return &Grant{m: m, vec: vec}, nil
		}
		for e := m.queue.Front(); e != nil; e = e.Next() {
			if e.Value.(*request) == req {
				m.queue.Remove(e)
				break
			}
		}
		m.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Snapshot returns the currently available amounts.
func (m *Manager) Snapshot() Vector {
	m.mu.Lock()
	defer m.mu.Unlock()
	return copyVector(m.avail)
}

// Queued reports how many requests are waiting.
func (m *Manager) Queued() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queue.Len()
}

// drainLocked grants the head of the queue while it fits; a non-fitting
// head blocks all successors (head-of-line blocking).
func (m *Manager) drainLocked() {
	for {
		e := m.queue.Front()
		if e == nil {
			return
		}
		req := e.Value.(*request)
		if !fits(m.avail, req.need) {
			return
		}
		subFrom(m.avail, req.need)
		m.queue.Remove(e)
		req.done = true
		close(req.granted)
	}
}

// assertConservedLocked verifies the conservation invariant: for every
// resource, available tokens stay within [0, capacity], i.e. tokens are
// neither created nor destroyed and grants are all-or-nothing.
func (m *Manager) assertConservedLocked() {
	for name, c := range m.cap {
		if a := m.avail[name]; a < 0 || a > c {
			panic(fmt.Sprintf("resman: conservation violated for %q: avail=%d capacity=%d", name, a, c))
		}
	}
}

func (m *Manager) validate(need Vector) error {
	for name, n := range need {
		if n < 0 {
			return fmt.Errorf("resman: negative amount for %q", name)
		}
		c, ok := m.cap[name]
		if !ok {
			return &ImpossibleError{Reason: fmt.Sprintf("unknown resource %q", name)}
		}
		if n > c {
			return &ImpossibleError{Reason: fmt.Sprintf("%q needs %d, capacity %d", name, n, c)}
		}
	}
	return nil
}

func fits(avail, need Vector) bool {
	for name, n := range need {
		if avail[name] < n {
			return false
		}
	}
	return true
}

func copyVector(v Vector) Vector {
	out := make(Vector, len(v))
	for name, n := range v {
		out[name] = n
	}
	return out
}

func addTo(dst, src Vector) {
	for name, n := range src {
		dst[name] += n
	}
}

func subFrom(dst, src Vector) {
	for name, n := range src {
		dst[name] -= n
	}
}
