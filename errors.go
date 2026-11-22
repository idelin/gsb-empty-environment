package disclosure

import "errors"

var (
	// ErrInvalidQuery means that the requested tenant or interval is malformed.
	ErrInvalidQuery = errors.New("disclosure: invalid query")
	// ErrInvalidLeaf means that a leaf is malformed or violates append-only
	// version-chain rules.
	ErrInvalidLeaf = errors.New("disclosure: invalid leaf")
	// ErrInvalidCheckpoint means that checkpoint fields are inconsistent.
	ErrInvalidCheckpoint = errors.New("disclosure: invalid checkpoint")
	// ErrCheckpointAuthentication means that the caller's checkpoint verifier
	// rejected the signed checkpoint.
	ErrCheckpointAuthentication = errors.New("disclosure: checkpoint authentication failed")
	// ErrInvalidProof means that a Merkle path does not lead to the committed
	// root.
	ErrInvalidProof = errors.New("disclosure: invalid proof")
	// ErrCrossRoot means that a proof was taken from a different checkpoint or
	// tree than the one being verified.
	ErrCrossRoot = errors.New("disclosure: cross-checkpoint or cross-root splice")
	// ErrIncompleteIndexRange means that the committed index range was not
	// returned contiguously, or its boundaries leave a possible gap.
	ErrIncompleteIndexRange = errors.New("disclosure: incomplete index range")
	// ErrMissingLeaf means that a leaf needed by an index entry or correction
	// chain was not disclosed.
	ErrMissingLeaf = errors.New("disclosure: required leaf missing")
	// ErrCorrectionMismatch means that a correction, tombstone, or index entry
	// does not agree with the same checkpoint's committed state.
	ErrCorrectionMismatch = errors.New("disclosure: correction or index state mismatch")
	// ErrUnauthorizedTombstone means that a tombstone's authorization was not
	// accepted by the configured authorizer.
	ErrUnauthorizedTombstone = errors.New("disclosure: unauthorized tombstone")
	// ErrBodyCommitment means that a disclosed body does not match its
	// committed digest.
	ErrBodyCommitment = errors.New("disclosure: body commitment mismatch")
)
