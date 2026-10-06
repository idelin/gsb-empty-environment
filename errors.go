package memtx

import "errors"

var (
	// ErrKeyNotFound is returned by Txn.Get when the key is absent from both
	// the transaction's write set and its snapshot.
	ErrKeyNotFound = errors.New("memtx: key not found")

	// ErrTxnClosed is returned when an operation is attempted on a
	// transaction that has already committed or rolled back.
	ErrTxnClosed = errors.New("memtx: transaction is closed")

	// ErrConflict is returned by Txn.Commit when another transaction
	// committed a write to a key in this transaction's write set after
	// this transaction's snapshot was taken.
	ErrConflict = errors.New("memtx: write conflict")

	// ErrStaleCursor is returned by Txn.Scan when the supplied cursor was
	// issued against a different snapshot or an older write-set revision
	// (i.e. the write set changed after the cursor was created).
	ErrStaleCursor = errors.New("memtx: stale cursor")
)
