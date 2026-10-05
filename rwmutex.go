// Package fairlock implements a fair, cancellable read-write mutex with
// atomic write-to-read downgrade.
//
// Fairness: waiters are served in FIFO order. Once a writer is queued,
// readers that arrive later queue behind it, so a queued writer can
// never be starved by a stream of later readers. A queued writer waits
// at most for the currently active readers plus the readers that were
// queued ahead of it.
//
// Tie-breaking rule (grant vs. cancel): the handoff of the lock to a
// waiter is committed atomically under the internal mutex. If the grant
// is committed before the cancellation is processed, the grant wins:
// Lock/RLock return nil even if the context is already cancelled, and
// the caller owns the lock and must release it. If the cancellation is
// processed first, the waiter is removed from the queue and the grant
// never happens. Because both decisions are serialized by the internal
// mutex, exactly one of the two outcomes occurs; there is no
// interleaving in which both happen (lock leaked to a cancelled
// waiter) or neither happens (lost wakeup).
package fairlock

import (
	"container/list"
	"context"
	"sync"
)

type waiterKind int8

const (
	readerKind waiterKind = iota
	writerKind
)

// waiter represents one queued Lock/RLock call.
type waiter struct {
	kind waiterKind
	// ch is closed exactly once, when the grant is committed.
	ch chan struct{}
	// granted records that the handoff has been committed. Guarded by
	// RWMutex.mu, as is every field of RWMutex itself.
	granted bool
}

// RWMutex is a fair, cancellable read-write mutex. The zero value is
// ready to use. It must not be copied after first use.
type RWMutex struct {
	mu      sync.Mutex
	readers int       // active readers, including handed-off but not yet noticed grants
	writer  bool      // a writer holds the lock, or the write handoff is committed
	queue   list.List // FIFO of *waiter
}

// Lock acquires the write lock, waiting in FIFO order. It returns
// ctx.Err() if the wait is cancelled before the grant is committed;
// see the package documentation for the grant-vs-cancel tie rule.
func (m *RWMutex) Lock(ctx context.Context) error {
	return m.acquire(ctx, writerKind)
}

// RLock acquires the read lock, waiting in FIFO order behind any
// queued writer. It returns ctx.Err() if the wait is cancelled before
// the grant is committed.
func (m *RWMutex) RLock(ctx context.Context) error {
	return m.acquire(ctx, readerKind)
}

func (m *RWMutex) acquire(ctx context.Context, kind waiterKind) error {
	m.mu.Lock()
	if m.queue.Len() == 0 && m.grantableLocked(kind) {
		m.takeLocked(kind)
		m.mu.Unlock()
		return nil
	}
	w := &waiter{kind: kind, ch: make(chan struct{})}
	e := m.queue.PushBack(w)
	m.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
	}

	// Cancellation path. The outcome is decided under mu: if the grant
	// was committed first, the grant wins and we now own the lock.
	m.mu.Lock()
	if w.granted {
		m.mu.Unlock()
		return nil
	}
	m.queue.Remove(e)
	// Removing ourselves may unblock waiters behind us (e.g. a
	// cancelled writer at the front unblocks following readers).
	m.grantLocked()
	m.mu.Unlock()
	return ctx.Err()
}

// grantableLocked reports whether a waiter of the given kind may be
// granted the lock in the current state. Caller must hold m.mu.
func (m *RWMutex) grantableLocked(kind waiterKind) bool {
	if kind == readerKind {
		return !m.writer
	}
	return !m.writer && m.readers == 0
}

// takeLocked records the lock as held by a new owner of the given
// kind. Caller must hold m.mu.
func (m *RWMutex) takeLocked(kind waiterKind) {
	if kind == readerKind {
		m.readers++
	} else {
		m.writer = true
	}
}

// grantLocked hands the lock to as many waiters from the front of the
// FIFO queue as currently possible. It stops at the first waiter that
// cannot be granted, so a queued writer blocks all later readers and
// at most one writer is ever granted. Caller must hold m.mu.
func (m *RWMutex) grantLocked() {
	for m.queue.Len() > 0 {
		e := m.queue.Front()
		w := e.Value.(*waiter)
		if !m.grantableLocked(w.kind) {
			return
		}
		m.queue.Remove(e)
		m.takeLocked(w.kind)
		w.granted = true
		close(w.ch)
		if w.kind == writerKind {
			return
		}
	}
}

// Unlock releases the write lock and grants the lock to the next
// waiter(s) in FIFO order. It panics if the write lock is not held.
func (m *RWMutex) Unlock() {
	m.mu.Lock()
	if !m.writer {
		m.mu.Unlock()
		panic("fairlock: Unlock of unlocked RWMutex")
	}
	m.writer = false
	m.grantLocked()
	m.mu.Unlock()
}

// RUnlock releases one read hold and, if it was the last reader,
// grants the lock to the next waiter(s). It panics if no read lock is
// held.
func (m *RWMutex) RUnlock() {
	m.mu.Lock()
	if m.readers == 0 {
		m.mu.Unlock()
		panic("fairlock: RUnlock of unlocked RWMutex")
	}
	m.readers--
	if m.readers == 0 {
		m.grantLocked()
	}
	m.mu.Unlock()
}

// Downgrade atomically converts a held write lock into a read lock.
// The caller must hold the write lock and must subsequently call
// RUnlock, not Unlock.
//
// The conversion and the wakeup of queued readers happen in the same
// critical section, so there is no unlocked gap: no writer can slip in
// between the release of the write hold and the acquisition of the
// read hold, and queued readers are granted as part of the same
// atomic step.
func (m *RWMutex) Downgrade() {
	m.mu.Lock()
	if !m.writer {
		m.mu.Unlock()
		panic("fairlock: Downgrade of non-write-held RWMutex")
	}
	m.writer = false
	m.readers++
	m.grantLocked()
	m.mu.Unlock()
}
