package netsec

import "errors"

var (
	ErrMalformedMessage      = errors.New("malformed ssh agent message")
	ErrNotUserAuthRequest    = errors.New("signed data is not an ssh user authentication request")
	ErrUnsupportedAuthMethod = errors.New("unsupported ssh user authentication method")
	ErrKeyNotFound           = errors.New("key is not registered")
	ErrMissingSessionBinding = errors.New("missing ssh agent session binding")
	ErrDuplicateNode         = errors.New("duplicate node in forwarding chain")
	ErrSessionMismatch       = errors.New("session binding does not match signed authentication data")
	ErrDestinationMismatch   = errors.New("forwarding chain does not match an allowed destination")
	ErrBindingSignature      = errors.New("session binding signature verification failed")
	ErrNoSigner              = errors.New("no signing callback configured")
)
