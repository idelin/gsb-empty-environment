package netsec

import "errors"

var (
	// Configuration errors.
	ErrEmptyTeamID       = errors.New("netsec: team ID must not be empty")
	ErrEmptyBundleID     = errors.New("netsec: bundle ID must not be empty")
	ErrNilVerifier       = errors.New("netsec: verifier must not be nil")
	ErrNilStore          = errors.New("netsec: store must not be nil")
	ErrNilCounterStore   = errors.New("netsec: counter store must not be nil")
	ErrNilChallengeStore = errors.New("netsec: challenge store must not be nil")

	// Request and assertion errors.
	ErrEmptyKeyID               = errors.New("netsec: key ID must not be empty")
	ErrEmptyChallenge           = errors.New("netsec: challenge must not be empty")
	ErrInvalidPublicKey         = errors.New("netsec: public key must be a valid ECDSA P-256 key")
	ErrInvalidAuthenticatorData = errors.New("netsec: invalid authenticator data")
	ErrRPIDHashMismatch         = errors.New("netsec: rpIdHash does not match team ID and bundle ID")
	ErrInvalidFlags             = errors.New("netsec: assertion flags must be 0x00")
	ErrInvalidClientDataHash    = errors.New("netsec: clientDataHash must be a SHA-256 digest")
	ErrClientDataHashMismatch   = errors.New("netsec: clientDataHash does not bind the challenge and request body")
	ErrInvalidSignature         = errors.New("netsec: assertion signature is invalid")
	ErrCounterNotAdvanced       = errors.New("netsec: assertion counter is not greater than the recorded counter")
	ErrCounterConflict          = errors.New("netsec: counter changed concurrently; assertion rejected")

	// Challenge store errors. A challenge is removed from the valid set as
	// soon as an assertion attempts to consume it, including a failed attempt.
	ErrChallengeNotFound      = errors.New("netsec: challenge not found")
	ErrChallengeExpired       = errors.New("netsec: challenge expired")
	ErrChallengeReused        = errors.New("netsec: challenge already used")
	ErrChallengeDuplicate     = errors.New("netsec: challenge already exists")
	ErrInvalidChallengeExpiry = errors.New("netsec: challenge expiry must be set")

	// Randomness errors.
	ErrChallengeGeneration = errors.New("netsec: failed to generate challenge")
)
