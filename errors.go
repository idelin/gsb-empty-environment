package netsec

import "errors"

var (
	// ErrEndpointUnknown is returned when no endpoint is registered under
	// the requested name.
	ErrEndpointUnknown = errors.New("netsec: endpoint name is not registered")

	// ErrEndpointRevoked is returned when an operation targets an endpoint
	// instance that has been revoked or replaced by a newer registration.
	ErrEndpointRevoked = errors.New("netsec: endpoint instance revoked")

	// ErrConnRejected is returned when a handshake fails because no
	// endpoint currently matches the name and challenge presented.
	ErrConnRejected = errors.New("netsec: connection handshake rejected")

	// ErrConnUnauthenticated is returned when a message is delivered on a
	// connection whose authentication callback has not completed yet.
	ErrConnUnauthenticated = errors.New("netsec: connection not authenticated")

	// ErrConnClosed is returned when a message is delivered on a closed
	// connection.
	ErrConnClosed = errors.New("netsec: connection closed")

	// ErrChallengeMismatch is returned by an authentication callback whose
	// response does not prove possession of the challenge issued for the
	// connection.
	ErrChallengeMismatch = errors.New("netsec: challenge response mismatch")
)
