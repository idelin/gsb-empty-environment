package ss2pl

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// TxnState is the lifecycle state of a transaction.
type TxnState int

const (
	// TxnActive means the transaction is in its growing phase: it may
	// acquire locks but never releases any.
	TxnActive TxnState = iota
	// TxnCommitted means the transaction committed and entered the
	// shrinking phase, releasing all locks at once.
	TxnCommitted
	// TxnAborted means the transaction rolled back and entered the
	// shrinking phase, releasing all locks at once.
	TxnAborted
)

func (s TxnState) String() string {
	switch s {
	case TxnActive:
		return "ACTIVE"
	case TxnCommitted:
		return "COMMITTED"
	case TxnAborted:
		return "ABORTED"
	}
	return "UNKNOWN"
}

// TxnEventKind identifies transaction lifecycle events.
type TxnEventKind int

const (
	// TxnEventBegin marks transaction start.
	TxnEventBegin TxnEventKind = iota
	// TxnEventCommit marks the commit decision (before locks are released).
	TxnEventCommit
	// TxnEventAbort marks the abort decision (before locks are released).
	TxnEventAbort
	// TxnEventCrash marks an injected crash; recovery aborts the txn.
	TxnEventCrash
)

// TxnEvent is emitted by the TxnManager at lifecycle transitions.
type TxnEvent struct {
	Kind  TxnEventKind
	TxnID int
}

// OpRecord is one entry of the global operation log, used to verify that
// the produced schedule is equivalent to the commit-time timestamp order.
type OpRecord struct {
	Seq   int64 // global operation order
	TxnID int
	Key   string
	Op    byte // 'R' or 'W'
}

// CommitRecord is one entry of the commit log; Seq is the commit-time
// timestamp of the transaction.
type CommitRecord struct {
	TxnID int
	Seq   int64
}

type undoEntry struct {
	key     string
	old     int64
	existed bool
}

// ShrinkingManager governs the shrinking phase of strict 2PL. It is the
// only component allowed to release locks, and it always releases all of a
// transaction's locks in a single atomic step. Because transactions have
// no API to release individual locks, the growing phase cannot shrink
// early by construction.
type ShrinkingManager struct {
	lm *LockManager
}

// NewShrinkingManager creates the shrinking-phase manager for lm.
func NewShrinkingManager(lm *LockManager) *ShrinkingManager {
	return &ShrinkingManager{lm: lm}
}

// Shrink releases every lock held by txnID at once. It is idempotent.
func (sm *ShrinkingManager) Shrink(txnID int) {
	sm.lm.ReleaseAll(txnID)
}

// TxnManager creates and tracks transactions and owns the commit log and
// the global operation log.
type TxnManager struct {
	lm       *LockManager
	store    *Store
	shrinker *ShrinkingManager

	nextTxnID int64
	opSeq     int64
	commitSeq int64

	mu        sync.Mutex
	txns      map[int]*Txn
	commitLog []CommitRecord
	opLog     []OpRecord
	hook      func(TxnEvent)
}

// NewTxnManager wires together store, lock manager and shrinking manager.
func NewTxnManager(store *Store, lm *LockManager) *TxnManager {
	return &TxnManager{
		lm:       lm,
		store:    store,
		shrinker: NewShrinkingManager(lm),
		txns:     make(map[int]*Txn),
	}
}

// SetHook installs a listener for transaction lifecycle events.
func (tm *TxnManager) SetHook(h func(TxnEvent)) {
	tm.mu.Lock()
	tm.hook = h
	tm.mu.Unlock()
}

func (tm *TxnManager) emit(ev TxnEvent) {
	tm.mu.Lock()
	h := tm.hook
	tm.mu.Unlock()
	if h != nil {
		h(ev)
	}
}

// Begin starts a new transaction in its growing phase.
func (tm *TxnManager) Begin() *Txn {
	id := int(atomic.AddInt64(&tm.nextTxnID, 1))
	t := &Txn{id: id, tm: tm, state: TxnActive}
	tm.mu.Lock()
	tm.txns[id] = t
	tm.mu.Unlock()
	tm.emit(TxnEvent{Kind: TxnEventBegin, TxnID: id})
	return t
}

// Crash simulates an abrupt failure of the transaction's worker (power
// loss, panic, kill). The recovery path rolls back every modification the
// transaction made and releases all of its locks in one shot, so other
// transactions are never blocked by a dead lock holder and never observe
// partial results.
func (tm *TxnManager) Crash(txnID int) error {
	tm.mu.Lock()
	t, ok := tm.txns[txnID]
	tm.mu.Unlock()
	if !ok {
		return fmt.Errorf("ss2pl: unknown transaction %d", txnID)
	}
	tm.emit(TxnEvent{Kind: TxnEventCrash, TxnID: txnID})
	t.Abort()
	return nil
}

