// Package ss2pl implements a strict two-phase locking (SS2PL) kernel with a
// shrinking-phase manager. Transactions acquire locks during the growing
// phase and release every lock at once on commit or rollback. Because no
// lock is ever released before commit, the resulting conflict-serializable
// schedule is equivalent to the timestamp order of transaction commit times.
package ss2pl

import (
	"errors"
	"sort"
	"sync"
)

// ErrDeadlock is returned to a lock requester whose wait would close a cycle
// in the wait-for graph. The requester is expected to roll back.
var ErrDeadlock = errors.New("ss2pl: deadlock detected, requester must roll back")

// LockMode is the granularity of a held or requested lock.
type LockMode int

const (
	// Shared allows concurrent readers.
	Shared LockMode = iota
	// Exclusive is required for writers and conflicts with every other mode.
	Exclusive
)

func (m LockMode) String() string {
	if m == Shared {
		return "S"
	}
	return "X"
}

func compatible(a, b LockMode) bool {
	return a == Shared && b == Shared
}

type lockRequest struct {
	txnID uint64
	mode  LockMode
	grant chan struct{}
}

type resourceLock struct {
	holders map[uint64]LockMode
	queue   []*lockRequest
}

// LockManager is the SS2PL kernel. It grants row locks, tracks the wait-for
// graph for deadlock detection, and exposes a monitor view of lock state.
// Locks are only ever released through ReleaseAll, which is invoked exactly
// once per transaction at commit or rollback time (the shrinking phase).
type LockManager struct {
	mu      sync.Mutex
	table   map[string]*resourceLock
	held    map[uint64]map[string]LockMode
	waitFor map[uint64]map[uint64]struct{}
}

// NewLockManager creates an empty lock manager.
func NewLockManager() *LockManager {
	return &LockManager{
		table:   make(map[string]*resourceLock),
		held:    make(map[uint64]map[string]LockMode),
		waitFor: make(map[uint64]map[uint64]struct{}),
	}
}

// Acquire blocks until txnID owns the lock on resource in the given mode.
// It returns ErrDeadlock if granting the wait would create a cycle.
func (lm *LockManager) Acquire(txnID uint64, resource string, mode LockMode) error {
	lm.mu.Lock()
	rl := lm.table[resource]
	if rl == nil {
		rl = &resourceLock{holders: make(map[uint64]LockMode)}
		lm.table[resource] = rl
	}

	// Re-entrant: already held in a sufficient mode.
	if held, ok := rl.holders[txnID]; ok {
		if held == mode || held == Exclusive {
			lm.mu.Unlock()
			return nil
		}
		// Upgrade S -> X: only possible if we are the sole holder and
		// nobody is queued ahead of us.
		if len(rl.holders) == 1 && len(rl.queue) == 0 {
			rl.holders[txnID] = Exclusive
			lm.held[txnID][resource] = Exclusive
			lm.mu.Unlock()
			return nil
		}
	}

	grantable := len(rl.queue) == 0
	if grantable {
		for holder, hm := range rl.holders {
			if holder != txnID && !compatible(hm, mode) {
				grantable = false
				break
			}
		}
	}
	if grantable {
		rl.holders[txnID] = mode
		lm.recordHeld(txnID, resource, mode)
		lm.mu.Unlock()
		return nil
	}

	// Must wait: build wait-for edges to every holder and every queued
	// requester ahead of us, then refuse if that closes a cycle.
	targets := make(map[uint64]struct{})
	for holder := range rl.holders {
		if holder != txnID {
			targets[holder] = struct{}{}
		}
	}
	for _, q := range rl.queue {
		if q.txnID != txnID {
			targets[q.txnID] = struct{}{}
		}
	}
	if lm.wouldCycle(txnID, targets) {
		lm.mu.Unlock()
		return ErrDeadlock
	}
	if lm.waitFor[txnID] == nil {
		lm.waitFor[txnID] = make(map[uint64]struct{})
	}
	for t := range targets {
		lm.waitFor[txnID][t] = struct{}{}
	}

	req := &lockRequest{txnID: txnID, mode: mode, grant: make(chan struct{})}
	rl.queue = append(rl.queue, req)
	lm.mu.Unlock()

	<-req.grant
	return nil
}

func (lm *LockManager) recordHeld(txnID uint64, resource string, mode LockMode) {
	if lm.held[txnID] == nil {
		lm.held[txnID] = make(map[string]LockMode)
	}
	lm.held[txnID][resource] = mode
}

// wouldCycle reports whether adding edges from txnID to targets closes a
// cycle in the wait-for graph.
func (lm *LockManager) wouldCycle(txnID uint64, targets map[uint64]struct{}) bool {
	seen := make(map[uint64]bool)
	var stack []uint64
	for t := range targets {
		stack = append(stack, t)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == txnID {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		for next := range lm.waitFor[cur] {
			stack = append(stack, next)
		}
	}
	return false
}

// ReleaseAll is the shrinking phase: it drops every lock held by txnID in a
// single atomic step and wakes eligible waiters in FIFO order. It is called
// exactly once per transaction, at commit or rollback.
func (lm *LockManager) ReleaseAll(txnID uint64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	resources := lm.held[txnID]
	for resource := range resources {
		if rl := lm.table[resource]; rl != nil {
			delete(rl.holders, txnID)
			lm.promote(resource, rl)
		}
	}
	delete(lm.held, txnID)
	delete(lm.waitFor, txnID)
	for _, edges := range lm.waitFor {
		delete(edges, txnID)
	}
}

// promote grants queued requests from the head of the queue while they are
// compatible with the (new) holder set. Caller must hold lm.mu.
func (lm *LockManager) promote(resource string, rl *resourceLock) {
	for len(rl.queue) > 0 {
		req := rl.queue[0]
		ok := true
		for holderID, hm := range rl.holders {
			if holderID == req.txnID {
				continue // a transaction never conflicts with itself (S->X upgrade)
			}
			if !compatible(hm, req.mode) {
				ok = false
				break
			}
		}
		if !ok {
			return
		}
		rl.queue = rl.queue[1:]
		rl.holders[req.txnID] = req.mode
		lm.recordHeld(req.txnID, resource, req.mode)
		delete(lm.waitFor, req.txnID)
		close(req.grant)
	}
}

// LockInfo is a monitor snapshot of one resource's lock state.
type LockInfo struct {
	Resource string
	Holders  map[uint64]LockMode
	Waiters  []uint64
}

// Snapshot returns the current lock table for monitoring. It lets an
// external observer verify that no row lock is released before commit.
func (lm *LockManager) Snapshot() []LockInfo {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	names := make([]string, 0, len(lm.table))
	for name := range lm.table {
		names = append(names, name)
	}
	sort.Strings(names)
	infos := make([]LockInfo, 0, len(names))
	for _, name := range names {
		rl := lm.table[name]
		info := LockInfo{Resource: name, Holders: make(map[uint64]LockMode)}
		for txn, mode := range rl.holders {
			info.Holders[txn] = mode
		}
		for _, req := range rl.queue {
			info.Waiters = append(info.Waiters, req.txnID)
		}
		infos = append(infos, info)
	}
	return infos
}

// HeldBy returns a copy of the lock set currently held by txnID.
func (lm *LockManager) HeldBy(txnID uint64) map[string]LockMode {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make(map[string]LockMode)
	for res, mode := range lm.held[txnID] {
		out[res] = mode
	}
	return out
}
