package noise

import "errors"

var (
	// ErrUnsupportedPattern is returned for a pattern other than XX, KK, IK,
	// or XXfallback.
	ErrUnsupportedPattern = errors.New("noise: unsupported handshake pattern")
	// ErrInvalidConfig indicates missing or inconsistent handshake inputs.
	ErrInvalidConfig = errors.New("noise: invalid handshake configuration")
	// ErrInvalidKey indicates a malformed X25519 key.
	ErrInvalidKey = errors.New("noise: invalid X25519 key")
	// ErrLowOrderPoint indicates an X25519 DH result of all zeroes.
	ErrLowOrderPoint = errors.New("noise: low-order X25519 point")
	// ErrInvalidMessage indicates a truncated or structurally invalid message.
	ErrInvalidMessage = errors.New("noise: invalid handshake message")
	// ErrAuthenticationFailed indicates AEAD authentication failure.
	ErrAuthenticationFailed = errors.New("noise: authentication failed")
	// ErrUnexpectedMessage indicates a message arriving on the wrong turn.
	ErrUnexpectedMessage = errors.New("noise: unexpected handshake message")
	// ErrHandshakeComplete indicates that the final handshake message has
	// already been processed.
	ErrHandshakeComplete = errors.New("noise: handshake complete")
	// ErrHandshakeIncomplete indicates an attempt to Split too early.
	ErrHandshakeIncomplete = errors.New("noise: handshake incomplete")
	// ErrNonceExhausted indicates that the 64-bit nonce reached MaxUint64.
	ErrNonceExhausted = errors.New("noise: nonce exhausted")
	// ErrStateDestroyed indicates use of a destroyed state object.
	ErrStateDestroyed = errors.New("noise: state destroyed")
	// ErrInvalidPrologue indicates that a fallback transition did not preserve
	// the original prologue.
	ErrInvalidPrologue = errors.New("noise: prologue mismatch")
)
