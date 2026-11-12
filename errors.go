package netsec

import "errors"

// Sentinel errors returned by the barrier and the coordinator. Callers can
// use errors.Is to inspect the failure category without parsing messages.
var (
	// ErrInvalidManifest indicates that a window manifest is malformed.
	ErrInvalidManifest = errors.New("barrier: invalid window manifest")

	// ErrUnknownPartition indicates that a seal targets a partition that is
	// not part of the current window manifest.
	ErrUnknownPartition = errors.New("barrier: partition not in window manifest")

	// ErrForeignWindow indicates that a seal was stamped for a window other
	// than the barrier's current window. Accepting it would mix generations.
	ErrForeignWindow = errors.New("barrier: seal belongs to a different window")

	// ErrSequenceGap indicates that a seal arrived out of order: the
	// preceding sequence numbers for the partition have not been proven.
	ErrSequenceGap = errors.New("barrier: missing preceding seal (prefix gap)")

	// ErrSequenceAboveBound indicates that a seal exceeds the partition's
	// inclusive upper bound declared by the window manifest.
	ErrSequenceAboveBound = errors.New("barrier: seal sequence above window upper bound")

	// ErrDuplicateConflict indicates that the same partition and sequence
	// were already sealed with a different digest. Two generations cannot
	// occupy one slot.
	ErrDuplicateConflict = errors.New("barrier: conflicting duplicate seal")

	// ErrChainBroken indicates that a seal does not hash-chain onto the
	// previous accepted seal (or onto the partition anchor for seq 1).
	ErrChainBroken = errors.New("barrier: seal chain linkage mismatch")

	// ErrBadDigest indicates that a seal's Digest does not match the
	// canonical digest of its own fields.
	ErrBadDigest = errors.New("barrier: seal digest mismatch")

	// ErrIncomplete indicates that the window cannot be finalized because at
	// least one partition has not proven its full continuous prefix.
	ErrIncomplete = errors.New("barrier: window prefix proof is incomplete")

	// ErrUnknownWindow indicates that an attestation or an advance request
	// does not belong to the coordinator's current window.
	ErrUnknownWindow = errors.New("barrier: unknown window")

	// ErrInvalidMigration indicates that a pending migration is malformed or
	// references an unknown partition.
	ErrInvalidMigration = errors.New("barrier: invalid partition migration")

	// ErrSignature indicates that an attestation signature does not verify.
	ErrSignature = errors.New("barrier: attestation signature verification failed")
)
