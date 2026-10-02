package ss2pl

import (
	"errors"
	"sort"
	"sync"
)

// LockMode is the mode of a lock on a resource.
type LockMode int

const (
	// SLock is a shared (read) lock.
	SLock LockMode = iota
	// XLock is an exclusive (write) lock.
	XLock
)

func (m LockMode) String() string {
	if m == XLock {
		return "X"
	}
	return "S"
}

var (
	// ErrDeadlock is returned to the transaction whose lock request would
	// close a cycle in the wait-for graph. That transaction is the victim
	// and must abort.
	ErrDeadlock = errors.New("ss2pl: deadlock detected, transaction chosen as victim")
	// ErrWaitCancelled is returned to a transaction whose pending lock
	// wait was cancelled (e.g. the transaction was aborted or crashed).
	ErrWaitCancelled = errors.New("ss2pl: lock wait cancelled, transaction aborted")
)

type lockRequest struct {
	txnID int
	mode  LockMode
	grant chan error // buffered (cap 1); receives nil on grant, error on cancel
}

type resourceLock struct {
	holders map[int]LockMode // txnID -> held mode
	queue   []*lockRequest   // FIFO wait queue
}

// LockEventKind distinguishes acquire from release events.
type LockEventKind int

const (
	// EventAcquire is emitted when a lock is granted.
	EventAcquire LockEventKind = iota
	// EventRelease is emitted when a lock is released (only ever from the
	// shrinking phase, i.e. at commit/abort time).
	EventRelease
)

// LockEvent is emitted by the LockManager whenever a lock is granted or
// released. The Monitor subscribes to these events.
type LockEvent struct {
	Kind     LockEventKind
	TxnID    int
	Resource string
	Mode     LockMode
}

// LockManager is a centralized lock table with fair FIFO queues and
// wait-for-graph deadlock detection.
//
// Strictness is enforced structurally: the manager exposes Acquire to
// everyone, but only ReleaseAll (used by the ShrinkingManager at
// commit/abort time) can release locks. There is intentionally no public
// per-lock Release, so a transaction in its growing phase cannot drop a
// single lock early.
type LockManager struct {
	mu      sync.Mutex
	locks   map[string]*resourceLock
	heldBy  map[int]map[string]LockMode // txnID -> resource -> mode
	waitFor map[int]map[int]bool        // txnID -> set of txnIDs it waits for
	hook    func(LockEvent)
}

// NewLockManager creates an empty lock manager.
func NewLockManager() *LockManager {
	return &LockManager{
		locks:   make(map[string]*resourceLock),
		heldBy:  make(map[int]map[string]LockMode),
		waitFor: make(map[int]map[int]bool),
	}
}

// SetHook installs a listener for lock events. The hook is invoked while
// the manager's internal mutex is held, so it must never call back into
// the LockManager.
func (lm *LockManager) SetHook(h func(LockEvent)) {
	lm.mu.Lock()
	lm.hook = h
	lm.mu.Unlock()
}

func (lm *LockManager) emit(ev LockEvent) {
	if lm.hook != nil {
		lm.hook(ev)
	}
}

// grantable reports whether a request for mode by txnID is compatible with
// the current holders (ignoring locks already held by txnID itself).
func grantable(rl *resourceLock, txnID int, mode LockMode) bool {
	for h, hm := range rl.holders {
		if h == txnID {
			continue
		}
		if mode == XLock || hm == XLock {
			return false
		}
	}
	return true
}

// Acquire blocks until the lock is granted, the wait is cancelled, or a
// deadlock is detected (in which case ErrDeadlock is returned and the
// caller must abort).
func (lm *LockManager) Acquire(txnID int, resource string, mode LockMode) error {
	lm.mu.Lock()
	rl := lm.locks[resource]
	if rl == nil {
		rl = &resourceLock{holders: make(map[int]LockMode)}
		lm.locks[resource] = rl
	}

	// Re-entrant request by the same transaction.
	if held, ok := rl.holders[txnID]; ok {
		if held >= mode {
			lm.mu.Unlock()
			return nil
		}
		// Lock upgrade S -> X: grant immediately only if sole holder and
		// nobody is queued; otherwise fall through and queue the upgrade.
		if len(rl.holders) == 1 && len(rl.queue) == 0 {
			rl.holders[txnID] = XLock
			lm.heldBy[txnID][resource] = XLock
			lm.emit(LockEvent{Kind: EventAcquire, TxnID: txnID, Resource: resource, Mode: XLock})
			lm.mu.Unlock()
			return nil
		}
	}

	if len(rl.queue) == 0 && grantable(rl, txnID, mode) {
		rl.holders[txnID] = mode
		lm.rememberLocked(txnID, resource, mode)
		lm.emit(LockEvent{Kind: EventAcquire, TxnID: txnID, Resource: resource, Mode: mode})
		lm.mu.Unlock()
		return nil
	}

	req := &lockRequest{txnID: txnID, mode: mode, grant: make(chan error, 1)}
	rl.queue = append(rl.queue, req)
	lm.addWaitEdges(txnID, rl)
	if lm.hasCycle(txnID) {
		lm.removeRequest(rl, req)
		lm.clearWaitEdges(txnID)
		lm.mu.Unlock()
		return ErrDeadlock
	}
	lm.mu.Unlock()

	return <-req.grant
}

