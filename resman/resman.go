// Package resman implements a multi-resource integer token manager.
//
// Semantics:
//   - A single Acquire requests a vector of integer resources and is granted
//     only when EVERY component is available. Partial grants never happen.
//   - Requests are served in strict FIFO order: a queued request that cannot
//     be satisfied blocks every request queued behind it (head-of-line
//     blocking), even if a later request would fit.
//   - Cancelling the context of a waiting Acquire only removes it from the
//     queue. Once a request has been granted, the tokens are owned by the
//     caller and are NEVER auto-released by context cancellation; the caller
//     must explicitly call Grant.Release to return them.
package resman

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
)

// Vector is an integer resource vector. Its length must equal the manager's
// dimension and every component must be non-negative.
type Vector []int64

// ErrInvalidVector is returned when a request vector has the wrong dimension,
// contains a negative component, or exceeds the manager's capacity (a request
// that can never be satisfied).
var ErrInvalidVector = errors.New("resman: invalid request vector")

type waiter struct {
	need    Vector
	ch      chan struct{} // closed exactly once when granted
	granted bool
	elem    *list.Element
}

// Manager hands out integer tokens for a fixed set of resource classes.
type Manager struct {
	mu        sync.Mutex
	capacity  Vector
	available Vector
	granted   Vector     // tokens currently checked out; available+granted == capacity
	queue     *list.List // FIFO of *waiter
}

// New creates a Manager whose capacity per resource class is given by
// capacity. All components must be non-negative.
func New(capacity Vector) (*Manager, error) {
	for _, c := range capacity {
		if c < 0 {
			return nil, fmt.Errorf("resman: negative capacity: %v", []int64(capacity))
		}
	}
	m := &Manager{
		capacity:  append(Vector(nil), capacity...),
		available: append(Vector(nil), capacity...),
		granted:   make(Vector, len(capacity)),
		queue:     list.New(),
	}
	return m, nil
}

// Dim returns the number of resource classes.
func (m *Manager) Dim() int { return len(m.capacity) }

// Available returns a snapshot of the currently available tokens.
func (m *Manager) Available() Vector {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append(Vector(nil), m.available...)
}

// Capacity returns a copy of the manager's capacity vector.
func (m *Manager) Capacity() Vector {
	return append(Vector(nil), m.capacity...)
}

// CheckConservation verifies the token conservation invariant
//
//	available[i] + granted[i] == capacity[i]  for every resource class i,
//
// with available and granted both non-negative. Any partial grant, lost
// token, or duplicated release would break this invariant. It is safe to
// call concurrently; the check is performed atomically under the manager
// lock.
func (m *Manager) CheckConservation() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.capacity {
		if m.available[i] < 0 {
			return fmt.Errorf("resman: available[%d]=%d is negative", i, m.available[i])
		}
		if m.granted[i] < 0 {
			return fmt.Errorf("resman: granted[%d]=%d is negative", i, m.granted[i])
		}
		if m.available[i]+m.granted[i] != m.capacity[i] {
			return fmt.Errorf("resman: conservation violated at %d: available %d + granted %d != capacity %d",
				i, m.available[i], m.granted[i], m.capacity[i])
		}
	}
	return nil
}

func (m *Manager) validate(need Vector) error {
	if len(need) != len(m.capacity) {
		return fmt.Errorf("%v: dim %d, want %d", ErrInvalidVector, len(need), len(m.capacity))
	}
	for i, n := range need {
		if n < 0 {
			return fmt.Errorf("%v: negative component at %d", ErrInvalidVector, i)
		}
		if n > m.capacity[i] {
			return fmt.Errorf("%v: component %d (%d) exceeds capacity %d", ErrInvalidVector, i, n, m.capacity[i])
		}
	}
	return nil
}

// fits reports whether need can be fully satisfied from available.
func (m *Manager) fits(need Vector) bool {
	for i, n := range need {
		if n > m.available[i] {
			return false
		}
	}
	return true
}

// tryGrantLocked grants as many requests as possible, strictly from the head
// of the queue. It stops at the first request that does not fit, so a blocked
// head blocks all successors. Callers must hold m.mu.
func (m *Manager) tryGrantLocked() {
	for {
		front := m.queue.Front()
		if front == nil {
			return
		}
		w := front.Value.(*waiter)
		if !m.fits(w.need) {
			return // head-of-line blocking
		}
		for i, n := range w.need {
			m.available[i] -= n
			m.granted[i] += n
		}
		m.queue.Remove(front)
		w.granted = true
		close(w.ch)
	}
}

// Grant represents a granted request. The tokens it holds are released only
// by an explicit call to Release; context cancellation never releases them.
type Grant struct {
	m        *Manager
	res      Vector
	release  sync.Once
	released chan struct{}
}

// Release returns the granted tokens to the manager. It is idempotent and
// safe to call multiple times; only the first call has an effect.
func (g *Grant) Release() {
	g.release.Do(func() {
		g.m.mu.Lock()
		for i, n := range g.res {
			g.m.available[i] += n
			g.m.granted[i] -= n
		}
		g.m.tryGrantLocked()
		g.m.mu.Unlock()
		close(g.released)
	})
}

// Released returns a channel closed once the grant has been released.
func (g *Grant) Released() <-chan struct{} { return g.released }

// Acquire waits until the full vector need can be granted, then returns a
// Grant owning those tokens.
//
// If ctx is cancelled while waiting, the pending request is removed from the
// FIFO queue and ctx.Err() is returned; no tokens are held in that case. If
// the request is granted concurrently with cancellation, the Grant is
// returned with a nil error and the caller still owns the tokens: granted
// resources are never implicitly reclaimed.
func (m *Manager) Acquire(ctx context.Context, need Vector) (*Grant, error) {
	if err := m.validate(need); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	w := &waiter{
		need: append(Vector(nil), need...),
		ch:   make(chan struct{}),
	}

	m.mu.Lock()
	w.elem = m.queue.PushBack(w)
	m.tryGrantLocked()
	m.mu.Unlock()

	select {
	case <-w.ch:
		return &Grant{m: m, res: w.need, released: make(chan struct{})}, nil
	case <-ctx.Done():
	}

	m.mu.Lock()
	if w.granted {
		// Lost the race with a grant: the tokens are ours now. Context
		// cancellation must not free in-use resources, so hand the grant
		// to the caller, which remains responsible for Release.
		m.mu.Unlock()
		return &Grant{m: m, res: w.need, released: make(chan struct{})}, nil
	}
	m.queue.Remove(w.elem)
	m.mu.Unlock()
	return nil, ctx.Err()
}
