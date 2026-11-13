package netsec

import "errors"

var (
	// ErrOpen is the sole decryption error exposed by Envelope.Open.
	// Unknown or duplicate key identifiers, malformed records, commitment
	// mismatches, and AEAD failures all return exactly this error.
	ErrOpen = errors.New("netsec: envelope open failed")

	// ErrKeyUnavailable is returned by Seal when a key identifier is unknown,
	// duplicated, or resolves to invalid key material.
	ErrKeyUnavailable = errors.New("netsec: key unavailable")

	// ErrInvalidKey reports invalid key material.
	ErrInvalidKey = errors.New("netsec: invalid key")

	// ErrInvalidKeyID reports an empty or overlong key identifier.
	ErrInvalidKeyID = errors.New("netsec: invalid key identifier")

	// ErrInputTooLarge reports an input exceeding a published size limit.
	ErrInputTooLarge = errors.New("netsec: input exceeds envelope limit")

	// ErrMalformedRecord reports a non-canonical or invalid record encoding.
	ErrMalformedRecord = errors.New("netsec: malformed record")

	// ErrRandomness reports a failure to obtain a nonce from the random source.
	ErrRandomness = errors.New("netsec: nonce generation failed")

	// ErrAEAD reports an injected-AEAD construction or size failure.
	ErrAEAD = errors.New("netsec: aead failure")

	// ErrStoreConflict reports that an atomic record replacement lost a race.
	ErrStoreConflict = errors.New("netsec: record store conflict")

	// ErrNoRecord reports that a record store contains no record.
	ErrNoRecord = errors.New("netsec: no record")

	// ErrInvalidConfiguration reports missing envelope dependencies.
	ErrInvalidConfiguration = errors.New("netsec: invalid configuration")
)
