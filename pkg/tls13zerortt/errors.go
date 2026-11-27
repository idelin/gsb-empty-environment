package tls13zerortt

import "errors"

// Wire/parse errors.
var (
	// ErrMalformedClientHello indicates the input is not a length-consistent
	// TLS 1.3 ClientHello handshake message (or record fragment).
	ErrMalformedClientHello = errors.New("tls13zerortt: malformed ClientHello")
	// ErrNoPreSharedKey indicates the ClientHello carries no pre_shared_key
	// extension, so no binder can be evaluated.
	ErrNoPreSharedKey = errors.New("tls13zerortt: missing pre_shared_key extension")
	// ErrPreSharedKeyNotLast indicates the pre_shared_key extension is not
	// the final extension, which RFC 8446 Section 4.2.11 forbids.
	ErrPreSharedKeyNotLast = errors.New("tls13zerortt: pre_shared_key must be the last extension")
	// ErrBinderIndex indicates the selected PSK identity has no matching
	// binder entry (identity/binder count mismatch).
	ErrBinderIndex = errors.New("tls13zerortt: binder index out of range")
	// ErrBinderLength indicates a binder is not HashLength (32) bytes; only
	// SHA-256 cipher suites are supported by this package.
	ErrBinderLength = errors.New("tls13zerortt: unsupported binder length")
	// ErrBinderMismatch is returned when the recomputed binder does not match
	// the wire value in constant time.
	ErrBinderMismatch = errors.New("tls13zerortt: PSK binder verification failed")
	// ErrUnknownPSKMode is returned for a PSKMode outside the defined set.
	ErrUnknownPSKMode = errUnknownPSKMode

	// ErrInvalidConfig indicates an invalid replay filter / validator option.
	ErrInvalidConfig = errors.New("tls13zerortt: invalid configuration")
)

var (
	errInvalidWindow   = ErrInvalidConfig
	errInvalidCapacity = ErrInvalidConfig
)

// Ticket / policy errors.
var (
	// ErrTicketExpired indicates the ticket's ticket_lifetime has elapsed.
	ErrTicketExpired = errors.New("tls13zerortt: ticket expired")
	// ErrTicketAgeSkew indicates the recovered client ticket age differs
	// from the server-measured age beyond the configured skew window.
	ErrTicketAgeSkew = errors.New("tls13zerortt: ticket age outside skew window")
	// ErrCipherSuiteMismatch indicates the offered cipher suite does not
	// match the one bound to the ticket.
	ErrCipherSuiteMismatch = errors.New("tls13zerortt: cipher suite mismatch")
	// ErrALPNMismatch indicates the offered ALPN protocol list does not
	// match the protocol bound to the ticket.
	ErrALPNMismatch = errors.New("tls13zerortt: ALPN mismatch")
	// ErrEarlyDataNotNegotiated indicates the ticket was issued without the
	// early_data extension (max_early_data_size == 0).
	ErrEarlyDataNotNegotiated = errors.New("tls13zerortt: ticket does not allow early_data")
	// ErrEarlyDataOverflow indicates the per-ticket cumulative early-data
	// byte counter would exceed max_early_data_size.
	ErrEarlyDataOverflow = errors.New("tls13zerortt: early_data byte budget exhausted")
	// ErrNonIdempotent indicates the request policy forbids 0-RTT for this
	// (non-idempotent) request path.
	ErrNonIdempotent = errors.New("tls13zerortt: 0-RTT rejected for non-idempotent request")
	// ErrReplayed indicates a second early_data attempt for a single-use
	// ticket identity.
	ErrReplayed = errors.New("tls13zerortt: early_data replay detected")
	// ErrRegistryUnavailable indicates the bounded registry can no longer
	// guarantee single-use semantics (state saturated); fail closed.
	ErrRegistryUnavailable = errors.New("tls13zerortt: replay registry state saturated")
)
