package quicguard

import "errors"

// Sentinel errors returned by the state machine. Errors are wrapped with
// context; callers should use errors.Is.
var (
	// ErrMalformed is returned for truncated or otherwise unparseable packets.
	ErrMalformed = errors.New("quicguard: malformed QUIC packet")
	// ErrFixedBit is returned when the RFC 9000 fixed bit is not set.
	ErrFixedBit = errors.New("quicguard: fixed bit not set")
	// ErrUnsupportedVersion is returned for a version the guard cannot parse.
	ErrUnsupportedVersion = errors.New("quicguard: unsupported QUIC version")
	// ErrConnectionIDTooLong is returned for a CID longer than 20 bytes.
	ErrConnectionIDTooLong = errors.New("quicguard: connection ID longer than 20 bytes")

	// ErrInitialTooSmall is returned when an Initial datagram is shorter than
	// the 1200-byte minimum (RFC 9000 section 14.1).
	ErrInitialTooSmall = errors.New("quicguard: Initial datagram shorter than 1200 bytes")
	// ErrAmplificationLimited is returned when an egress packet cannot be sent
	// because txBytes would exceed 3*rxBytes on an unvalidated path.
	ErrAmplificationLimited = errors.New("quicguard: 3x anti-amplification limit reached, packet suspended")
	// ErrQueueFull is returned when the suspension queue is already full.
	ErrQueueFull = errors.New("quicguard: anti-amplification suspension queue full")

	// ErrTokenAEAD is returned when a Retry token fails AES-256-GCM decryption.
	ErrTokenAEAD = errors.New("quicguard: Retry token authentication failed")
	// ErrTokenWrongIP is returned when the token is presented from another IP.
	ErrTokenWrongIP = errors.New("quicguard: Retry token bound to a different client IP")
	// ErrTokenExpired is returned when the token is past its TTL or far in the future.
	ErrTokenExpired = errors.New("quicguard: Retry token expired or not yet valid")
	// ErrTokenReplay is returned when a token nonce has already been consumed.
	ErrTokenReplay = errors.New("quicguard: Retry token replay detected")
	// ErrTokenBinding is returned when a bound CID does not match.
	ErrTokenBinding = errors.New("quicguard: Retry token CID binding mismatch")
	// ErrRetryIntegrity is returned when a Retry integrity tag does not verify.
	ErrRetryIntegrity = errors.New("quicguard: Retry integrity tag mismatch")

	// ErrNoPendingChallenge is returned when a response arrives without a challenge.
	ErrNoPendingChallenge = errors.New("quicguard: no pending PATH_CHALLENGE on path")
	// ErrPathChallengeMismatch is returned for a wrong PATH_RESPONSE value.
	ErrPathChallengeMismatch = errors.New("quicguard: PATH_RESPONSE does not match PATH_CHALLENGE")
	// ErrPathFrame is returned for a malformed PATH_CHALLENGE/PATH_RESPONSE frame.
	ErrPathFrame = errors.New("quicguard: malformed path validation frame")

	// ErrResetTriggerTooSmall is returned when the triggering packet is at most
	// 21 bytes long, so no compliant (>=21 bytes, strictly smaller) reset exists.
	ErrResetTriggerTooSmall = errors.New("quicguard: triggering packet too small for a stateless reset")
	// ErrResetLoop is returned when a stateless reset would be sent in response
	// to another stateless reset, which would feed an infinite reset loop.
	ErrResetLoop = errors.New("quicguard: refusing to reset a stateless reset")
)
