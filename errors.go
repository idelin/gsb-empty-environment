package netsec

import "errors"

var (
	// ErrInvalidFilter marks a filter that cannot be installed.
	ErrInvalidFilter = errors.New("netsec: invalid filter")
	// ErrInvalidAction marks an action outside the supported priority order.
	ErrInvalidAction = errors.New("netsec: invalid action")
	// ErrInvalidRule marks a malformed rule.
	ErrInvalidRule = errors.New("netsec: invalid rule")
	// ErrInvalidArg marks a malformed argument predicate.
	ErrInvalidArg = errors.New("netsec: invalid argument predicate")
	// ErrInstallFailed marks a failure while installing a composed chain on a
	// thread. The host state is unchanged when this error is returned.
	ErrInstallFailed = errors.New("netsec: filter installation failed")
	// ErrUnknownThread marks an operation on a thread that is not registered.
	ErrUnknownThread = errors.New("netsec: unknown thread")
	// ErrThreadExists marks a duplicate thread registration.
	ErrThreadExists = errors.New("netsec: thread already registered")
)