func (lm *LockManager) rememberLocked(txnID int, resource string, mode LockMode) {
	if lm.heldBy[txnID] == nil {
		lm.heldBy[txnID] = make(map[string]LockMode)
	}
	lm.heldBy[txnID][resource] = mode
}

func (lm *LockManager) addWaitEdges(txnID int, rl *resourceLock) {
	if lm.waitFor[txnID] == nil {
		lm.waitFor[txnID] = make(map[int]bool)
	}
	for h := range rl.holders {
		if h != txnID {
			lm.waitFor[txnID][h] = true
		}
	}
	for _, q := range rl.queue {
		if q.txnID != txnID {
			lm.waitFor[txnID][q.txnID] = true
		}
	}
}

func (lm *LockManager) clearWaitEdges(txnID int) {
	delete(lm.waitFor, txnID)
}

// hasCycle reports whether txnID sits on a cycle of the wait-for graph.
func (lm *LockManager) hasCycle(start int) bool {
	visited := make(map[int]bool)
	var dfs func(n int) bool
	dfs = func(n int) bool {
		for next := range lm.waitFor[n] {
			if next == start {
				return true
			}
			if !visited[next] {
				visited[next] = true
				if dfs(next) {
					return true
				}
			}
		}
		return false
	}
	return dfs(start)
}

func (lm *LockManager) removeRequest(rl *resourceLock, req *lockRequest) {
	for i, q := range rl.queue {
		if q == req {
			rl.queue = append(rl.queue[:i], rl.queue[i+1:]...)
			return
		}
	}
}

// processQueue grants waiting requests from the front of the queue while
// they are compatible with the current holders. Callers must hold lm.mu.
func (lm *LockManager) processQueue(rl *resourceLock, resource string) {
	for len(rl.queue) > 0 {
		req := rl.queue[0]
		if !grantable(rl, req.txnID, req.mode) {
			return
		}
		rl.queue = rl.queue[1:]
		rl.holders[req.txnID] = req.mode
		lm.rememberLocked(req.txnID, resource, req.mode)
		lm.clearWaitEdges(req.txnID)
		lm.emit(LockEvent{Kind: EventAcquire, TxnID: req.txnID, Resource: resource, Mode: req.mode})
		req.grant <- nil
	}
}

// CancelWait removes any queued (not yet granted) request of txnID and
// wakes it up with ErrWaitCancelled. A transaction waits on at most one
// request at a time.
func (lm *LockManager) CancelWait(txnID int) {
	lm.mu.Lock()
	lm.cancelWaitLocked(txnID)
	lm.mu.Unlock()
}

func (lm *LockManager) cancelWaitLocked(txnID int) {
	for _, rl := range lm.locks {
		for _, q := range rl.queue {
			if q.txnID == txnID {
				lm.removeRequest(rl, q)
				q.grant <- ErrWaitCancelled
			}
		}
	}
	lm.clearWaitEdges(txnID)
}

// ReleaseAll atomically releases every lock held by txnID. This is the
// only way locks are ever released, and it is invoked exactly once per
// transaction, by the ShrinkingManager, at commit or abort time.
func (lm *LockManager) ReleaseAll(txnID int) {
	lm.mu.Lock()
	lm.cancelWaitLocked(txnID)

	held := lm.heldBy[txnID]
	names := make([]string, 0, len(held))
	for name := range held {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic release order for readable traces

	affected := make(map[string]*resourceLock)
	for _, name := range names {
		rl := lm.locks[name]
		if rl == nil {
			continue
		}
		mode := rl.holders[txnID]
		delete(rl.holders, txnID)
		lm.emit(LockEvent{Kind: EventRelease, TxnID: txnID, Resource: name, Mode: mode})
		affected[name] = rl
	}
	delete(lm.heldBy, txnID)

	// Grant queued requests that became compatible, in FIFO order.
	names = names[:0]
	for name := range affected {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lm.processQueue(affected[name], name)
	}
	lm.mu.Unlock()
}

// HeldLocks returns a sorted snapshot of the locks currently held by txnID.
func (lm *LockManager) HeldLocks(txnID int) []string {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make([]string, 0, len(lm.heldBy[txnID]))
	for name := range lm.heldBy[txnID] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TableSnapshot returns a human-readable snapshot of the lock table.
func (lm *LockManager) TableSnapshot() map[string]map[int]LockMode {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make(map[string]map[int]LockMode, len(lm.locks))
	for name, rl := range lm.locks {
		if len(rl.holders) == 0 {
			continue
		}
		m := make(map[int]LockMode, len(rl.holders))
		for txn, mode := range rl.holders {
			m[txn] = mode
		}
		out[name] = m
	}
	return out
}
