package sctpauth

import "errors"

// Security and protocol errors returned by the state machine.
var (
	// ErrForbiddenTag is returned when an Initiate Tag or Verification Tag
	// carries the value 0, which RFC 9260 forbids.
	ErrForbiddenTag = errors.New("sctpauth: initiate/verification tag 0 is forbidden")

	// ErrMalformedCookie is returned for a truncated or structurally invalid
	// cookie before any authentication is attempted.
	ErrMalformedCookie = errors.New("sctpauth: malformed state cookie")

	// ErrCookieSignature is returned when no rotating key authenticates a
	// cookie's HMAC, i.e. the cookie was forged or tampered with.
	ErrCookieSignature = errors.New("sctpauth: cookie HMAC authentication failed")

	// ErrCookieExpired is returned when a cookie is presented after its
	// lifespan has elapsed.
	ErrCookieExpired = errors.New("sctpauth: state cookie expired")

	// ErrCookieReplay is returned when a single-use cookie nonce is presented
	// a second time, or a cookie is replayed from an unexpected source.
	ErrCookieReplay = errors.New("sctpauth: state cookie replay rejected")

	// ErrSourceAddress is returned when a COOKIE-ECHO arrives from an address
	// that is not bound inside the cookie.
	ErrSourceAddress = errors.New("sctpauth: cookie source address mismatch")

	// ErrVerificationTag is returned when a chunk's Verification Tag does not
	// exactly match the association tag.
	ErrVerificationTag = errors.New("sctpauth: verification tag mismatch")

	// ErrTSNOutsideWindow is returned when a DATA chunk TSN is neither the
	// next expected TSN nor within the configurable receive window.
	ErrTSNOutsideWindow = errors.New("sctpauth: TSN outside sliding receive window")

	// ErrDuplicateTSN is returned when a DATA chunk TSN has already been
	// accepted (a replayed chunk).
	ErrDuplicateTSN = errors.New("sctpauth: duplicate TSN rejected")

	// ErrPathUnconfirmed is returned when business data would be sent to a
	// path that has not passed HEARTBEAT path validation.
	ErrPathUnconfirmed = errors.New("sctpauth: destination path is UNCONFIRMED")

	// ErrUnknownPath is returned when an operation references an address that
	// does not belong to the association.
	ErrUnknownPath = errors.New("sctpauth: unknown peer path")

	// ErrAssociationExists is returned when a verified COOKIE-ECHO would
	// establish an association whose tag is already in use.
	ErrAssociationExists = errors.New("sctpauth: association already exists")

	// ErrUnknownAssociation is returned when a chunk references an association
	// that the server is not tracking.
	ErrUnknownAssociation = errors.New("sctpauth: unknown association")

	// ErrInvalidChunk is returned for malformed or unsupported chunks.
	ErrInvalidChunk = errors.New("sctpauth: invalid chunk")
)