func (tm *TxnManager) recordCommit(txnID int) {
	seq := atomic.AddInt64(&tm.commitSeq, 1)
	tm.mu.Lock()
	tm.commitLog = append(tm.commitLog, CommitRecord{TxnID: txnID, Seq: seq})
	tm.mu.Unlock()
}

func (tm *TxnManager) logOp(txnID int, key string, op byte) {
	seq := atomic.AddInt64(&tm.opSeq, 1)
	tm.mu.Lock()
	tm.opLog = append(tm.opLog, OpRecord{Seq: seq, TxnID: txnID, Key: key, Op: op})
	tm.mu.Unlock()
}

// CommitLog returns the commit log ordered by commit-time timestamp.
func (tm *TxnManager) CommitLog() []CommitRecord {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	out := make([]CommitRecord, len(tm.commitLog))
	copy(out, tm.commitLog)
	return out
}

// LockManager exposes the underlying lock manager (monitoring/tests).
func (tm *TxnManager) LockManager() *LockManager { return tm.lm }

// Store exposes the underlying store (monitoring/tests).
func (tm *TxnManager) Store() *Store { return tm.store }

// Txn is a strict two-phase locking transaction.
type Txn struct {
	id    int
	tm    *TxnManager
	mu    sync.Mutex
	state TxnState
	undo  []undoEntry
}

// ID returns the transaction identifier.
func (t *Txn) ID() int { return t.id }

// State returns the current lifecycle state.
func (t *Txn) State() TxnState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// ErrTxnNotActive is returned when an operation is attempted on a
// transaction that has already committed or aborted.
var ErrTxnNotActive = fmt.Errorf("ss2pl: transaction is no longer active")

// Read acquires a shared lock on key and returns its value.
func (t *Txn) Read(key string) (int64, error) {
	if err := t.ensureActive(); err != nil {
		return 0, err
	}
	if err := t.tm.lm.Acquire(t.id, key, SLock); err != nil {
		t.Abort()
		return 0, err
	}
	t.mu.Lock()
	if t.state != TxnActive {
		t.mu.Unlock()
		return 0, ErrTxnNotActive
	}
	v, _ := t.tm.store.Get(key)
	t.mu.Unlock()
	t.tm.logOp(t.id, key, 'R')
	return v, nil
}

// Write acquires an exclusive lock on key and sets its value, recording
// the previous image in the undo log.
func (t *Txn) Write(key string, val int64) error {
	if err := t.ensureActive(); err != nil {
		return err
	}
	if err := t.tm.lm.Acquire(t.id, key, XLock); err != nil {
		t.Abort()
		return err
	}
	// Hold t.mu across the store write and the undo append so a
	// concurrent crash-recovery rollback cannot interleave with them.
	t.mu.Lock()
	if t.state != TxnActive {
		t.mu.Unlock()
		return ErrTxnNotActive
	}
	old, existed := t.tm.store.Set(key, val)
	t.undo = append(t.undo, undoEntry{key: key, old: old, existed: existed})
	t.mu.Unlock()
	t.tm.logOp(t.id, key, 'W')
	return nil
}

// Commit ends the growing phase, records the commit-time timestamp and
// enters the shrinking phase: all locks are released in one atomic step.
func (t *Txn) Commit() error {
	t.mu.Lock()
	if t.state != TxnActive {
		t.mu.Unlock()
		return ErrTxnNotActive
	}
	t.state = TxnCommitted
	t.mu.Unlock()

	t.tm.emit(TxnEvent{Kind: TxnEventCommit, TxnID: t.id})
	t.tm.recordCommit(t.id)
	t.tm.shrinker.Shrink(t.id)
	return nil
}

// Abort rolls back every modification (in reverse order) and enters the
// shrinking phase: all locks are released in one atomic step.
func (t *Txn) Abort() {
	t.mu.Lock()
	if t.state != TxnActive {
		t.mu.Unlock()
		return
	}
	t.state = TxnAborted
	undo := t.undo
	t.undo = nil
	// Roll back while still holding t.mu so concurrent writes on this
	// transaction cannot interleave with the undo pass.
	for i := len(undo) - 1; i >= 0; i-- {
		if undo[i].existed {
			t.tm.store.Set(undo[i].key, undo[i].old)
		} else {
			t.tm.store.Delete(undo[i].key)
		}
	}
	t.mu.Unlock()

	t.tm.emit(TxnEvent{Kind: TxnEventAbort, TxnID: t.id})
	t.tm.shrinker.Shrink(t.id)
}

func (t *Txn) ensureActive() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != TxnActive {
		return ErrTxnNotActive
	}
	return nil
}
