package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidPolicy is returned when a trust policy is structurally invalid.
	ErrInvalidPolicy = errors.New("invalid trust policy")
	// ErrInvalidVerificationLevel is returned for an unknown verification level.
	ErrInvalidVerificationLevel = errors.New("invalid verification level")
	// ErrNoMatchingPolicy is returned when no policy scope matches an image.
	ErrNoMatchingPolicy = errors.New("no trust policy matches image")
	// ErrOverlappingScopes is returned when one image matches more than one
	// concrete (non-global) registry scope.
	ErrOverlappingScopes = errors.New("image matches multiple registry scopes")
)

// VerificationLevel controls how signature verification failures affect
// admission.
type VerificationLevel string

const (
	// VerificationLevelStrict denies expired signatures, revoked signatures
	// and signatures whose revocation status is unknown.
	VerificationLevelStrict VerificationLevel = "strict"
	// VerificationLevelPermissive is identical to strict except that an
	// unknown revocation status is allowed.
	VerificationLevelPermissive VerificationLevel = "permissive"
	// VerificationLevelAudit records findings but does not deny admission.
	VerificationLevelAudit VerificationLevel = "audit"
)

// Short aliases for callers that prefer them.
const (
	Strict     = VerificationLevelStrict
	Permissive = VerificationLevelPermissive
	Audit      = VerificationLevelAudit
)

// SignatureVerification mirrors the Notation trust policy field of the same
// name.
type SignatureVerification struct {
	Level VerificationLevel `json:"level"`
}

// TrustPolicy is a Notation-style trust policy. Only Name, RegistryScopes and
// SignatureVerification.Level affect selection and admission in this package;
// the remaining fields are preserved for callers that also perform trust store
// and identity checks.
type TrustPolicy struct {
	Name                  string                `json:"name,omitempty"`
	RegistryScopes        []string              `json:"registryScopes"`
	SignatureVerification SignatureVerification `json:"signatureVerification"`
	// VerificationLevel is accepted as a shorthand for
	// SignatureVerification.Level when the nested field is empty.
	VerificationLevel VerificationLevel `json:"verificationLevel,omitempty"`
	TrustStores       []string          `json:"trustStores,omitempty"`
	TrustedIdentities []string          `json:"trustedIdentities,omitempty"`
}

// PolicyDocument is the Notation trust policy document wrapper.
type PolicyDocument struct {
	Version       string        `json:"version,omitempty"`
	TrustPolicies []TrustPolicy `json:"trustPolicies"`
}

// PolicyMatch describes the scope and policy selected for an image.
type PolicyMatch struct {
	Policy *TrustPolicy
	Scope  string
	Global bool
}

// ScopeMatch identifies one scope that matched an image.
type ScopeMatch struct {
	PolicyName string `json:"policyName,omitempty"`
	Scope      string `json:"scope"`
}

// ScopeOverlapError reports an ambiguous image-to-policy mapping.
type ScopeOverlapError struct {
	Image   string
	Matches []ScopeMatch
}

func (e *ScopeOverlapError) Error() string {
	if e == nil {
		return ErrOverlappingScopes.Error()
	}
	parts := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		if match.PolicyName == "" {
			parts = append(parts, fmt.Sprintf("%q", match.Scope))
			continue
		}
		parts = append(parts, fmt.Sprintf("%q (policy %q)", match.Scope, match.PolicyName))
	}
	return fmt.Sprintf("%s %q: %s", ErrOverlappingScopes, e.Image, strings.Join(parts, ", "))
}

// Unwrap supports errors.Is(err, ErrOverlappingScopes).
func (e *ScopeOverlapError) Unwrap() error { return ErrOverlappingScopes }

// NoMatchingPolicyError reports that no scope, including the global "*"
// scope, matched the image.
type NoMatchingPolicyError struct {
	Image string
}

func (e *NoMatchingPolicyError) Error() string {
	if e == nil {
		return ErrNoMatchingPolicy.Error()
	}
	return fmt.Sprintf("%s: %q", ErrNoMatchingPolicy, e.Image)
}

