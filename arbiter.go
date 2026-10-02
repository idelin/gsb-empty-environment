package aries

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrAborted is returned to a transaction that was wounded by the conflict
// arbiter (a younger transaction conflicting with an older requester).
var ErrAborted = errors.New("aries: transaction wounded by conflict arbiter")

// LockMode is the granularity of a lock request.
type LockMode uint8

// Lock modes.
const (
	Shared LockMode = iota + 1
	Exclusive
)

func modesConflict(a, b LockMode) bool { return a == Exclusive || b == Exclusive }

// LockManager is the multi-node conflict arbiter. Many executor goroutines
// ("nodes") contend for keyed locks; the manager guarantees:
//
//   - Linearizability: every state transition happens under one mutex, so
//     the grant history is a legal serial history.
//   - Deadlock freedom: wound-wait — an older requester (smaller txn ID)
//     aborts conflicting younger transactions; a younger requester waits.
//     Waits-for edges therefore only ever point from young to old, which
//     makes cycles impossible.
//   - No goroutine leaks: the manager spawns no background goroutines and a
//     blocked Acquire is always released, either by a grant or by a wound.
//   - Fairness: waiters are served FIFO; shared locks may be co-granted.
type LockManager struct {
	mu      sync.Mutex
	entries map[string]*lockEntry
	txns    map[int64]*txnState
	wounds  atomic.Int64
	grants  atomic.Int64
}

type lockEntry struct {
	key     string
	holders map[int64]LockMode
	waiters []*waiter // FIFO
}

type waiter struct {
	txnID   int64
	mode    LockMode
	upgrade bool
	notify  chan struct{} // closed exactly once: on grant or on wound
}

type txnState struct {
	aborted bool
	held    map[string]LockMode
	waiting *waiter // non-nil while blocked in Acquire
	waitKey string  // entry key for waiting
}

// NewLockManager returns an empty arbiter.
func NewLockManager() *LockManager {
	return &LockManager{
		entries: make(map[string]*lockEntry),
		txns:    make(map[int64]*txnState),
	}
}

// waiters reports the queue length for a key; used by tests to synchronize
// deterministic interleavings.
func (lm *LockManager) waiters(key string) int {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if e, ok := lm.entries[key]; ok {
		return len(e.waiters)
	}
	return 0
}

// Stats reports cumulative grants and wounds (for tests and the demo).
func (lm *LockManager) Stats() (grants, wounds int64) {
	return lm.grants.Load(), lm.wounds.Load()
}

// Pending reports internal queue sizes; a fully drained manager reports
// zeros, which tests use to assert the absence of leaks.
func (lm *LockManager) Pending() (entries, txns int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return len(lm.entries), len(lm.txns)
}

func (lm *LockManager) stateLocked(txnID int64) *txnState {
	st, ok := lm.txns[txnID]
	if !ok {
		st = &txnState{held: make(map[string]LockMode)}
		lm.txns[txnID] = st
	}
	return st
}

func (lm *LockManager) entryLocked(key string) *lockEntry {
	e, ok := lm.entries[key]
	if !ok {
		e = &lockEntry{key: key, holders: make(map[int64]LockMode)}
		lm.entries[key] = e
	}
	return e
}

// grantableLocked reports whether a fresh (non-upgrade) request may be
// granted immediately: no queued waiters (FIFO fairness) and no conflicting
// holder.
func (lm *LockManager) grantableLocked(e *lockEntry, txnID int64, mode LockMode) bool {
	if len(e.waiters) > 0 {
		return false
	}
	for h, m := range e.holders {
		if h != txnID && modesConflict(m, mode) {
			return false
		}
	}
	return true
}

// abortLocked marks a transaction aborted and, if it is blocked in Acquire,
// removes its waiter and wakes it so it can observe ErrAborted.
func (lm *LockManager) abortLocked(st *txnState, txnID int64) {
	if st.aborted {
		return
	}
	st.aborted = true
	lm.wounds.Add(1)
	if st.waiting != nil {
		e := lm.entries[st.waitKey]
		lm.removeWaiterLocked(e, st.waiting)
		st.waiting = nil
		lm.grantWaitersLocked(e)
	}
}

func (lm *LockManager) removeWaiterLocked(e *lockEntry, target *waiter) {
	for i, w := range e.waiters {
		if w == target {
			e.waiters = append(e.waiters[:i], e.waiters[i+1:]...)
			close(w.notify)
			return
		}
	}
}

// woundLocked aborts every *younger* transaction (larger txn ID) that
// conflicts with the request: holders keep their locks until they release,
// waiters are dequeued immediately.
func (lm *LockManager) woundLocked(e *lockEntry, txnID int64, mode LockMode) {
	var victims []int64
	for h, m := range e.holders {
		if h > txnID && modesConflict(m, mode) {
			victims = append(victims, h)
		}
	}
	for _, w := range e.waiters {
		if w.txnID > txnID && modesConflict(w.mode, mode) {
			victims = append(victims, w.txnID)
		}
	}
	for _, v := range victims {
		if st, ok := lm.txns[v]; ok {
			lm.abortLocked(st, v)
		}
	}
}

