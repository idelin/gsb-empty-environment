package netsec

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrEmptyTarget is returned when a transaction is created without any
	// target thread.
	ErrEmptyTarget = errors.New("netsec: attach transaction has no target threads")
	// ErrBarrierFailed is returned (or used to wrap a cause) when the
	// generation barrier cannot be satisfied, for example because the target
	// process exited.
	ErrBarrierFailed = errors.New("netsec: attach barrier failed")
	// ErrBarrierNotOpen is returned when registers are read before the
	// same-generation stop barrier has completed successfully.
	ErrBarrierNotOpen = errors.New("netsec: attach barrier has not been reached")
	// ErrTxClosed is returned when a transaction is used after Close.
	ErrTxClosed = errors.New("netsec: attach transaction is closed")
)

// txThread tracks one thread participating in a transaction's barrier.
type txThread struct {
	tid       TID
	threadGen Gen
	// owned records that this transaction drove the stop and therefore must
	// resume the thread on rollback/close. Stops merely observed (owned by
	// another tracer) are never resumed by us.
	owned     bool
	confirmed bool
	exited    bool
}

// AttachTx is one attach transaction. It binds together:
//   - a trusted process generation (the ProcHandle),
//   - a generation-bound debug authorization,
//   - the set of target threads (extended automatically for new clones), and
//   - the tracer that owns the stops made by this transaction.
//
// Register reads are permitted only after the barrier has confirmed a
// same-generation stop for every target thread. Late confirmations that refer
// to an old generation or an unknown thread are ignored.
type AttachTx struct {
	kernel *Kernel
	handle *ProcHandle
	auth   Auth
	tracer int

	mu      sync.Mutex
	threads map[TID]*txThread
	open    bool // barrier reached successfully
	closed  bool
	failErr error
	exited  bool
}

// NewAttachTx creates a transaction and immediately requests the initial set of
// target threads to stop. New threads created afterwards are discovered through
// clone events and join the same barrier.
//
// The call fails fast if the handle, authorization or initial thread set is
// invalid; nothing has been suspended in that case.
func NewAttachTx(k *Kernel, h *ProcHandle, auth Auth, tids []TID) (*AttachTx, error) {
	if len(tids) == 0 {
		return nil, ErrEmptyTarget
	}
	if !h.Alive() {
		return nil, ErrUnknownProcess
	}
	if auth.gen != h.gen || auth.pid != h.pid {
		return nil, ErrAuthStale
	}

	tx := &AttachTx{
		kernel:  k,
		handle:  h,
		auth:    auth,
		tracer:  auth.tracer,
		threads: make(map[TID]*txThread),
	}

	// Validate and register the initial set before any suspend, so a bad TID
	// aborts without side effects.
	k.mu.Lock()
	proc, ok := k.processes[h.pid]
	if !ok || !proc.alive || proc.gen != h.gen {
		k.mu.Unlock()
		return nil, ErrAuthStale
	}
	if err := k.checkAuthLocked(auth, proc); err != nil {
		k.mu.Unlock()
		return nil, err
	}
	for _, tid := range tids {
		th, ok := proc.threads[tid]
		if !ok || th.state == ThreadExited {
			k.mu.Unlock()
			return nil, ErrUnknownThread
		}
		if _, dup := tx.threads[tid]; dup {
			continue
		}
		tx.threads[tid] = &txThread{tid: tid, threadGen: th.threadID}
	}
	k.mu.Unlock()

	// Issue the initial suspends. Any thread we actually stop is marked owned.
	for _, entry := range tx.threads {
		outcome, err := k.RequestSuspend(auth, entry.tid)
		if err != nil {
			tx.rollback()
			return nil, err
		}
		entry.owned = outcome == SuspendedByMe
		if outcome == ThreadGone {
			entry.exited = true
		}
	}
	return tx, nil
}

// pumpEvents drains the tracer's kernel event queue and applies every event to
// the transaction. It is safe to call repeatedly; production waiting loops use
// it while tests call it directly for deterministic sequencing.
func (tx *AttachTx) pumpEvents() int {
	events := tx.kernel.DrainEvents(tx.tracer)
	for _, ev := range events {
		tx.handleEvent(ev)
	}
	return len(events)
}

