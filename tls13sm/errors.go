package tls13sm

import "errors"

// Sentinel errors returned by the state machines. All of them are wrapped
// with context via fmt.Errorf("...: %w", ...), so callers should use
// errors.Is to match them.
var (
	// ErrIllegalMessage indicates a record or handshake message that violates
	// the RFC 8446 ordering rules for the current state.
	ErrIllegalMessage = errors.New("tls13sm: illegal message for current state")

	// ErrDowngradeDetected is returned when ServerHello.random carries one of
	// the RFC 8446 4.1.3 downgrade sentinels in its trailing eight bytes.
	ErrDowngradeDetected = errors.New("tls13sm: downgrade sentinel in ServerHello.random")

	// ErrHRRRejected covers every HelloRetryRequest policy failure, including
	// a second HRR and parameter mismatches between the two ClientHello
	// messages.
	ErrHRRRejected = errors.New("tls13sm: HelloRetryRequest rejected")

	// ErrReplayDetected means the strike register already contains the ticket
	// nonce presented by a 0-RTT attempt.
	ErrReplayDetected = errors.New("tls13sm: 0-RTT early data replay detected")

	// ErrTicketAgeSkew means the obfuscated ticket age is outside the
	// configured acceptance window.
	ErrTicketAgeSkew = errors.New("tls13sm: ticket age outside acceptance window")

	// ErrNotIdempotent means the caller attempted to emit or accept early data
	// without an explicit idempotency declaration.
	ErrNotIdempotent = errors.New("tls13sm: early data requires explicit idempotency declaration")

	// ErrEarlyDataRejected is returned when the peer refused early data or the
	// local policy gate did not admit it.
	ErrEarlyDataRejected = errors.New("tls13sm: early data not accepted")

	// ErrKeyUpdateLimit means the configured per-direction KeyUpdate budget
	// has been exhausted, preventing out-of-bound key rotations.
	ErrKeyUpdateLimit = errors.New("tls13sm: KeyUpdate rotation limit exceeded")

	// ErrDecryptFailed is returned when a protected record fails
	// authentication or is presented under the wrong epoch.
	ErrDecryptFailed = errors.New("tls13sm: record decryption or epoch check failed")

	// ErrFinishedVerify means the peer Finished verify_data does not match the
	// HMAC computed over the current transcript.
	ErrFinishedVerify = errors.New("tls13sm: Finished verify_data mismatch")

	// ErrProtocol encodes malformed wire data and general protocol violations
	// not covered by a more specific sentinel.
	ErrProtocol = errors.New("tls13sm: protocol violation")

	// ErrState means the machine was asked to perform an action that is not
	// available in its current phase.
	ErrState = errors.New("tls13sm: operation not allowed in current state")
)
