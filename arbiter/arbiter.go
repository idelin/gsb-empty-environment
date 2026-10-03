// Package arbiter implements a multi-node lock arbitration service.
// Concurrent executors (nodes) acquire shared/exclusive locks on
// resources through a single Manager. All arbitration decisions are
// made atomically under one mutex, giving linearizable grant order.
// Deadlock freedom is guaranteed by the wound-wait policy: an older
// transaction requesting a conflicting lock wounds (aborts) younger
// holders and waiters, while a younger transaction simply waits for
// older ones. Wait-for edges therefore only ever point from younger
// to older transactions, so no cycle can exist.
package arbiter

import (
	"errors"
	"fmt"
	"sync"
)

// Mode is a lock mode.
type Mode int

const (
	Shared Mode = iota
	Exclusive
)

func (m Mode) String() string {
	if m == Shared {
		return "S"
	}
	return "X"
}

// ErrAborted is returned to a transaction that was wounded by an
// older transaction (or explicitly aborted).
var ErrAborted = errors.New("arbiter: transaction aborted by wound-wait arbitration")

// ErrUnknownTxn is returned when operating on an unregistered txn.
var ErrUnknownTxn = errors.New("arbiter: unknown transaction")

func compatible(held, want Mode) bool {
	return held == Shared && want == Shared
}

type txn struct {
	id      uint64
	stamp   uint64
	aborted bool
	held    map[uint64]Mode
}

type request struct {
	txn  *txn
	mode Mode
	ch   chan error // buffered(1); receives nil on grant, ErrAborted on wound
}

type resource struct {
	id      uint64
	holders map[*txn]Mode
	queue   []*request
}

// Manager arbitrates lock acquisition between concurrent
// transactions. The zero value is not usable; call NewManager.
type Manager struct {
	mu        sync.Mutex
	nextStamp uint64
	txns      map[uint64]*txn
	resources map[uint64]*resource
}

// NewManager creates an empty arbitration manager.
func NewManager() *Manager {
	return &Manager{
		txns:      make(map[uint64]*txn),
		resources: make(map[uint64]*resource),
	}
}

// Begin registers a transaction. Re-registering an existing id
// resets it (used when retrying an aborted transaction).
func (m *Manager) Begin(txnID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextStamp++
	m.txns[txnID] = &txn{id: txnID, stamp: m.nextStamp, held: make(map[uint64]Mode)}
}

func (m *Manager) getResource(resID uint64) *resource {
	res := m.resources[resID]
	if res == nil {
		res = &resource{id: resID, holders: make(map[*txn]Mode)}
		m.resources[resID] = res
	}
	return res
}

// Lock acquires the lock on resID in the given mode, blocking until
// the lock is granted or the transaction is wounded. It returns nil
// on grant and ErrAborted if the transaction was aborted while
// waiting (or had already been aborted).
func (m *Manager) Lock(txnID, resID uint64, mode Mode) error {
	m.mu.Lock()
	t := m.txns[txnID]
	if t == nil {
		m.mu.Unlock()
		return ErrUnknownTxn
	}
	if t.aborted {
		m.mu.Unlock()
		return ErrAborted
	}
	res := m.getResource(resID)

	if cur, ok := res.holders[t]; ok {
		if cur == Exclusive || cur == mode {
			m.mu.Unlock()
			return nil // already held in a sufficient mode
		}
		// Upgrade S -> X. Wound younger conflicting parties, then
		// either grant immediately or queue at the head (upgrades
		// jump the FIFO to avoid upgrade convoys).
		m.woundYounger(res, t, Exclusive)
		if m.grantable(res, t, Exclusive) {
			res.holders[t] = Exclusive
			t.held[resID] = Exclusive
			m.mu.Unlock()
			return nil
		}
		req := &request{txn: t, mode: Exclusive, ch: make(chan error, 1)}
		res.queue = append([]*request{req}, res.queue...)
		m.mu.Unlock()
		return <-req.ch
	}

	m.woundYounger(res, t, mode)
	if m.grantable(res, t, mode) {
		res.holders[t] = mode
		t.held[resID] = mode
		m.mu.Unlock()
		return nil
	}
	req := &request{txn: t, mode: mode, ch: make(chan error, 1)}
	res.queue = append(res.queue, req)
	m.mu.Unlock()
	return <-req.ch
}