// Unwrap supports errors.Is(err, ErrNoMatchingPolicy).
func (e *NoMatchingPolicyError) Unwrap() error { return ErrNoMatchingPolicy }

type compiledPolicy struct {
	policy TrustPolicy
	level  VerificationLevel
	scopes []registryScope
}

type matchCandidate struct {
	policyIndex int
	scope       registryScope
}

// PolicySet is an immutable, concurrency-safe set of compiled trust policies.
type PolicySet struct {
	policies []compiledPolicy
}

// NewPolicySet validates and compiles trust policies. Scope overlap is not
// rejected here because whether two wildcard scopes overlap can depend on the
// image being admitted; Select and SelectMatch report it as a configuration
// error for the affected image.
func NewPolicySet(policies []TrustPolicy) (*PolicySet, error) {
	compiled := make([]compiledPolicy, 0, len(policies))
	for i := range policies {
		policy := cloneTrustPolicy(policies[i])
		label := policy.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i)
		}

		nestedLevel, err := normalizeVerificationLevel(policy.SignatureVerification.Level)
		if err != nil {
			return nil, fmt.Errorf("%w: policy %s: %w", ErrInvalidPolicy, label, err)
		}
		level := nestedLevel
		if policy.VerificationLevel != "" {
			shorthand, err := normalizeVerificationLevel(policy.VerificationLevel)
			if err != nil {
				return nil, fmt.Errorf("%w: policy %s: %w", ErrInvalidPolicy, label, err)
			}
			if policy.SignatureVerification.Level != "" && shorthand != nestedLevel {
				return nil, fmt.Errorf("%w: policy %s has conflicting verification levels", ErrInvalidPolicy, label)
			}
			level = shorthand
		}
		policy.SignatureVerification.Level = level
		if len(policy.RegistryScopes) == 0 {
			return nil, fmt.Errorf("%w: policy %s has no registryScopes", ErrInvalidPolicy, label)
		}

		scopes := make([]registryScope, 0, len(policy.RegistryScopes))
		for _, rawScope := range policy.RegistryScopes {
			scope, err := parseRegistryScope(rawScope)
			if err != nil {
				return nil, fmt.Errorf("%w: policy %s: %w", ErrInvalidPolicy, label, err)
			}
			scopes = append(scopes, scope)
		}

		compiled = append(compiled, compiledPolicy{
			policy: policy,
			level:  level,
			scopes: scopes,
		})
	}
	return &PolicySet{policies: compiled}, nil
}

// ParsePolicySet parses a Notation trust policy document:
//
//	{"version":"1.0","trustPolicies":[...]}
//
// A bare array of policies and a single bare policy object are also accepted
// for convenience.
func ParsePolicySet(data []byte) (*PolicySet, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: empty document", ErrInvalidPolicy)
	}

	if trimmed[0] == '[' {
		var policies []TrustPolicy
		if err := json.Unmarshal(trimmed, &policies); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
		}
		return NewPolicySet(policies)
	}

	var doc PolicyDocument
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	if doc.Version != "" && doc.Version != "1.0" {
		return nil, fmt.Errorf("%w: unsupported version %q", ErrInvalidPolicy, doc.Version)
	}
	if doc.TrustPolicies == nil {
		var single TrustPolicy
		if err := json.Unmarshal(trimmed, &single); err == nil && len(single.RegistryScopes) > 0 {
			return NewPolicySet([]TrustPolicy{single})
		}
		return nil, fmt.Errorf("%w: missing trustPolicies", ErrInvalidPolicy)
	}
	return NewPolicySet(doc.TrustPolicies)
}

// ParseTrustPolicy is an alias for ParsePolicySet.
func ParseTrustPolicy(data []byte) (*PolicySet, error) {
	return ParsePolicySet(data)
}

// Policies returns a copy of the configured policies.
func (s *PolicySet) Policies() []TrustPolicy {
	if s == nil {
		return nil
	}
	out := make([]TrustPolicy, 0, len(s.policies))
	for _, compiled := range s.policies {
		out = append(out, cloneTrustPolicy(compiled.policy))
	}
	return out
}

// Len returns the number of configured policies.
func (s *PolicySet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.policies)
}

