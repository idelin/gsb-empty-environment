package netsec

import "errors"

var (
	// ErrInvalidConfig is returned when a Config violates the fixed model.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")

	// ErrBlockNotFound means that the requested logical block has never been
	// written and therefore has no position in the client position map.
	ErrBlockNotFound = errors.New("netsec: block not found")

	// ErrInvalidData means that a write did not supply exactly
	// Config.BlockDataSize bytes.
	ErrInvalidData = errors.New("netsec: invalid block data length")

	// ErrStashOverflow means that the decrypted stash exceeded
	// Config.StashCapacity.  Such a transaction is aborted before any write
	// back is staged.
	ErrStashOverflow = errors.New("netsec: stash capacity exceeded")

	// ErrVersionConflict covers duplicate block versions with different
	// contents and non-consecutive versions arriving through the path.
	ErrVersionConflict = errors.New("netsec: block version conflict")

	// ErrIntegrity means that a bucket, block MAC, bucket header, or receipt
	// failed authentication.
	ErrIntegrity = errors.New("netsec: integrity check failed")

	// ErrRollback means that the server presented a tree older than the
	// client's committed root.
	ErrRollback = errors.New("netsec: server tree rollback detected")

	// ErrServerStateMismatch means that the server root differs from the
	// committed client root at the same transaction number.
	ErrServerStateMismatch = errors.New("netsec: server state does not match committed root")

	// ErrUnexpectedTransaction means that the server advanced beyond the
	// transaction the client is willing to accept.
	ErrUnexpectedTransaction = errors.New("netsec: unexpected server transaction")

	// ErrReceipt is returned for an invalid, stale, or replayed root receipt.
	ErrReceipt = errors.New("netsec: invalid root receipt")

	// ErrTransactionOverflow means that the monotonic transaction counter
	// cannot be advanced without wrapping.
	ErrTransactionOverflow = errors.New("netsec: transaction counter overflow")

	// ErrMissingBucket is returned by a host when a bucket was deleted.
	ErrMissingBucket = errors.New("netsec: bucket missing")

	// ErrIncompletePathWrite is returned by a host when a path commit does
	// not contain every bucket in the pre-drawn path.
	ErrIncompletePathWrite = errors.New("netsec: incomplete path write")

	// ErrDroppedWrite is returned by a host that deliberately discarded one
	// bucket of a path write.
	ErrDroppedWrite = errors.New("netsec: write back dropped")

	// ErrGeneration means that a staged bucket did not extend the live
	// bucket's generation by exactly one.
	ErrGeneration = errors.New("netsec: bad bucket generation")

	// ErrAlreadyInitialized is returned if Initialize is called twice.
	ErrAlreadyInitialized = errors.New("netsec: tree already initialized")

	// ErrNotInitialized is returned by a host before Initialize.
	ErrNotInitialized = errors.New("netsec: tree not initialized")

	// ErrRecoveryRequired means that a previous transaction left an intent
	// journal entry that could not be replayed.
	ErrRecoveryRequired = errors.New("netsec: previous transaction must be recovered")
)