// woundYounger aborts every younger transaction that conflicts with
// (t, mode) on res, whether it currently holds the lock or is queued.
// Must be called with m.mu held.
func (m *Manager) woundYounger(res *resource, t *txn, mode Mode) {
	var victims []*txn
	for h, hm := range res.holders {
		if h != t && h.stamp > t.stamp && !compatible(hm, mode) {
			victims = append(victims, h)
		}
	}
	for _, req := range res.queue {
		if req.txn != t && req.txn.stamp > t.stamp && !compatible(req.mode, mode) {
			victims = append(victims, req.txn)
		}
	}
	for _, v := range victims {
		m.abortLocked(v)
	}
}

// grantable reports whether (t, mode) can be granted on res right
// now: no conflicting holder and no conflicting queued waiter ahead.
// Must be called with m.mu held.
func (m *Manager) grantable(res *resource, t *txn, mode Mode) bool {
	for h, hm := range res.holders {
		if h != t && !compatible(hm, mode) {
			return false
		}
	}
	for _, req := range res.queue {
		if req.txn != t && !req.txn.aborted && !compatible(req.mode, mode) {
			return false
		}
	}
	return true
}

// processQueue grants the FIFO prefix of compatible queued requests.
// Must be called with m.mu held.
func (m *Manager) processQueue(res *resource) {
	for len(res.queue) > 0 {
		req := res.queue[0]
		if req.txn.aborted {
			res.queue = res.queue[1:]
			continue
		}
		ok := true
		for h, hm := range res.holders {
			if h != req.txn && !compatible(hm, req.mode) {
				ok = false
				break
			}
		}
		if !ok {
			return
		}
		res.queue = res.queue[1:]
		res.holders[req.txn] = req.mode
		req.txn.held[res.id] = req.mode
		req.ch <- nil
	}
}

// abortLocked marks t aborted and fails its queued requests. Locks
// it already holds are NOT released here: they are dropped when the
// transaction rolls back (Finish/Unlock), which preserves the 2PL
// invariant that no other transaction can observe its uncommitted
// state. Deadlock freedom is retained because an aborted transaction
// never waits again (all further Lock calls fail immediately), so
// wait-for edges still only point from younger to older live txns.
// Must be called with m.mu held.
func (m *Manager) abortLocked(t *txn) {
	if t.aborted {
		return
	}
	t.aborted = true
	for _, res := range m.resources {
		kept := res.queue[:0]
		for _, req := range res.queue {
			if req.txn == t {
				req.ch <- ErrAborted
			} else {
				kept = append(kept, req)
			}
		}
		res.queue = kept
	}
	for _, res := range m.resources {
		m.processQueue(res)
	}
}

// Abort explicitly aborts a transaction (same effect as being
// wounded). Pending Lock calls return ErrAborted.
func (m *Manager) Abort(txnID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.txns[txnID]; t != nil {
		m.abortLocked(t)
	}
}

// Unlock releases one lock held by the transaction.
func (m *Manager) Unlock(txnID, resID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.txns[txnID]
	if t == nil {
		return
	}
	res := m.resources[resID]
	if res == nil {
		return
	}
	if _, ok := res.holders[t]; ok {
		delete(res.holders, t)
		delete(t.held, resID)
		m.processQueue(res)
	}
}

// Finish releases every lock held by the transaction and removes it
// from the manager (commit/abort completion).
func (m *Manager) Finish(txnID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.txns[txnID]
	if t == nil {
		return
	}
	for resID := range t.held {
		if res := m.resources[resID]; res != nil {
			delete(res.holders, t)
		}
	}
	t.held = make(map[uint64]Mode)
	delete(m.txns, txnID)
	for _, res := range m.resources {
		m.processQueue(res)
	}
}

// IsAborted reports whether the transaction has been wounded.
func (m *Manager) IsAborted(txnID uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.txns[txnID]
	return t != nil && t.aborted
}

// Holders returns a snapshot of the holder txn IDs of a resource.
func (m *Manager) Holders(resID uint64) []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.resources[resID]
	if res == nil {
		return nil
	}
	out := make([]uint64, 0, len(res.holders))
	for t := range res.holders {
		out = append(out, t.id)
	}
	return out
}

// Waiters returns the number of queued requests on a resource.
func (m *Manager) Waiters(resID uint64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if res := m.resources[resID]; res != nil {
		return len(res.queue)
	}
	return 0
}

// String renders the manager state (debugging aid).
func (m *Manager) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("txns=%d resources=%d", len(m.txns), len(m.resources))
}
