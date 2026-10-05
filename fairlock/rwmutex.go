// Package fairlock implements a fair, cancellable reader-writer mutex
// with an atomic write-to-read downgrade.
//
// Fairness: waiters are served in FIFO order. Once a writer is queued,
// later readers queue behind it, so a queued writer can never be
// starved by a stream of later readers.
//
// Cancellation: Lock and RLock take a context. A blocked waiter whose
// context is done stops waiting and returns ctx.Err(), unless its grant
// was already linearized (see the tie-break rule below).
//
// Tie-break rule (grant vs. cancel): every grant and every cancellation
// is linearized under the lock's internal mutex. A grant linearizes when
// the scheduler marks the waiter granted; a cancellation linearizes when
// the waiter's cancel path, holding the internal mutex, observes that it
// is not yet granted and dequeues it. Whichever linearizes first wins.
// Because both decisions are serialized on the same mutex, exactly one
// of them wins and the outcome is unambiguous.
//
// Downgrade: Downgrade converts a held write lock into a read lock
// atomically. The state transition happens inside a single critical
// section of the internal mutex, so no other goroutine can observe the
// lock as unheld in between: there is no lock-free gap.
package fairlock

import (
	"context"
	"sync"
)

type waiterKind uint8

const (
	kindReader waiterKind = iota
	kindWriter
)

// waiter is one queued Lock or RLock request.
type waiter struct {
	kind waiterKind
	// token has capacity 1. The scheduler sends exactly one token, at the
	// moment the grant is linearized, while holding mu. The send can never
	// block and the token can never be lost: if the waiter has already
	// taken the cancel path, that path observes granted == true under mu
	// and honors the grant instead of abandoning it.
	token chan struct{}
	// granted is set under mu when the grant linearizes. Once true,
	// cancellation loses the tie-break.
	granted bool
}

// RWMutex is a fair reader-writer lock. The zero value is ready to use.
//
// The internal state machine is:
//
//	writing bool   // a write token is outstanding
//	readers int    // number of outstanding read shares
//	queue   FIFO   // waiters not yet granted
//
// All state transitions happen under mu, and every transition that could
// make a queued waiter grantable is immediately followed by a run of the
// scheduler (scheduleLocked) inside the same critical section.
type RWMutex struct {
	mu      sync.Mutex
	writing bool
	readers int
	queue   []*waiter
}

// Lock acquires the write lock, blocking until it is granted or ctx is
// done. On success it returns nil and the caller holds the write lock.
// On cancellation it returns ctx.Err() and the caller holds nothing.
//
// If the lock can be granted immediately, Lock succeeds even if ctx is
// already done: an uncontended acquisition never fails.
func (m *RWMutex) Lock(ctx context.Context) error {
	w := &waiter{kind: kindWriter, token: make(chan struct{}, 1)}

	m.mu.Lock()
	if !m.writing && m.readers == 0 && len(m.queue) == 0 {
		// Fast path: the grant linearizes immediately.
		m.writing = true
		m.mu.Unlock()
		return nil
	}
	m.queue = append(m.queue, w)
	m.mu.Unlock()

	select {
	case <-w.token:
		return nil
	case <-ctx.Done():
	}

	m.mu.Lock()
	if w.granted {
		// The grant linearized before the cancellation: grant wins.
		m.mu.Unlock()
		return nil
	}
	// The cancellation linearizes now: cancel wins. Dequeue and re-run
	// the scheduler so a successor that became grantable is not stranded.
	m.removeLocked(w)
	m.scheduleLocked()
	m.mu.Unlock()
	return ctx.Err()
}

// RLock acquires a read share, blocking until it is granted or ctx is
// done. Semantics mirror Lock. A reader never barges ahead of a queued
// writer: if the queue is non-empty, RLock queues behind it.
func (m *RWMutex) RLock(ctx context.Context) error {
	w := &waiter{kind: kindReader, token: make(chan struct{}, 1)}

	m.mu.Lock()
	if !m.writing && len(m.queue) == 0 {
		// Fast path: the grant linearizes immediately.
		m.readers++
		m.mu.Unlock()
		return nil
	}
	m.queue = append(m.queue, w)
	m.mu.Unlock()

	select {
	case <-w.token:
		return nil
	case <-ctx.Done():
	}

	m.mu.Lock()
	if w.granted {
		// The grant linearized before the cancellation: grant wins.
		m.mu.Unlock()
		return nil
	}
	// Cancel wins; see Lock.
	m.removeLocked(w)
	m.scheduleLocked()
	m.mu.Unlock()
	return ctx.Err()
}

// Unlock releases a held write lock. It panics if the write lock is not
// held.
func (m *RWMutex) Unlock() {
	m.mu.Lock()
	if !m.writing {
		m.mu.Unlock()
		panic("fairlock: Unlock of unheld write lock")
	}
	m.writing = false
	m.scheduleLocked()
	m.mu.Unlock()
}

// RUnlock releases one read share. It panics if no read share is held.
func (m *RWMutex) RUnlock() {
	m.mu.Lock()
	if m.readers == 0 {
		m.mu.Unlock()
		panic("fairlock: RUnlock of unheld read lock")
	}
	m.readers--
	if m.readers == 0 {
		m.scheduleLocked()
	}
	m.mu.Unlock()
}

// Downgrade atomically converts a held write lock into a read share.
// After Downgrade returns, the caller holds a read share and must
// release it with RUnlock. It panics if the write lock is not held.
//
// The conversion happens inside one critical section of mu: writing is
// cleared and readers is incremented before mu is released, and the
// scheduler only grants a writer when it observes readers == 0 under mu.
// Hence no goroutine can ever observe the lock as completely unheld
// during a downgrade: there is no lock-free gap.
func (m *RWMutex) Downgrade() {
	m.mu.Lock()
	if !m.writing {
		m.mu.Unlock()
		panic("fairlock: Downgrade of unheld write lock")
	}
	m.writing = false
	m.readers++
	m.scheduleLocked()
	m.mu.Unlock()
}

// scheduleLocked grants every waiter that has become eligible, in FIFO
// order. It must be called with mu held, after any state change that
// could make the head of the queue grantable.
//
// A consecutive run of readers at the head is granted together; a writer
// is granted only when the lock is completely free, and grants stop
// after it because a writer is exclusive.
func (m *RWMutex) scheduleLocked() {
	for len(m.queue) > 0 {
		w := m.queue[0]
		if w.kind == kindWriter {
			if m.writing || m.readers > 0 {
				return
			}
			m.queue = m.queue[1:]
			w.granted = true
			m.writing = true
			w.token <- struct{}{}
			return
		}
		if m.writing {
			return
		}
		m.queue = m.queue[1:]
		w.granted = true
		m.readers++
		w.token <- struct{}{}
	}
}

// removeLocked dequeues w. It must be called with mu held, and only for
// a waiter that is still queued (granted == false).
func (m *RWMutex) removeLocked(w *waiter) {
	for i, q := range m.queue {
		if q == w {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
	panic("fairlock: cancel of unqueued waiter")
}
