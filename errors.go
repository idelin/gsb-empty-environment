package netsec

import "errors"

var (
	// ErrObjectNotFound is returned for every handle that the caller is not
	// entitled to use. In particular, the same error is returned for a handle
	// that never existed and for a handle owned by another tenant. Callers
	// must not be able to distinguish those two cases.
	ErrObjectNotFound = errors.New("netsec: object not found")

	// ErrUnknownHandle and ErrHandleNotFound are aliases kept for callers that
	// use handle-oriented terminology. They intentionally share the exact
	// value of ErrObjectNotFound so their errors are indistinguishable.
	ErrUnknownHandle  = ErrObjectNotFound
	ErrHandleNotFound = ErrObjectNotFound

	// ErrSessionClosed is returned when an operation is attempted on a
	// session that has already been closed.
	ErrSessionClosed = errors.New("netsec: session closed")

	// ErrNotLoggedIn is returned when an operation requires the tenant to be
	// logged in, but the tenant has no active login.
	ErrNotLoggedIn = errors.New("netsec: not logged in")

	// ErrInvalidPIN is returned when a configured tenant PIN does not match.
	ErrInvalidPIN = errors.New("netsec: invalid PIN")

	// ErrInvalidTenant is returned for an empty tenant identifier.
	ErrInvalidTenant = errors.New("netsec: invalid tenant")

	// ErrTenantExists is returned when registering an already registered
	// tenant.
	ErrTenantExists = errors.New("netsec: tenant already registered")

	// ErrEntropy is returned when the system cannot produce a random handle.
	ErrEntropy = errors.New("netsec: cannot generate object handle")
)
