package netsec

import "errors"

var (
	// ErrHandshakeIncomplete is returned when an application asks for the
	// peer identity before the XX handshake has completed.
	ErrHandshakeIncomplete = errors.New("netsec: handshake has not completed")

	// ErrFirstMessagePayload is returned when a payload is attached to the
	// first XX handshake message. The first message may only contain the
	// initiator's ephemeral public key.
	ErrFirstMessagePayload = errors.New("netsec: first handshake message payload must be empty")

	// ErrIdentityPayloadMissing is returned when the second or third
	// handshake message does not carry an identity payload.
	ErrIdentityPayloadMissing = errors.New("netsec: handshake message has no identity payload")

	// ErrInvalidIdentityPayload is returned for malformed identity payloads.
	ErrInvalidIdentityPayload = errors.New("netsec: malformed identity payload")

	// ErrStaticKeyMismatch is returned when the static public key named by
	// the identity payload is not the static key negotiated by the handshake.
	ErrStaticKeyMismatch = errors.New("netsec: identity payload is bound to a different static public key")

	// ErrInvalidIdentitySignature is returned when the Ed25519 signature
	// does not authenticate the handshake hash and static public key.
	ErrInvalidIdentitySignature = errors.New("netsec: invalid identity payload signature")

	// ErrIdentityAlreadyPublished is returned when a peer tries to publish a
	// second identity for the same handshake.
	ErrIdentityAlreadyPublished = errors.New("netsec: peer identity already published")

	// ErrUnexpectedHandshakeMessage is returned when the caller attempts to
	// write or read a message out of turn.
	ErrUnexpectedHandshakeMessage = errors.New("netsec: unexpected handshake message")

	// ErrInvalidHandshakeMessage is returned for truncated or malformed
	// Noise handshake messages.
	ErrInvalidHandshakeMessage = errors.New("netsec: invalid handshake message")

	// ErrInvalidKey is returned for malformed X25519 or Ed25519 keys.
	ErrInvalidKey = errors.New("netsec: invalid key")
)
