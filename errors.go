package netsec

import "errors"

// Sentinel errors returned by the DH-CHAP negotiation and validation logic.
// Callers should use errors.Is to match them and map them onto AUTH_ELS
// reject status codes; none of them trigger a fallback to plain CHAP.
var (
	// ErrInvalidPolicy is returned when a Policy is empty, unsorted, contains
	// unknown identifiers, or contradicts its AllowNullDH setting.
	ErrInvalidPolicy = errors.New("dhchap: invalid negotiation policy")

	// ErrNoCommonHash is returned when no configured hash is offered by the
	// peer.
	ErrNoCommonHash = errors.New("dhchap: no mutually acceptable hash algorithm")

	// ErrNoCommonDHGroup is returned when no configured DH group is offered
	// by the peer and null DH is either not offered or not permitted.
	ErrNoCommonDHGroup = errors.New("dhchap: no mutually acceptable DH group")

	// ErrNullDHNotAllowed is returned when the peer only offers the null DH
	// group while the local policy does not explicitly permit it.
	ErrNullDHNotAllowed = errors.New("dhchap: null DH group is not permitted by policy")

	// ErrInvalidDHPublicValue is returned when a peer DH public value is not
	// in the range [2, p-2]. This rejects the small-subgroup values 0, 1 and
	// p-1 as well as any value outside the modulus.
	ErrInvalidDHPublicValue = errors.New("dhchap: peer DH public value outside [2, p-2]")

	// ErrChallengeReused is returned when the same challenge bytes are
	// presented twice within one transaction.
	ErrChallengeReused = errors.New("dhchap: challenge reused within transaction")

	// ErrChallengeTooShort is returned for a challenge shorter than the
	// 128-bit (16 byte) minimum required by FC-SP.
	ErrChallengeTooShort = errors.New("dhchap: challenge shorter than 16 bytes")

	// ErrInvalidMessage is returned when an AUTH_ELS message is malformed,
	// carries an unexpected status, or violates the exchange state machine.
	ErrInvalidMessage = errors.New("dhchap: malformed or unexpected AUTH_ELS message")

	// ErrTransactionFailed is returned when a transaction has already failed
	// and must be restarted rather than continued.
	ErrTransactionFailed = errors.New("dhchap: transaction has failed and must be restarted")

	// ErrBadCredentials is returned when a peer CHAP response does not match
	// the locally computed value.
	ErrBadCredentials = errors.New("dhchap: CHAP response verification failed")
)