// Select chooses the policy for an image reference and returns the policy.
// A concrete (non-global) scope always takes precedence over "*". If more than
// one concrete scope matches, Select returns an error wrapping
// ErrOverlappingScopes.
func (s *PolicySet) Select(image string) (*TrustPolicy, error) {
	match, err := s.SelectMatch(image)
	if err != nil {
		return nil, err
	}
	return match.Policy, nil
}

// SelectPolicy is an alias for Select.
func (s *PolicySet) SelectPolicy(image string) (*TrustPolicy, error) {
	return s.Select(image)
}

// SelectMatch is Select with additional information about the winning scope.
func (s *PolicySet) SelectMatch(image string) (*PolicyMatch, error) {
	ref, err := ParseImageReference(image)
	if err != nil {
		return nil, err
	}
	return s.SelectReference(ref)
}

// SelectReference is SelectMatch for an already parsed reference.
func (s *PolicySet) SelectReference(ref ImageReference) (*PolicyMatch, error) {
	if err := validateParsedReference(ref); err != nil {
		return nil, err
	}
	if s == nil || len(s.policies) == 0 {
		return nil, &NoMatchingPolicyError{Image: ref.Name()}
	}

	var concrete []matchCandidate
	var global []matchCandidate

	for policyIndex := range s.policies {
		for _, scope := range s.policies[policyIndex].scopes {
			if !scope.match(ref) {
				continue
			}
			c := matchCandidate{policyIndex: policyIndex, scope: scope}
			if scope.global {
				global = append(global, c)
			} else {
				concrete = append(concrete, c)
			}
		}
	}

	// An image matching two concrete scopes is ambiguous: admitting it could
	// silently apply the more permissive of the two policies.
	if len(concrete) > 1 {
		return nil, s.overlapError(ref, concrete)
	}
	if len(concrete) == 1 {
		return s.matchFor(concrete[0]), nil
	}
	if len(global) > 1 {
		return nil, s.overlapError(ref, global)
	}
	if len(global) == 1 {
		return s.matchFor(global[0]), nil
	}
	return nil, &NoMatchingPolicyError{Image: ref.Name()}
}

func (s *PolicySet) matchFor(c matchCandidate) *PolicyMatch {
	policy := cloneTrustPolicy(s.policies[c.policyIndex].policy)
	policy.SignatureVerification.Level = s.policies[c.policyIndex].level
	return &PolicyMatch{
		Policy: &policy,
		Scope:  c.scope.raw,
		Global: c.scope.global,
	}
}

func validateParsedReference(ref ImageReference) error {
	if ref.Registry == "" || ref.Repository == "" {
		return fmt.Errorf("%w: registry and repository are required", ErrInvalidReference)
	}
	if _, err := normalizeRegistry(ref.Registry); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReference, err)
	}
	if err := validateRepository(ref.Repository); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReference, err)
	}
	if ref.Tag != "" {
		if err := validateTag(ref.Tag); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidReference, err)
		}
	}
	if ref.Digest != "" {
		if _, err := normalizeDigest(ref.Digest); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidReference, err)
		}
	}
	return nil
}

func (s *PolicySet) overlapError(ref ImageReference, matches []matchCandidate) error {
	err := &ScopeOverlapError{Image: ref.Name()}
	for _, match := range matches {
		err.Matches = append(err.Matches, ScopeMatch{
			PolicyName: s.policies[match.policyIndex].policy.Name,
			Scope:      match.scope.raw,
		})
	}
	return err
}

func normalizeVerificationLevel(level VerificationLevel) (VerificationLevel, error) {
	switch strings.ToLower(strings.TrimSpace(string(level))) {
	case "", "strict":
		return VerificationLevelStrict, nil
	case "permissive":
		return VerificationLevelPermissive, nil
	case "audit":
		return VerificationLevelAudit, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidVerificationLevel, level)
	}
}

func cloneTrustPolicy(in TrustPolicy) TrustPolicy {
	out := in
	out.RegistryScopes = append([]string(nil), in.RegistryScopes...)
	out.TrustStores = append([]string(nil), in.TrustStores...)
	out.TrustedIdentities = append([]string(nil), in.TrustedIdentities...)
	return out
}