func (tx *AttachTx) handleEvent(ev Event) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return
	}
	// Reject anything that does not belong to the bound incarnation. This is
	// the core PID-reuse defense: a late stop from the next tenant reusing
	// the numeric PID carries a different generation.
	if ev.PID != tx.handle.pid || ev.Gen != tx.handle.gen {
		return
	}

	switch ev.Kind {
	case EventExit:
		tx.exited = true
		if tx.failErr == nil {
			tx.failErr = ErrBarrierFailed
		}
	case EventThreadExit:
		entry, ok := tx.threads[ev.TID]
		if !ok {
			return
		}
		// Only an exit for the exact tracked identity counts; a recycled TID
		// belonging to a replacement thread carries a different identity.
		if ev.ThreadGen != entry.threadGen {
			return
		}
		entry.exited = true
		entry.confirmed = false
	case EventClone:
		// A new thread in the same generation joins the barrier. A recycled
		// TID still matches because it carries the current generation. An
		// exited entry with the same numeric TID is replaced by the newcomer.
		if existing, exists := tx.threads[ev.TID]; exists && !existing.exited {
			return
		}
		entry := &txThread{tid: ev.TID, threadGen: ev.ThreadGen}
		tx.threads[ev.TID] = entry
		tx.mu.Unlock()
		outcome, err := tx.kernel.RequestSuspend(tx.auth, ev.TID)
		tx.mu.Lock()
		if err != nil || outcome == ThreadGone {
			entry.exited = true
			return
		}
		entry.owned = outcome == SuspendedByMe
	case EventStop:
		entry, ok := tx.threads[ev.TID]
		if !ok {
			// Late stop for a thread this transaction never tracked.
			return
		}
		// Same-generation confirmation only. A reused TID owned by a later
		// thread has a different thread generation and must not count.
		if ev.ThreadGen != entry.threadGen {
			return
		}
		entry.confirmed = true
	}
}

// barrierSatisfiedLocked reports whether every still-live target thread has a
// same-generation stop confirmation. Exited threads no longer participate.
func (tx *AttachTx) barrierSatisfiedLocked() bool {
	if tx.exited {
		return false
	}
	for _, entry := range tx.threads {
		if entry.exited {
			continue
		}
		if !entry.confirmed {
			return false
		}
	}
	return true
}

// WaitBarrier blocks until every target thread has confirmed a same-generation
// stop, the context expires, or the target generation ends. On failure it
// rolls back only the stops owned by this transaction.
func (tx *AttachTx) WaitBarrier(ctx context.Context) error {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return ErrTxClosed
	}
	if tx.open {
		tx.mu.Unlock()
		return nil
	}
	tx.mu.Unlock()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		tx.pumpEvents()

		tx.mu.Lock()
		if tx.exited {
			cause := tx.failErr
			tx.mu.Unlock()
			tx.rollback()
			return cause
		}
		if tx.barrierSatisfiedLocked() {
			tx.open = true
			tx.mu.Unlock()
			return nil
		}
		tx.mu.Unlock()

		select {
		case <-ctx.Done():
			tx.rollback()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ReadRegisters reads one target thread's registers. It is allowed only after
// the barrier has opened and while the bound generation is still live, so a
// reused PID belonging to a new tenant can never be read through this
// transaction.
func (tx *AttachTx) ReadRegisters(tid TID) (Regs, error) {
	tx.mu.Lock()
	if tx.exited {
		tx.mu.Unlock()
		return Regs{}, ErrAuthStale
	}
	if tx.closed {
		tx.mu.Unlock()
		return Regs{}, ErrTxClosed
	}
	if !tx.open {
		tx.mu.Unlock()
		return Regs{}, ErrBarrierNotOpen
	}
	entry, ok := tx.threads[tid]
	if !ok || entry.exited {
		tx.mu.Unlock()
		return Regs{}, ErrUnknownThread
	}
	tx.mu.Unlock()

	// The kernel independently re-validates the generation, authorization and
	// stop ownership, giving defense in depth against a stale transaction.
	return tx.kernel.ReadRegisters(tx.auth, tid)
}

// PendingCount reports how many live target threads still lack a confirmed
// same-generation stop. Intended for tests and diagnostics.
func (tx *AttachTx) PendingCount() int {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	pending := 0
	for _, entry := range tx.threads {
		if !entry.exited && !entry.confirmed {
			pending++
		}
	}
	return pending
}

// Close ends the transaction, resuming exactly the threads this transaction
// suspended. Stops owned by other tracers are left untouched. It is idempotent.
func (tx *AttachTx) Close() {
	tx.rollback()
}

// rollback resumes only owned stops and marks the transaction closed.
func (tx *AttachTx) rollback() {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return
	}
	tx.closed = true
	tx.open = false
	owned := make([]TID, 0, len(tx.threads))
	for _, entry := range tx.threads {
		if entry.owned && !entry.exited {
			owned = append(owned, entry.tid)
		}
	}
	tx.mu.Unlock()

	for _, tid := range owned {
		// A process exit or thread exit makes Resume a no-op/error; ignore it.
		_ = tx.kernel.Resume(tx.auth, tid)
	}
}
