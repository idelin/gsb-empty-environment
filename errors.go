package taskpkg

import "errors"

// Sentinel errors. Callers should use errors.Is to distinguish them.
var (
	// ErrCounterUnavailable is returned when the injected monotonic counter
	// cannot be read or advanced. The commit fails closed and the side effect
	// is not executed.
	ErrCounterUnavailable = errors.New("taskpkg: monotonic counter unavailable")

	// ErrCounterRolledBack is returned when the counter reports a sequence
	// that was already consumed (or otherwise moved backwards), which proves a
	// counter snapshot has been rolled back.
	ErrCounterRolledBack = errors.New("taskpkg: monotonic counter rolled back")

	// ErrCounterGap is returned when the counter skipped over a sequence that
	// the local log has already recorded, indicating log/counter desync.
	ErrCounterGap = errors.New("taskpkg: monotonic counter sequence gap")

	// ErrWitnessUnavailable is returned when the remote witness callback
	// cannot be reached or returns an invalid transport-level response.
	ErrWitnessUnavailable = errors.New("taskpkg: witness callback unavailable")

	// ErrBadSignature is returned when a witness confirmation fails Ed25519
	// verification or binds the wrong fields.
	ErrBadSignature = errors.New("taskpkg: invalid witness signature")

	// ErrReplayedConfirmation is returned when a confirmation carrying an
	// already-consumed sequence is presented again.
	ErrReplayedConfirmation = errors.New("taskpkg: witness confirmation replayed")

	// ErrLogTampered is returned when the persisted audit log fails hash-chain
	// or signature verification.
	ErrLogTampered = errors.New("taskpkg: audit log tampered or truncated")

	// ErrLogMissing is returned when the audit log file is absent, including
	// the case where an administrator deleted it.
	ErrLogMissing = errors.New("taskpkg: audit log missing")

	// ErrLogCounterDesync is returned when the highest sequence in the local
	// log does not match the counter's next sequence, i.e. an old local
	// snapshot is being presented against a live counter.
	ErrLogCounterDesync = errors.New("taskpkg: audit log and counter out of sync")
)
