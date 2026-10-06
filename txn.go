package memtx

import "sync"

// wsEntry is a buffered write inside a transaction.
type wsEntry struct {
	value   []byte
	deleted bool
}

type txnState int

const (
	txnActive txnState = iota
	txnCommitted
	txnAborted
)

// Txn is a single-use transaction. It reads from the fixed snapshot taken
// at Begin, overlaid with its own write set (read-your-own-writes). A Txn
// is not safe for concurrent use by multiple goroutines.
type Txn struct {
	db      *DB
	snapRev Revision

	mu    sync.Mutex
	ws    map[string]wsEntry
	wsRev uint64 // bumped on every write-set mutation; cursors bind to it
	state txnState
}

// SnapshotRevision returns the committed revision this transaction reads
// from. It never changes during the transaction's lifetime.
func (tx *Txn) SnapshotRevision() Revision {
	return tx.snapRev
}

// WriteSetRevision returns the current revision of the transaction's
// write set. Every Put or Delete bumps it, invalidating cursors issued
// before the mutation.
func (tx *Txn) WriteSetRevision() uint64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.wsRev
}

func (tx *Txn) checkActive() error {
	if tx.state != txnActive {
		return ErrTxnClosed
	}
	return nil
}

// Get returns the value for key, consulting the transaction's own write
// set first and falling back to the snapshot. It returns ErrKeyNotFound
// if the key is absent or deleted.
func (tx *Txn) Get(key string) ([]byte, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.checkActive(); err != nil {
		return nil, err
	}
	if entry, ok := tx.ws[key]; ok {
		if entry.deleted {
			return nil, ErrKeyNotFound
		}
		return append([]byte(nil), entry.value...), nil
	}
	tx.db.mu.RLock()
	defer tx.db.mu.RUnlock()
	value, ok := tx.db.lookupLocked(key, tx.snapRev)
	if !ok {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

// Put buffers a write of value at key. The write becomes visible to this
// transaction immediately and to others only after Commit.
func (tx *Txn) Put(key string, value []byte) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.checkActive(); err != nil {
		return err
	}
	tx.ws[key] = wsEntry{value: append([]byte(nil), value...)}
	tx.wsRev++
	return nil
}

// Delete buffers a deletion of key. The key disappears from this
// transaction's reads and scans immediately.
func (tx *Txn) Delete(key string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.checkActive(); err != nil {
		return err
	}
	tx.ws[key] = wsEntry{deleted: true}
	tx.wsRev++
	return nil
}

// Commit atomically applies the write set at a new database revision. It
// fails with ErrConflict if any written key was committed by another
// transaction after this transaction's snapshot was taken; the
// transaction is then aborted and must be discarded.
func (tx *Txn) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.checkActive(); err != nil {
		return err
	}
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if err := tx.db.commitLocked(tx); err != nil {
		tx.state = txnAborted
		return err
	}
	tx.state = txnCommitted
	return nil
}

// Rollback discards the transaction's write set. It is safe to call
// instead of Commit; after it, the transaction is closed.
func (tx *Txn) Rollback() {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state == txnActive {
		tx.state = txnAborted
	}
}
