package netsec

import "errors"

var (
	// ErrEmptyMessage is returned when a message contains no chunks.
	ErrEmptyMessage = errors.New("netsec: empty message")

	// ErrSequenceJump is returned when the chunk sequence skips a number.
	ErrSequenceJump = errors.New("netsec: chunk sequence jump")

	// ErrSequenceOrder is returned when a chunk sequence moves backwards.
	ErrSequenceOrder = errors.New("netsec: chunk sequence out of order")

	// ErrSequenceReuse is returned by strict validation when two chunks use
	// the same sequence number.
	ErrSequenceReuse = errors.New("netsec: chunk sequence reused")

	// ErrDuplicateFinal is returned when more than one final chunk marker is
	// present.
	ErrDuplicateFinal = errors.New("netsec: duplicate final chunk")

	// ErrFinalNotLast is returned when a final chunk is followed by data.
	ErrFinalNotLast = errors.New("netsec: final chunk is not last")

	// ErrMissingFinal is returned by strict validation when the final marker
	// is absent.
	ErrMissingFinal = errors.New("netsec: missing final chunk")

	// ErrInvalidFinal is returned when the final flag and the final sequence
	// marker disagree.
	ErrInvalidFinal = errors.New("netsec: invalid final chunk marker")

	// ErrAuthentication is returned when an AEAD tag does not verify.
	ErrAuthentication = errors.New("netsec: authentication failed")

	// ErrNonceMismatch is returned when a chunk carries a nonce that does
	// not match the nonce derived from its sequence number.
	ErrNonceMismatch = errors.New("netsec: nonce does not match sequence")

	// ErrChunkTooLarge is returned when a plaintext chunk exceeds the
	// configured chunk size.
	ErrChunkTooLarge = errors.New("netsec: plaintext chunk too large")

	// ErrInvalidChunkSize is returned for a chunk size that is not a power of
	// two representable by the SEIPD chunk-size exponent.
	ErrInvalidChunkSize = errors.New("netsec: invalid chunk size")

	// ErrInvalidKey is returned for an empty or incorrectly sized session
	// key.
	ErrInvalidKey = errors.New("netsec: invalid session key")

	// ErrInvalidSalt is returned for a malformed salt.
	ErrInvalidSalt = errors.New("netsec: invalid salt")

	// ErrInvalidNonce is returned for a malformed IV.
	ErrInvalidNonce = errors.New("netsec: invalid nonce")

	// ErrUnsupportedCipher is returned for an unsupported symmetric cipher.
	ErrUnsupportedCipher = errors.New("netsec: unsupported cipher")

	// ErrUnsupportedMode is returned for an unsupported AEAD mode.
	ErrUnsupportedMode = errors.New("netsec: unsupported AEAD mode")

	// ErrMalformedPacket is returned when a serialized message cannot be
	// parsed.
	ErrMalformedPacket = errors.New("netsec: malformed SEIPD message")
)
