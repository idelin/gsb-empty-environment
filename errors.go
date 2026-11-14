package netsec

import "errors"

var (
	// ErrInvalidConfig is returned when a seal configuration or holder set is
	// malformed.
	ErrInvalidConfig = errors.New("netsec: invalid seal configuration")

	// ErrAlreadyInitialized is returned when Initialize is called on a state
	// that has already been initialized; use Rotate to start a new generation.
	ErrAlreadyInitialized = errors.New("netsec: unseal state already initialized")

	// ErrNotInitialized is returned when a share is submitted before the state
	// has been sealed.
	ErrNotInitialized = errors.New("netsec: unseal state is not initialized")

	// ErrAlreadyUnsealed is returned when a share is submitted after the
	// current generation has already been unsealed. Unsealing is one-time.
	ErrAlreadyUnsealed = errors.New("netsec: generation already unsealed")

	// ErrStaleGeneration is returned when a share is bound to an older
	// generation than the one currently accepting shares.
	ErrStaleGeneration = errors.New("netsec: share belongs to a stale generation")

	// ErrFutureGeneration is returned when a share is bound to a generation
	// that the state has not reached yet.
	ErrFutureGeneration = errors.New("netsec: share belongs to a future generation")

	// ErrSealConfigMismatch is returned when the seal configuration digest
	// embedded in a share does not match the active seal configuration.
	ErrSealConfigMismatch = errors.New("netsec: seal configuration mismatch")

	// ErrNonceMismatch is returned when the nonce embedded in a share does not
	// match the nonce of the active sealing.
	ErrNonceMismatch = errors.New("netsec: unseal nonce mismatch")

	// ErrUnknownHolder is returned when the share holder is not part of the
	// active seal holder set.
	ErrUnknownHolder = errors.New("netsec: unknown share holder")

	// ErrHolderIndexMismatch is returned when the share index does not match
	// the index registered for the declared holder.
	ErrHolderIndexMismatch = errors.New("netsec: holder/share index mismatch")

	// ErrInvalidTag is returned when the share authentication tag cannot be
	// verified against the holder key and the bound context.
	ErrInvalidTag = errors.New("netsec: share authentication tag is invalid")

	// ErrDuplicateShare is returned when the same holder submits more than one
	// share for the current generation, including replayed shares.
	ErrDuplicateShare = errors.New("netsec: duplicate share from holder")

	// ErrGenerationRollback is returned when an imported snapshot would move
	// the state to an older generation than it has already observed.
	ErrGenerationRollback = errors.New("netsec: generation rollback rejected")

	// ErrSnapshotIntegrity is returned when a snapshot is malformed or its
	// authentication tag does not verify.
	ErrSnapshotIntegrity = errors.New("netsec: snapshot integrity check failed")

	// ErrNoStateKey is returned when snapshot import/export is attempted
	// without configuring a state integrity key.
	ErrNoStateKey = errors.New("netsec: snapshot state key is required")

	// ErrNotUnsealed is returned when the unsealed key is requested while the
	// state is still sealed.
	ErrNotUnsealed = errors.New("netsec: state has not been unsealed")
)