// grantWaitersLocked serves the FIFO front of the queue. Consecutive
// compatible (shared) requests are co-granted; the first incompatible
// request stops the scan, preserving order.
func (lm *LockManager) grantWaitersLocked(e *lockEntry) {
	for len(e.waiters) > 0 {
		w := e.waiters[0]
		st, ok := lm.txns[w.txnID]
		if !ok || st.aborted {
			e.waiters = e.waiters[1:]
			close(w.notify)
			continue
		}
		compatible := true
		for h, m := range e.holders {
			if h != w.txnID && modesConflict(m, w.mode) {
				compatible = false
				break
			}
		}
		if !compatible {
			return
		}
		e.waiters = e.waiters[1:]
		e.holders[w.txnID] = w.mode
		st.held[e.key] = w.mode
		st.waiting = nil
		lm.grants.Add(1)
		close(w.notify)
	}
}

// Acquire blocks until the lock is granted or the transaction is wounded.
// It is re-entrant for the same transaction and supports S->X upgrade.
func (lm *LockManager) Acquire(txnID int64, key string, mode LockMode) error {
	lm.mu.Lock()
	st := lm.stateLocked(txnID)
	if st.aborted {
		lm.mu.Unlock()
		return ErrAborted
	}
	e := lm.entryLocked(key)

	if held, ok := e.holders[txnID]; ok {
		if held == mode || held == Exclusive {
			lm.mu.Unlock()
			return nil // re-entrant
		}
		// Upgrade S -> X.
		if len(e.holders) == 1 {
			e.holders[txnID] = Exclusive
			st.held[key] = Exclusive
			lm.mu.Unlock()
			return nil
		}
		lm.woundLocked(e, txnID, Exclusive)
		w := &waiter{txnID: txnID, mode: Exclusive, upgrade: true, notify: make(chan struct{})}
		e.waiters = append([]*waiter{w}, e.waiters...) // upgrader jumps the queue
		st.waiting, st.waitKey = w, key
		lm.mu.Unlock()
		<-w.notify
		lm.mu.Lock()
		defer lm.mu.Unlock()
		if lm.txns[txnID].aborted {
			return ErrAborted
		}
		if m, ok := lm.entries[key].holders[txnID]; ok && m == Exclusive {
			return nil
		}
		return ErrAborted
	}

	if lm.grantableLocked(e, txnID, mode) {
		e.holders[txnID] = mode
		st.held[key] = mode
		lm.grants.Add(1)
		lm.mu.Unlock()
		return nil
	}
	lm.woundLocked(e, txnID, mode)
	if lm.grantableLocked(e, txnID, mode) {
		e.holders[txnID] = mode
		st.held[key] = mode
		lm.grants.Add(1)
		lm.mu.Unlock()
		return nil
	}
	w := &waiter{txnID: txnID, mode: mode, notify: make(chan struct{})}
	e.waiters = append(e.waiters, w)
	st.waiting, st.waitKey = w, key
	lm.mu.Unlock()

	<-w.notify // always closed: by grant, by wound, or by queue reaper

	lm.mu.Lock()
	defer lm.mu.Unlock()
	cur, ok := lm.txns[txnID]
	if ok && cur.aborted {
		if len(cur.held) == 0 {
			delete(lm.txns, txnID) // lock-less victim: nothing to roll back
		}
		return ErrAborted
	}
	if _, ok := lm.entries[key].holders[txnID]; ok {
		return nil
	}
	if ok && len(cur.held) == 0 {
		delete(lm.txns, txnID)
	}
	return ErrAborted
}

// Abort marks txnID aborted and releases everything it holds or waits for.
func (lm *LockManager) Abort(txnID int64) {
	lm.mu.Lock()
	if st, ok := lm.txns[txnID]; ok {
		lm.abortLocked(st, txnID)
	}
	lm.releaseLocked(txnID)
	lm.mu.Unlock()
}

// ReleaseAll drops every lock held by txnID and deletes its state. A
// transaction whose state is gone starts fresh on its next Acquire.
func (lm *LockManager) ReleaseAll(txnID int64) {
	lm.mu.Lock()
	lm.releaseLocked(txnID)
	lm.mu.Unlock()
}

func (lm *LockManager) releaseLocked(txnID int64) {
	st, ok := lm.txns[txnID]
	if !ok {
		return
	}
	if st.waiting != nil {
		if e, ok := lm.entries[st.waitKey]; ok {
			lm.removeWaiterLocked(e, st.waiting)
			lm.grantWaitersLocked(e)
		}
		st.waiting = nil
	}
	for key := range st.held {
		e, ok := lm.entries[key]
		if !ok {
			continue
		}
		delete(e.holders, txnID)
		lm.grantWaitersLocked(e)
		if len(e.holders) == 0 && len(e.waiters) == 0 {
			delete(lm.entries, key)
		}
	}
	delete(lm.txns, txnID)
}
