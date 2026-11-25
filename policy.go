package workloadid

import (
	"crypto/x509"
	"errors"
	"strings"
)

// Policy errors.
var (
	ErrPolicyDenied      = errors.New("workloadid: identity does not satisfy policy")
	ErrInvalidPolicy     = errors.New("workloadid: invalid policy configuration")
	ErrPathDepthExceeded = errors.New("workloadid: SPIFFE path depth exceeds policy limit")
	ErrEKUNotPermitted   = errors.New("workloadid: certificate extended key usage not permitted")
	ErrAttestationDenied = errors.New("workloadid: node attestation does not satisfy policy")
	ErrSANTypeMismatch   = errors.New("workloadid: SAN type cannot substitute for the required name space")
)

// DNSRule is one explicitly declared DNS SAN allow rule. Wildcard semantics are
// enforced by DNSMatcher.
type DNSRule struct {
	// AllowedPatterns lists exact names or left-most-label wildcard patterns.
	AllowedPatterns []string
	// MaxLabels bounds the depth of accepted names.
	MaxLabels int
}

// Policy is evaluated independently for the calling workload and the target
// workload on every connection (including session resumption).
type Policy struct {
	// Name is a human-readable identifier for logs.
	Name string
	// TrustDomain is the only trust domain the policy applies to. A workload
	// from any other trust domain is denied outright.
	TrustDomain string
	// AllowedPathPrefixes lists SPIFFE path-segment prefixes that are accepted.
	// An entry of nil (or a single empty-slice entry) represents the root path.
	AllowedPathPrefixes [][]string
	// MaxPathSegments bounds the SPIFFE path depth. Zero means no explicit
	// bound beyond the global default.
	MaxPathSegments int
	// AllowedEKUs lists the acceptable extended key usages. At least one must be
	// present in the leaf; subject purpose is therefore never implicitly broad.
	AllowedEKUs []x509.ExtKeyUsage
	// AllowedDNS optionally constrains the DNS SAN set. Every DNS SAN of the
	// leaf must be covered; the rule never grants identity by itself.
	AllowedDNS *DNSRule
	// AllowedIPs optionally lists the exact IP SAN values that may appear. If
	// non-nil, every IP SAN must be in the list.
	AllowedIPs []string
	// AllowedURIs optionally lists exact URI SAN values (in addition to the
	// SPIFFE ID) that may appear. A DNS or IP SAN can never satisfy this.
	AllowedURIs []string
	// RequireAttestation mandates a node-attestation claim and constrains it.
	RequireAttestation *NodeAttestation
}

// Validate checks the policy for internally inconsistent configuration.
func (p *Policy) Validate() error {
	if strings.TrimSpace(p.TrustDomain) == "" {
		return ErrInvalidPolicy
	}
	if len(p.AllowedPathPrefixes) == 0 {
		return ErrInvalidPolicy
	}
	for _, prefix := range p.AllowedPathPrefixes {
		for _, segment := range prefix {
			if segment == "" || segment == "." || segment == ".." {
				return ErrInvalidPolicy
			}
		}
	}
	if p.AllowedDNS != nil {
		for _, pattern := range p.AllowedDNS.AllowedPatterns {
			if err := ValidatePattern(pattern); err != nil {
				return err
			}
		}
	}
	for _, ip := range p.AllowedIPs {
		if !validIPString(ip) {
			return ErrInvalidPolicy
		}
	}
	if p.RequireAttestation != nil {
		if err := p.RequireAttestation.validate(); err != nil {
			return err
		}
	}
	return nil
}

func validIPString(value string) bool {
	for _, san := range []SAN{{Type: SANIP, Value: strings.TrimSpace(value)}} {
		if san.Value == "" {
			return false
		}
	}
	return parseIPStrict(value) != nil
}

// Matches evaluates the policy against an authenticated identity and an
// attestation claim. attestation may be nil when the policy does not require
// node attestation.
func (p *Policy) Matches(id *Identity, attestation *NodeAttestationClaim) error {
	if id.TrustDomain != strings.ToLower(strings.TrimSpace(p.TrustDomain)) {
		// Cross trust-domain access is never granted, regardless of DNS overlap.
		return ErrPolicyDenied
	}

	if p.MaxPathSegments > 0 && len(id.PathSegments) > p.MaxPathSegments {
		return ErrPathDepthExceeded
	}
	if !pathPrefixAllowed(id.PathSegments, p.AllowedPathPrefixes) {
		return ErrPolicyDenied
	}

	if err := p.matchEKUs(id.Leaf); err != nil {
		return err
	}
	if err := p.matchSANs(id); err != nil {
		return err
	}
	if p.RequireAttestation != nil {
		if attestation == nil {
			return ErrAttestationDenied
		}
		if err := p.RequireAttestation.matches(attestation); err != nil {
			return err
		}
	}
	return nil
}

func pathPrefixAllowed(segments []string, prefixes [][]string) bool {
	for _, prefix := range prefixes {
		if len(prefix) > len(segments) {
			continue
		}
		ok := true
		for i := range prefix {
			if segments[i] != prefix[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (p *Policy) matchEKUs(leaf *x509.Certificate) error {
	if len(p.AllowedEKUs) == 0 {
		return nil
	}
	for _, required := range p.AllowedEKUs {
		for _, present := range leaf.ExtKeyUsage {
			if present == required {
				return nil
			}
		}
	}
	return ErrEKUNotPermitted
}

func (p *Policy) matchSANs(id *Identity) error {
	if p.AllowedDNS != nil {
		matcher := DNSMatcher{
			AllowedPatterns: p.AllowedDNS.AllowedPatterns,
			MaxLabels:       p.AllowedDNS.MaxLabels,
		}
		for _, name := range id.DNSNames() {
			ok, err := matcher.Match(name)
			if err != nil {
				return err
			}
			if !ok {
				return ErrPolicyDenied
			}
		}
	}

	if p.AllowedIPs != nil {
		allowed := make(map[string]struct{}, len(p.AllowedIPs))
		for _, ip := range p.AllowedIPs {
			allowed[parseIPStrict(ip).String()] = struct{}{}
		}
		for _, ip := range id.IPSANs() {
			if _, ok := allowed[ip]; !ok {
				// An IP SAN cannot be substituted by a DNS/URI entry.
				return ErrSANTypeMismatch
			}
		}
	}

	if p.AllowedURIs != nil {
		allowed := make(map[string]struct{}, len(p.AllowedURIs))
		for _, uri := range p.AllowedURIs {
			allowed[strings.ToLower(strings.TrimSpace(uri))] = struct{}{}
		}
		for _, uri := range id.URISANs() {
			if uri == id.SPIFFEID {
				continue
			}
			if _, ok := allowed[uri]; !ok {
				return ErrSANTypeMismatch
			}
		}
	}
	return nil
}
