package netsec

import "errors"

const (
	// MaxSkippedKeysPerChain is the hard upper bound on message keys that a
	// single receive chain may skip. The bound is enforced before any key
	// derivation work is performed.
	MaxSkippedKeysPerChain = 1000

	// ChainKeySize is the size of a symmetric ratchet chain key in bytes.
	ChainKeySize = 32

	// MessageKeySize is the size of a derived AEAD message key in bytes.
	MessageKeySize = 32
)

// Aliases kept for callers that use the terminology from the Signal
// specification.
const (
	MaxSkip               = MaxSkippedKeysPerChain
	DefaultMaxSkippedKeys = MaxSkippedKeysPerChain
)

var (
	// ErrSkipLimitExceeded is returned when a message sequence number would
	// require skipping more than the configured (at most 1000) message keys.
	ErrSkipLimitExceeded = errors.New("netsec: skip limit exceeded")

	// ErrTooManySkippedKeys is an alias of ErrSkipLimitExceeded.
	ErrTooManySkippedKeys = ErrSkipLimitExceeded

	// ErrDuplicateMessage is returned for a message whose sequence number has
	// already been consumed, or whose one-shot skipped key has been used.
	ErrDuplicateMessage = errors.New("netsec: duplicate or already-used message")

	// ErrReplay and ErrOldMessage are aliases of ErrDuplicateMessage.
	ErrReplay     = ErrDuplicateMessage
	ErrOldMessage = ErrDuplicateMessage

	// ErrInvalidChainKey is returned when a chain key is not exactly 32 bytes.
	ErrInvalidChainKey = errors.New("netsec: chain key must be 32 bytes")

	// ErrInvalidRatchetKey is returned when a ratchet public key is empty.
	ErrInvalidRatchetKey = errors.New("netsec: ratchet public key must not be empty")

	// ErrInvalidMaxSkippedKeys is returned when a skip limit outside of
	// [0, MaxSkippedKeysPerChain] is requested.
	ErrInvalidMaxSkippedKeys = errors.New("netsec: max skipped keys must be between 0 and 1000")

	// ErrInvalidNonce is returned when a nonce is not 12 bytes.
	ErrInvalidNonce = errors.New("netsec: nonce must be 12 bytes")

	// ErrInvalidCiphertext is returned when a ciphertext cannot contain an
	// AES-GCM authentication tag.
	ErrInvalidCiphertext = errors.New("netsec: ciphertext is too short")

	// ErrNilOpener is returned when a decrypt callback is nil.
	ErrNilOpener = errors.New("netsec: nil message key opener")

	// ErrNilStore is returned when a nil skipped-key store is supplied.
	ErrNilStore = errors.New("netsec: nil skipped key store")

	// ErrInvalidMessageKey is returned when a message key is not exactly 32
	// bytes.
	ErrInvalidMessageKey = errors.New("netsec: message key must be 32 bytes")

	// ErrSequenceOverflow is returned when advancing would wrap the sequence
	// number space.
	ErrSequenceOverflow = errors.New("netsec: message sequence overflow")

	// ErrInvalidSequence is returned for a negative sequence number.
	ErrInvalidSequence = errors.New("netsec: message sequence must not be negative")
)
