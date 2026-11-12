package netsec

import "errors"

var (
	// ErrInvalidOrder is returned when an order registration is incomplete.
	ErrInvalidOrder = errors.New("netsec: invalid order")
	// ErrOrderExists is returned when a matchingId is registered twice.
	ErrOrderExists = errors.New("netsec: order already exists")
	// ErrOrderNotFound is returned for an unknown matchingId.
	ErrOrderNotFound = errors.New("netsec: order not found")
	// ErrOrderLocked is returned once the confirmation-code failure limit is
	// exceeded.
	ErrOrderLocked = errors.New("netsec: order locked")
	// ErrMatchingIDConsumed is returned when a matchingId has already been
	// used to obtain a profile.
	ErrMatchingIDConsumed = errors.New("netsec: matchingId already consumed")
	// ErrMatchingIDMismatch is returned when a transaction is used with a
	// matchingId belonging to another order.
	ErrMatchingIDMismatch = errors.New("netsec: matchingId does not belong to this transaction")

	// ErrSessionNotFound is returned when a transactionId is unknown.
	ErrSessionNotFound = errors.New("netsec: session not found")
	// ErrSessionExpired is returned after the session TTL has elapsed.
	ErrSessionExpired = errors.New("netsec: session expired")
	// ErrSessionSuperseded is returned when a newer challenge replaced the
	// session's challenge.
	ErrSessionSuperseded = errors.New("netsec: session superseded by a newer challenge")
	// ErrAuthenticationReplay is returned when an authentication response or
	// transactionId is used more than once.
	ErrAuthenticationReplay = errors.New("netsec: authentication response already used")
	// ErrChallengeMismatch is returned when the server challenge in the
	// response is not the challenge issued for this transaction.
	ErrChallengeMismatch = errors.New("netsec: server challenge mismatch")
	// ErrSignatureInvalid is returned when the response signature does not
	// cover the transactionId and server challenge.
	ErrSignatureInvalid = errors.New("netsec: authentication signature invalid")
	// ErrAuthenticationRequired is returned when a profile is requested before
	// the eUICC authentication response has been accepted.
	ErrAuthenticationRequired = errors.New("netsec: authentication required")
	// ErrEIDMismatch is returned when the eUICC EID is not the EID bound to the
	// order.
	ErrEIDMismatch = errors.New("netsec: eUICC EID mismatch")

	// ErrConfirmationCodeInvalid is returned for a wrong confirmation code.
	ErrConfirmationCodeInvalid = errors.New("netsec: confirmation code invalid")
	// ErrNonceGeneration is returned when the injected randomness source
	// cannot produce a transactionId and challenge.
	ErrNonceGeneration = errors.New("netsec: could not generate nonce")
)
