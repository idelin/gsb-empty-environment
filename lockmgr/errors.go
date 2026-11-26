package lockmgr

import "errors"

// Sentinel errors returned by the lock manager.
var (
	// ErrIncompatible means the requested mode conflicts with a lock that a
	// concurrent transaction currently holds.
	ErrIncompatible = errors.New("lockmgr: requested lock mode is incompatible with held locks")

	// ErrIntentionViolation means the top-down intention lock prerequisite on
	// an ancestor resource is not satisfied.
	ErrIntentionViolation = errors.New("lockmgr: ancestor intention lock prerequisite not satisfied")

	// ErrDeadlockRollback is returned on the victim's blocked Lock call after
	// the manager rolled it back to a savepoint or aborted it to break a
	// deadlock cycle.
	ErrDeadlockRollback = errors.New("lockmgr: transaction chosen as deadlock victim")

	// ErrTxnNotActive is returned when an operation references a transaction
	// that has already been committed or aborted.
	ErrTxnNotActive = errors.New("lockmgr: transaction is not active")

	// ErrTxnBusy is returned when a transaction issues a second Lock request
	// while a previous request of that transaction is still blocked.
	ErrTxnBusy = errors.New("lockmgr: transaction already has a blocked lock request")

	// ErrSavepointNotFound is returned when rolling back to an unknown or
	// already released savepoint.
	ErrSavepointNotFound = errors.New("lockmgr: savepoint not found")
)
