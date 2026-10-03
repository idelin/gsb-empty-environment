package ss2pl

import (
	"errors"
	"fmt"
	"sync"
)

// ErrTxnNotGrowing is returned when a transaction tries to acquire a lock or
// access data after leaving the growing phase (i.e. after commit/rollback
// has begun). SS2PL forbids any lock acquisition during the shrinking phase.
var ErrTxnNotGrowing = errors.New("ss2pl: transaction is no longer in the growing phase")

// Phase identifies where a transaction sits in the 2PL lifecycle.
type Phase int

const (
	// Growing: locks may be acquired, none may be released.
	Growing Phase = iota
	// Shrinking: commit/rollback in progress; all locks released at once.
	Shrinking
	// Done: transaction finished; no further operations allowed.
	Done
)

func (p Phase) String() string {
	switch p {
	case Growing:
		return "growing"
	case Shrinking:
		return "shrinking"
	default:
		return "done"
	}
}

type undoEntry struct {
	key     string
	oldVal  int
	existed bool
}

// DB is a tiny in-memory key/value store guarded by the SS2PL kernel.
type DB struct {
	mu       sync.Mutex
	data     map[string]int
	lm       *LockManager
	seq      uint64 // transaction id allocator
	commitTS uint64 // commit timestamp oracle
}

// NewDB creates a store with its own lock manager.
func NewDB() *DB {
	return &DB{data: make(map[string]int), lm: NewLockManager()}
}

// LockManager exposes the kernel so monitors can inspect lock state.
func (db *DB) LockManager() *LockManager { return db.lm }

// Set seeds a key outside of any transaction (test/setup helper).
func (db *DB) Set(key string, val int) {
	db.mu.Lock()
	db.data[key] = val
	db.mu.Unlock()
}

// Get reads a key outside of any transaction (test/setup helper).
func (db *DB) Get(key string) (int, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	v, ok := db.data[key]
	return v, ok
}

// Begin starts a transaction in the growing phase.
func (db *DB) Begin() *Txn {
	db.mu.Lock()
	db.seq++
	id := db.seq
	db.mu.Unlock()
	return &Txn{db: db, id: id, phase: Growing}
}

// Txn is a strict two-phase locking transaction.
type Txn struct {
	db       *DB
	id       uint64
	mu       sync.Mutex
	phase    Phase
	undo     []undoEntry
	commitTS uint64
}

// ID returns the transaction identifier.
func (t *Txn) ID() uint64 { return t.id }

// Phase reports the current 2PL phase.
func (t *Txn) Phase() Phase {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase
}

// CommitTS returns the commit timestamp assigned at commit time. Under
// SS2PL the conflict order of committed transactions equals this order.
func (t *Txn) CommitTS() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commitTS
}

func (t *Txn) checkGrowing() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phase != Growing {
		return ErrTxnNotGrowing
	}
	return nil
}

// Read acquires a shared lock on key and returns its value.
func (t *Txn) Read(key string) (int, error) {
	if err := t.checkGrowing(); err != nil {
		return 0, err
	}
	if err := t.db.lm.Acquire(t.id, key, Shared); err != nil {
		return 0, err
	}
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	return t.db.data[key], nil
}

// Write acquires an exclusive lock on key, records an undo entry, and
// applies the new value.
func (t *Txn) Write(key string, val int) error {
	if err := t.checkGrowing(); err != nil {
		return err
	}
	if err := t.db.lm.Acquire(t.id, key, Exclusive); err != nil {
		return err
	}
	t.db.mu.Lock()
	old, existed := t.db.data[key]
	t.db.data[key] = val
	t.db.mu.Unlock()
	t.mu.Lock()
	t.undo = append(t.undo, undoEntry{key: key, oldVal: old, existed: existed})
	t.mu.Unlock()
	return nil
}

// Commit ends the transaction: it enters the shrinking phase, assigns the
// commit timestamp, and releases every held lock in one atomic step. No
// lock is ever released before this point.
func (t *Txn) Commit() error {
	t.mu.Lock()
	if t.phase != Growing {
		t.mu.Unlock()
		return ErrTxnNotGrowing
	}
	t.phase = Shrinking
	t.mu.Unlock()

	t.db.mu.Lock()
	t.db.commitTS++
	ts := t.db.commitTS
	t.db.mu.Unlock()

	t.db.lm.ReleaseAll(t.id) // shrinking phase: single bulk release

	t.mu.Lock()
	t.commitTS = ts
	t.phase = Done
	t.mu.Unlock()
	return nil
}

// Rollback undoes every write in reverse order and releases all locks.
func (t *Txn) Rollback() error {
	t.mu.Lock()
	if t.phase != Growing {
		t.mu.Unlock()
		return ErrTxnNotGrowing
	}
	t.phase = Shrinking
	undo := t.undo
	t.mu.Unlock()

	t.db.mu.Lock()
	for i := len(undo) - 1; i >= 0; i-- {
		u := undo[i]
		if u.existed {
			t.db.data[u.key] = u.oldVal
		} else {
			delete(t.db.data, u.key)
		}
	}
	t.db.mu.Unlock()

	t.db.lm.ReleaseAll(t.id)

	t.mu.Lock()
	t.phase = Done
	t.mu.Unlock()
	return nil
}

// Exec runs fn as the transaction body. If fn panics (an injected crash),
// Exec safely rolls back every write and releases every lock the
// transaction holds before returning an error. If fn returns an error,
// the transaction is rolled back as well.
func (t *Txn) Exec(fn func(*Txn) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			_ = t.Rollback()
			err = fmt.Errorf("ss2pl: txn %d crashed, rolled back: %v", t.id, r)
		}
	}()
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}
