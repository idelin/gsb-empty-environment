package netsec

import "errors"

// Sentinel errors returned by the page-ownership package.
var (
	// ErrPoolExhausted is returned by non-blocking allocations when every
	// physical page is live, pending zeroing or quarantined.
	ErrPoolExhausted = errors.New("netsec: page pool exhausted")

	// ErrClosed is returned when the host or pool has been closed.
	ErrClosed = errors.New("netsec: host or pool closed")

	// ErrRevoked is returned when a snapshot has been revoked and is used
	// after the fact.
	ErrRevoked = errors.New("netsec: snapshot revoked")

	// ErrNoPage is returned when a snapshot does not hold the requested key.
	ErrNoPage = errors.New("netsec: snapshot has no page for key")

	// ErrPageExists is returned when granting a key already held by a snapshot.
	ErrPageExists = errors.New("netsec: snapshot already holds a page for key")

	// ErrZeroFailed is returned by a Zeroer to signal that a page could not
	// be wiped. The pool quarantines such a page instead of reallocating it.
	ErrZeroFailed = errors.New("netsec: page zeroing failed")

	// ErrInvalidConfig is returned for invalid constructor arguments.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")
)
