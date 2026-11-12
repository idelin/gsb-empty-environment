package netsec

import "errors"

var (
	// ErrInvalidConfig reports an invalid Encryptor or Decryptor configuration.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")

	// ErrInvalidMessage reports malformed message metadata.
	ErrInvalidMessage = errors.New("netsec: invalid message")

	// ErrUnknownKeyName reports a metadata key name outside the registered set.
	ErrUnknownKeyName = errors.New("netsec: unregistered key name")

	// ErrDuplicateKeyName reports duplicate key names in message metadata.
	ErrDuplicateKeyName = errors.New("netsec: duplicate key name")

	// ErrNoMatchingKey reports that no private key matches the message key list.
	ErrNoMatchingKey = errors.New("netsec: no matching private key")

	// ErrDecrypt reports a wrapped-key or payload authentication failure.
	ErrDecrypt = errors.New("netsec: decryption failed")

	// ErrNonceExhausted reports that a data key has no nonce counter values left.
	// Encrypt rotates instead of returning this error, but injected lower layers
	// may still surface it.
	ErrNonceExhausted = errors.New("netsec: nonce counter exhausted")
)
