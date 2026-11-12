package netsec

import "errors"

// Protocol level sentinel errors. Callers should use errors.Is to branch on
// these because wrapped errors may carry backend-specific detail.
var (
	// ErrExists is returned when a secret id is already registered, including
	// ids whose records have reached a terminal state. Terminal ids are never
	// reused.
	ErrExists = errors.New("netsec: secret already exists")

	// ErrNotFound is returned when no secret is registered under the id.
	ErrNotFound = errors.New("netsec: secret not found")

	// ErrLeaseHeld is returned when Claim is called while an unexpired lease
	// exists. The holder must use Retransmit; nobody else may take over.
	ErrLeaseHeld = errors.New("netsec: secret is currently leased")

	// ErrLeaseMismatch is returned when a Retransmit/Acknowledge call does not
	// prove possession of the active lease (wrong session id or nonce).
	ErrLeaseMismatch = errors.New("netsec: lease session or nonce does not match")

	// ErrNoLease is returned when a lease-only operation hits a secret that has
	// never been claimed.
	ErrNoLease = errors.New("netsec: no active lease")

	// ErrInvalidProof is returned when the client confirmation proof cannot be
	// verified. The lease is kept intact so the client can retry.
	ErrInvalidProof = errors.New("netsec: invalid delivery confirmation proof")

	// ErrAlreadyDelivered is returned when an operation touches a secret whose
	// first successful delivery has already destroyed the payload.
	ErrAlreadyDelivered = errors.New("netsec: secret already delivered and destroyed")

	// ErrOutcomeUnknown is returned for every protocol operation on a secret in
	// the terminal unknown state. The secret must never be reissued to anyone.
	ErrOutcomeUnknown = errors.New("netsec: delivery outcome is unknown; secret cannot be reissued")

	// ErrDestroyFailed wraps a backend deletion failure after a valid client
	// confirmation. The record is forced into the unknown terminal state.
	ErrDestroyFailed = errors.New("netsec: payload destruction failed")
)
