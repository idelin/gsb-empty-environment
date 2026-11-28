package ssidb

import "errors"

// Predicate-lock / SSI failure modes. All of them map to SQLSTATE 40001
// (serialization_failure) in a real SQL engine.
var (
	// ErrSerialization is raised when SSI breaks a dangerous rw-antidependency
	// structure by aborting one of the participating transactions.
	ErrSerialization = errors.New("ssidb: could not serialize access due to read/write dependencies among transactions (40001)")

	// ErrWriteWriteConflict is raised under first-updater-wins SI when a
	// transaction writes a key that a concurrent transaction already wrote.
	ErrWriteWriteConflict = errors.New("ssidb: could not serialize access due to concurrent update (write/write conflict, 40001)")

	// ErrAborted wraps the failure of a transaction that was rolled back
	// explicitly via Rollback.
	ErrAborted = errors.New("ssidb: transaction has been rolled back")

	// ErrTxnClosed is returned when an ended transaction is used again.
	ErrTxnClosed = errors.New("ssidb: transaction has already ended")

	// ErrReadOnly is returned when a read-only transaction attempts a write.
	ErrReadOnly = errors.New("ssidb: cannot execute write in a read-only transaction")
)

// IsSerializationFailure reports whether err is a 40001-class failure:
// an SSI abort, a first-updater-wins write conflict, or an explicit rollback.
func IsSerializationFailure(err error) bool {
	return errors.Is(err, ErrSerialization) ||
		errors.Is(err, ErrWriteWriteConflict) ||
		errors.Is(err, ErrAborted)
}
