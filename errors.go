package netsec

import "errors"

var (
	// ErrInvalidGrant is returned for every terminal token endpoint rejection:
	// unknown or consumed codes, client mismatch, denial, expiry, subject
	// mismatch, and scope escalation. Callers must not distinguish them.
	ErrInvalidGrant = errors.New("invalid_grant")

	// ErrAccessDenied is an alias that deliberately compares as ErrInvalidGrant.
	ErrAccessDenied = ErrInvalidGrant

	// ErrInvalidDeviceCode is an alias that deliberately compares as
	// ErrInvalidGrant.
	ErrInvalidDeviceCode = ErrInvalidGrant

	// ErrExpiredDeviceCode is an alias that deliberately compares as
	// ErrInvalidGrant.
	ErrExpiredDeviceCode = ErrInvalidGrant

	// ErrClientMismatch is an alias that deliberately compares as
	// ErrInvalidGrant.
	ErrClientMismatch = ErrInvalidGrant

	// ErrAuthorizationPending means the user has not decided yet. Clients
	// should keep polling no faster than the advertised interval.
	ErrAuthorizationPending = errors.New("authorization_pending")

	// ErrSlowDown means the client polled before the advertised interval.
	// The code remains pending or approved and has not been consumed.
	ErrSlowDown = errors.New("slow_down")

	// ErrAlreadyDecided is returned by approval operations after a subject
	// has already approved the authorization.
	ErrAlreadyDecided = errors.New("authorization_already_decided")

	// ErrInvalidSubject is returned when an approval or denial has no subject.
	ErrInvalidSubject = errors.New("invalid_subject")

	// ErrInvalidClient is returned when authorization creation has no client.
	ErrInvalidClient = errors.New("invalid_client")

	// ErrScopeNotRequested is returned when approval attempts to grant a scope
	// that the client did not request.
	ErrScopeNotRequested = errors.New("scope_not_requested")
)
