package credentialauth

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidInput marks malformed or ambiguous merge input.
	ErrInvalidInput = errors.New("invalid merge input")
	// ErrTooManyDevices marks a snapshot containing more than MaxDevices
	// distinct device IDs.
	ErrTooManyDevices = errors.New("too many devices")
	// ErrDeviceFork marks divergent states for one device boot generation.
	ErrDeviceFork = errors.New("device boot generation fork")
	// ErrCounterRollback marks a replayed or regressed signature counter.
	ErrCounterRollback = errors.New("signature counter rollback")
	// ErrBatchConflict marks two different summaries with the same batch ID.
	ErrBatchConflict = errors.New("conflicting batch summary")
	// ErrUnknownCredential marks a batch candidate that has no signed source
	// receipt.
	ErrUnknownCredential = errors.New("credential has no signed source")
	// ErrRPIsolation marks a credential reference that crosses RP identity.
	ErrRPIsolation = errors.New("credential RP identity mismatch")
	// ErrNoGlobalAssignment marks a set of batches that satisfy their local
	// capacity checks but have no global one-to-one assignment.
	ErrNoGlobalAssignment = errors.New("no global transaction-credential assignment")
	// ErrCandidatesExhausted marks a permit request whose candidates are all
	// reserved, forbidden by purpose, or unavoidable in every completion.
	ErrCandidatesExhausted = errors.New("permit candidates exhausted")
	// ErrSnapshotConflict marks a different permit request for a snapshot
	// which has already issued a mutually exclusive lock.
	ErrSnapshotConflict = errors.New("snapshot already has a different consumption lock")
	// ErrProofMismatch marks a proof that is not the deterministic proof of the
	// supplied snapshot.
	ErrProofMismatch = errors.New("consumption proof does not match snapshot")
	// ErrOracleLimit marks a brute-force search that exceeded its node limit.
	ErrOracleLimit = errors.New("oracle enumeration limit exceeded")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}
