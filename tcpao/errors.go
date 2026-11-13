package tcpao

import "errors"

var (
	// ErrInvalidKey reports malformed key material or validity metadata.
	ErrInvalidKey = errors.New("tcpao: invalid key")

	// ErrDuplicateKey reports an attempt to register an already-known KeyID.
	ErrDuplicateKey = errors.New("tcpao: duplicate key id")

	// ErrUnsupportedAlgorithm reports a MAC algorithm not implemented by this
	// package.
	ErrUnsupportedAlgorithm = errors.New("tcpao: unsupported MAC algorithm")

	// ErrUnknownKey reports that an inbound KeyID is not registered.
	ErrUnknownKey = errors.New("tcpao: unknown key id")

	// ErrReceiveKeyNotYetValid reports that the matching key's receive validity
	// starts in the future.
	ErrReceiveKeyNotYetValid = errors.New("tcpao: receive key not yet valid")

	// ErrReceiveKeyExpired reports that the matching key's receive validity
	// has ended. Replayed segments using a retired key fail this check.
	ErrReceiveKeyExpired = errors.New("tcpao: receive key expired")

	// ErrAuthenticationFailed reports a missing, malformed, or incorrect MAC.
	ErrAuthenticationFailed = errors.New("tcpao: authentication failed")

	// ErrNextKeyUnknown reports that RNextKeyID names an unregistered key.
	ErrNextKeyUnknown = errors.New("tcpao: next key is not registered")

	// ErrNextKeyNotYetValid reports that RNextKeyID names a key whose send
	// validity has not started.
	ErrNextKeyNotYetValid = errors.New("tcpao: next key is not send-valid yet")

	// ErrNextKeySendExpired reports that RNextKeyID names a key whose send
	// validity has ended.
	ErrNextKeySendExpired = errors.New("tcpao: next key send validity expired")

	// ErrNoSendKey reports that no send key has been selected.
	ErrNoSendKey = errors.New("tcpao: no send key")

	// ErrSendKeyUnknown reports that the selected send key is no longer
	// registered.
	ErrSendKeyUnknown = errors.New("tcpao: selected send key is not registered")

	// ErrSendKeyNotYetValid reports that the selected send key's send validity
	// starts in the future.
	ErrSendKeyNotYetValid = errors.New("tcpao: send key not yet valid")

	// ErrSendKeyExpired reports that the selected send key's send validity has
	// ended.
	ErrSendKeyExpired = errors.New("tcpao: send key expired")
)
