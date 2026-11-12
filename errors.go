package taskpkg

import "errors"

// Structural and authorization errors returned while validating an update
// round. They are deliberately sentinels so callers can classify failures
// without parsing messages.
var (
	// ErrEmptyRound is returned when no segment is submitted.
	ErrEmptyRound = errors.New("taskpkg: update round contains no segments")

	// ErrSegmentIndex is returned when segments are not numbered 0..N-1
	// without gaps or duplicates.
	ErrSegmentIndex = errors.New("taskpkg: segment sequence must be zero-based, contiguous and unique")

	// ErrZoneMismatch is returned when a segment targets a different zone
	// than the accepting store, or when segments disagree on the zone.
	ErrZoneMismatch = errors.New("taskpkg: segment zone identity does not match the update context")

	// ErrVersionMismatch is returned when a segment does not start at the
	// store's current version, does not advance it, or disagrees with the
	// other segments of the round.
	ErrVersionMismatch = errors.New("taskpkg: segment versions do not match the current chain state")

	// ErrChainLink is returned when a segment's predecessor digest does not
	// equal the digest of the preceding segment, which includes splicing a
	// segment from a different round.
	ErrChainLink = errors.New("taskpkg: segment predecessor digest breaks the chain")

	// ErrFinalSegment is returned when the last segment is not marked final
	// or a non-final segment is marked final.
	ErrFinalSegment = errors.New("taskpkg: exactly the last segment must be marked final")

	// ErrRoundDigest is returned when the final segment's signed round
	// digest or segment count does not match the submitted chain.
	ErrRoundDigest = errors.New("taskpkg: final segment round digest or segment count does not match")

	// ErrUnauthorized is returned when no authorized master key verifies a
	// segment signature.
	ErrUnauthorized = errors.New("taskpkg: segment signature is not from an authorized master")

	// ErrSignature is returned when a segment signature fails verification.
	ErrSignature = errors.New("taskpkg: segment signature verification failed")

	// ErrInvalidSegment is returned when a segment itself is malformed, for
	// example an empty operation key or a malformed operation type.
	ErrInvalidSegment = errors.New("taskpkg: malformed segment or operation")

	// ErrDeleteMismatch is returned when a delete operation does not match
	// the digest of the record currently staged under its key.
	ErrDeleteMismatch = errors.New("taskpkg: delete does not match the current record digest")

	// ErrPrivateKey is returned by the builder when no signing key or an
	// invalid key is supplied.
	ErrPrivateKey = errors.New("taskpkg: ed25519 private key is required")
)
