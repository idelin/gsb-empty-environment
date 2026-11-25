// Package evidence re-exports the root evidence package for callers that use
// the module's /evidence import path.  The root package remains the canonical
// implementation.
package evidence

import core "prescreen.local/ps-c7499fbc67"

type (
	Acceptance         = core.Acceptance
	Aggregator         = core.Aggregator
	Attribution        = core.Attribution
	Bundle             = core.Bundle
	Challenge          = core.Challenge
	Decision           = core.Decision
	Digest             = core.Digest
	Evidence           = core.Evidence
	Fork               = core.Fork
	ForkKind           = core.ForkKind
	IdentityList       = core.IdentityList
	IdentitySupplement = core.IdentitySupplement
	IdentityVerifier   = core.IdentityVerifier
	Interval           = core.Interval
	Order              = core.Order
	Problem            = core.Problem
	ProofVerifier      = core.ProofVerifier
	Rejection          = core.Rejection
	Request            = core.Request
	Result             = core.Result
	Revocation         = core.Revocation
	RevocationKind     = core.RevocationKind
	SelectionMode      = core.SelectionMode
	SignatureVerifier  = core.SignatureVerifier
	SupplementVerifier = core.SupplementVerifier
	TimeSpan           = core.TimeSpan
	ValidationResult   = core.ValidationResult
	Witness            = core.Witness
)

const (
	ChallengeHTTP01    = core.ChallengeHTTP01
	ChallengeDNS01     = core.ChallengeDNS01
	ChallengeTLSALPN01 = core.ChallengeTLSALPN01

	ForkDifferentProofDigest = core.ForkDifferentProofDigest
	ForkInconsistentReceipt  = core.ForkInconsistentReceipt

	RevokeProbe       = core.RevokeProbe
	RevokeRouteOrigin = core.RevokeRouteOrigin
	RevokeRouteProof  = core.RevokeRouteProof
	RevokeAttribution = core.RevokeAttribution

	SelectFewest = core.SelectFewest
	SelectMost   = core.SelectMost
)

var (
	ErrInvalidIP           = core.ErrInvalidIP
	ErrReservedIP          = core.ErrReservedIP
	ErrInvalidOrder        = core.ErrInvalidOrder
	ErrInvalidWitness      = core.ErrInvalidWitness
	ErrOrderMismatch       = core.ErrOrderMismatch
	ErrAddressMismatch     = core.ErrAddressMismatch
	ErrMissingIdentity     = core.ErrMissingIdentity
	ErrAmbiguousIdentity   = core.ErrAmbiguousIdentity
	ErrIdentityConflict    = core.ErrIdentityConflict
	ErrRevoked             = core.ErrRevoked
	ErrNoCommonInstant     = core.ErrNoCommonInstant
	ErrInsufficientDomains = core.ErrInsufficientDomains
	ErrRouteOriginConflict = core.ErrRouteOriginConflict
	ErrForkedReceipt       = core.ErrForkedReceipt
	ErrNoValidEvidence     = core.ErrNoValidEvidence
	ErrInvalidBundle       = core.ErrInvalidBundle
	ErrSearchLimit         = core.ErrSearchLimit
	ErrInvalidDigest       = core.ErrInvalidDigest
	ErrInvalidTimeSpan     = core.ErrInvalidTimeSpan
)

var (
	Accept                  = core.Accept
	Aggregate               = core.Aggregate
	Audit                   = core.Audit
	CommonInstant           = core.CommonInstant
	DigestBytes             = core.DigestBytes
	DigestOf                = core.DigestOf
	IntersectAll            = core.IntersectAll
	IsPublicIP              = core.IsPublicIP
	MustNewTimeSpan         = core.MustNewTimeSpan
	MustParseDigest         = core.MustParseDigest
	MustSpanOf              = core.MustSpanOf
	NewOrder                = core.NewOrder
	NewTimeSpan             = core.NewTimeSpan
	NormalizeDomain         = core.NormalizeDomain
	NormalizeOrder          = core.NormalizeOrder
	NormalizePublicAddr     = core.NormalizePublicAddr
	NormalizePublicAddrPort = core.NormalizePublicAddrPort
	NormalizeRouteOrigin    = core.NormalizeRouteOrigin
	ParseDigest             = core.ParseDigest
	ParsePublicAddrPort     = core.ParsePublicAddrPort
	ParsePublicIP           = core.ParsePublicIP
	Select                  = core.Select
	SpanOf                  = core.SpanOf
	UnionAll                = core.UnionAll
	ValidatePublicIP        = core.ValidatePublicIP
	ValidateOrder           = core.ValidateOrder
	ValidateWitness         = core.ValidateWitness
	Verify                  = core.Verify
)

// New returns a stateless aggregator.
func New() *Aggregator { return core.New() }
