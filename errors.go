package threshold

import "errors"

// Stable sentinel errors allow hosts to classify protocol failures without
// matching localized strings.
var (
	ErrInvalidConfig            = errors.New("threshold: invalid configuration")
	ErrGroupRequired            = errors.New("threshold: group implementation is required")
	ErrInvalidThreshold         = errors.New("threshold: threshold must satisfy 1 <= t <= n")
	ErrUnknownParticipant       = errors.New("threshold: unknown participant")
	ErrDuplicateParticipant     = errors.New("threshold: duplicate participant")
	ErrInvalidParticipantIndex  = errors.New("threshold: participant index must be a nonzero scalar modulo group order")
	ErrBlacklisted              = errors.New("threshold: participant is permanently blacklisted")
	ErrRoundViolation           = errors.New("threshold: protocol round transition violated")
	ErrDuplicateMessage         = errors.New("threshold: duplicate or conflicting protocol message")
	ErrProofOfPossession        = errors.New("threshold: invalid Schnorr proof of possession")
	ErrInvalidCommitment        = errors.New("threshold: invalid Feldman commitment")
	ErrInvalidShare             = errors.New("threshold: share does not match Feldman commitments")
	ErrMissingCommitment        = errors.New("threshold: missing round-one commitment")
	ErrDuplicateShare           = errors.New("threshold: conflicting share received")
	ErrComplaintUnsubstantiated = errors.New("threshold: complaint does not prove a bad share")
	ErrInvalidComplaint         = errors.New("threshold: invalid complaint evidence")
	ErrInsufficientQualified    = errors.New("threshold: fewer than threshold qualified participants")
	ErrFrozen                   = errors.New("threshold: DKG public key is frozen")
	ErrMissingShare             = errors.New("threshold: missing verified share")
	ErrNonceReuse               = errors.New("threshold: one-time signing nonce or commitment reused")
	ErrUnknownNonce             = errors.New("threshold: unknown signing nonce")
	ErrInvalidNonce             = errors.New("threshold: invalid signing nonce commitment")
	ErrDuplicateCommitment      = errors.New("threshold: duplicate signer commitment")
	ErrInvalidSigningPackage    = errors.New("threshold: invalid FROST signing package")
	ErrInvalidPartialSignature  = errors.New("threshold: invalid partial Schnorr signature")
	ErrMissingPartial           = errors.New("threshold: missing partial signature")
	ErrDuplicatePartial         = errors.New("threshold: duplicate partial signature")
	ErrAccountability           = errors.New("threshold: signing accountability failure")
	ErrInvalidSignature         = errors.New("threshold: invalid aggregated Schnorr signature")
)

// ErrNonceReplay is an explicit alias for hosts using replay terminology.
var ErrNonceReplay = ErrNonceReuse
