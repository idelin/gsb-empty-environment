package netsec

import (
	"errors"
	"fmt"
	"time"
)

// ResultPassed is the only verification result accepted for admission.
const ResultPassed = "PASSED"

// DefaultMaxAge is the maximum allowed age of a VSA, counted from
// TimeVerified to the moment of the admission decision.
const DefaultMaxAge = 24 * time.Hour

// clockSkew bounds how far a VSA timestamp may lie in the future.
const clockSkew = 5 * time.Minute

var (
	ErrUntrustedVerifier = errors.New("vsa: verifier.id is not in the trusted set")
	ErrSubjectMismatch   = errors.New("vsa: subject digest does not match the request")
	ErrResourceMismatch  = errors.New("vsa: resourceUri does not match the request")
	ErrNotPassed         = errors.New("vsa: verificationResult is not PASSED")
	ErrLevelMissing      = errors.New("vsa: verifiedLevels does not contain the required level")
	ErrPolicyMismatch    = errors.New("vsa: policy.digest does not match the current policy")
	ErrStale             = errors.New("vsa: timeVerified is outside the allowed window")
)

// DigestSet maps digest algorithm names (e.g. "sha256") to hex digests,
// mirroring in-toto's DigestSet.
type DigestSet map[string]string

// Subject is a single in-toto subject entry.
type Subject struct {
	Name   string    `json:"name"`
	Digest DigestSet `json:"digest"`
}

// Verifier identifies the system that produced the VSA.
type Verifier struct {
	ID string `json:"id"`
}

// PolicyRef points at the policy the verification ran against.
type PolicyRef struct {
	URI    string    `json:"uri"`
	Digest DigestSet `json:"digest"`
}

// VSA models the decision-relevant fields of a SLSA Verification Summary
// Attestation predicate together with the statement subject.
type VSA struct {
	Verifier           Verifier  `json:"verifier"`
	TimeVerified       time.Time `json:"timeVerified"`
	ResourceURI        string    `json:"resourceUri"`
	Policy             PolicyRef `json:"policy"`
	VerificationResult string    `json:"verificationResult"`
	VerifiedLevels     []string  `json:"verifiedLevels"`
	Subject            []Subject `json:"subject"`
}

// Request describes the artifact asking for admission.
type Request struct {
	ResourceURI   string
	Digest        DigestSet
	RequiredLevel string
}

// Rules pins every admission-time expectation that must not come from the
// attestation itself.
type Rules struct {
	// TrustedVerifierIDs is the allowlist of acceptable verifier identities.
	TrustedVerifierIDs []string
	// PolicyDigest is the digest of the policy currently in force.
	PolicyDigest DigestSet
	// MaxAge bounds VSA freshness; zero uses DefaultMaxAge (24h).
	MaxAge time.Duration
	// now supplies the current time; nil uses time.Now. Exposed for tests.
	now func() time.Time
}

// Judge decides whether a VSA justifies admitting an artifact.
type Judge struct {
	trusted map[string]struct{}
	rules   Rules
}

// NewJudge builds a Judge from the given rules.
func NewJudge(r Rules) (*Judge, error) {
	if len(r.TrustedVerifierIDs) == 0 {
		return nil, errors.New("vsa: rules must list at least one trusted verifier")
	}
	if len(r.PolicyDigest) == 0 {
		return nil, errors.New("vsa: rules must pin the current policy digest")
	}
	if r.MaxAge == 0 {
		r.MaxAge = DefaultMaxAge
	}
	if r.MaxAge < 0 {
		return nil, errors.New("vsa: MaxAge must not be negative")
	}
	if r.now == nil {
		r.now = time.Now
	}
	trusted := make(map[string]struct{}, len(r.TrustedVerifierIDs))
	for _, id := range r.TrustedVerifierIDs {
		trusted[id] = struct{}{}
	}
	return &Judge{trusted: trusted, rules: r}, nil
}

// Admit reports whether the VSA authorizes admission of the requested
// artifact. A nil error means admit; otherwise the error wraps one of the
// package's sentinel errors identifying the failed check.
func (j *Judge) Admit(v *VSA, req Request) error {
	if v == nil {
		return errors.New("vsa: nil attestation")
	}
	if _, ok := j.trusted[v.Verifier.ID]; !ok {
		return fmt.Errorf("%w: %q", ErrUntrustedVerifier, v.Verifier.ID)
	}
	if !subjectMatches(v.Subject, req.Digest) {
		return fmt.Errorf("%w: want %v", ErrSubjectMismatch, req.Digest)
	}
	if v.ResourceURI != req.ResourceURI {
		return fmt.Errorf("%w: got %q, want %q", ErrResourceMismatch, v.ResourceURI, req.ResourceURI)
	}
	if v.VerificationResult != ResultPassed {
		return fmt.Errorf("%w: got %q", ErrNotPassed, v.VerificationResult)
	}
	if !containsLevel(v.VerifiedLevels, req.RequiredLevel) {
		return fmt.Errorf("%w: %q not in %v", ErrLevelMissing, req.RequiredLevel, v.VerifiedLevels)
	}
	if !digestCovers(v.Policy.Digest, j.rules.PolicyDigest) {
		return fmt.Errorf("%w: got %v, want %v", ErrPolicyMismatch, v.Policy.Digest, j.rules.PolicyDigest)
	}
	now := j.rules.now()
	age := now.Sub(v.TimeVerified)
	if age < -clockSkew || age > j.rules.MaxAge {
		return fmt.Errorf("%w: timeVerified %s is %s from now (max %s)",
			ErrStale, v.TimeVerified.UTC().Format(time.RFC3339), age, j.rules.MaxAge)
	}
	return nil
}

// subjectMatches requires every digest pinned by the request to appear, with
// an equal value, in at least one subject entry. A request without digests
// never matches: admission must bind to concrete content.
func subjectMatches(subjects []Subject, want DigestSet) bool {
	if len(want) == 0 {
		return false
	}
	for _, s := range subjects {
		if digestCovers(s.Digest, want) {
			return true
		}
	}
	return false
}

// digestCovers reports whether got contains every algorithm/value pair in
// want. The attestation may carry extra algorithms; it may not contradict
// or omit a pinned one.
func digestCovers(got, want DigestSet) bool {
	if len(want) == 0 {
		return false
	}
	for alg, val := range want {
		if got[alg] != val {
			return false
		}
	}
	return true
}

func containsLevel(levels []string, want string) bool {
	for _, l := range levels {
		if l == want {
			return true
		}
	}
	return false
}
