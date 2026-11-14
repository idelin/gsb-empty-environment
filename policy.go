package netsec

import (
	"fmt"
	"net/netip"
	"strings"
)

// PrincipalUse is the mTLS role for which a certificate is being evaluated.
type PrincipalUse uint8

const (
	UseClient PrincipalUse = 1 << iota
	UseServer
)

func (u PrincipalUse) String() string {
	switch u {
	case UseClient:
		return "client"
	case UseServer:
		return "server"
	default:
		return fmt.Sprintf("principal-use(%d)", uint8(u))
	}
}

// IdentityPolicy describes the exact identity attributes permitted for one
// side of a connection. Paths are exact or explicit directory prefixes; they
// are never wildcards. DNS rules constrain DNS SANs but never define the
// principal. IP rules constrain IP SANs in their own namespace.
//
// A policy with no Paths and no PathPrefixes denies every workload. An empty
// Uses slice means the identity may be used for both client and server mTLS.
type IdentityPolicy struct {
	TrustDomain   string
	Paths         []string
	PathPrefixes  []string
	DNSNames      []DNSNameRule
	IPPrefixes    []netip.Prefix
	NodeSelectors map[string]string
	Uses          []PrincipalUse
}

// Validate checks the policy and rejects wildcard paths, ambiguous prefixes,
// DNS identity substitution, and unknown principal-use bits.
func (p IdentityPolicy) Validate() error {
	_, err := compileIdentityPolicy(p)
	return err
}

type compiledIdentityPolicy struct {
	trustDomain TrustDomain
	paths       []SPIFFEPath
	prefixes    []string
	dnsNames    []DNSNameRule
	ipPrefixes  []netip.Prefix
	selectors   map[string]string
	uses        PrincipalUse
}

func compileIdentityPolicy(p IdentityPolicy) (compiledIdentityPolicy, error) {
	td, err := ParseTrustDomain(p.TrustDomain)
	if err != nil {
		return compiledIdentityPolicy{}, fmt.Errorf("%w: identity policy trust domain: %v", ErrInvalidConfig, err)
	}

	if len(p.Paths) == 0 && len(p.PathPrefixes) == 0 {
		return compiledIdentityPolicy{}, fmt.Errorf("%w: identity policy must declare at least one path or path prefix", ErrInvalidConfig)
	}

	out := compiledIdentityPolicy{trustDomain: td}
	for i, rawPath := range p.Paths {
		path, pathErr := ParseSPIFFEPath(rawPath)
		if pathErr != nil {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: path %d: %w", ErrInvalidConfig, i, pathErr)
		}
		out.paths = append(out.paths, path)
	}
	for i, prefix := range p.PathPrefixes {
		if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") || prefix == "/" {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: path prefix %d must be a non-root directory ending in '/'", ErrInvalidConfig, i)
		}
		if strings.ContainsAny(prefix, "*?#\\ \t\r\n") {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: path prefix %d contains a wildcard or invalid character", ErrInvalidConfig, i)
		}
		trimmed := strings.TrimSuffix(prefix, "/")
		if _, pathErr := ParseSPIFFEPath(trimmed); pathErr != nil {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: path prefix %d: %w", ErrInvalidConfig, i, pathErr)
		}
		out.prefixes = append(out.prefixes, prefix)
	}

	for i, rule := range p.DNSNames {
		if ruleErr := rule.Validate(); ruleErr != nil {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: DNS rule %d: %w", ErrInvalidConfig, i, ruleErr)
		}
		rule.Use = SANUseHostname
		if rule.Depth == 0 && strings.Contains(rule.Pattern, "*") {
			rule.Depth = 1
		}
		out.dnsNames = append(out.dnsNames, rule)
	}

	for i, prefix := range p.IPPrefixes {
		if !prefix.IsValid() {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: IP prefix %d is invalid", ErrInvalidConfig, i)
		}
		if prefix.Addr().Is4In6() {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		out.ipPrefixes = append(out.ipPrefixes, prefix.Masked())
	}

	out.selectors = cloneStringMap(p.NodeSelectors)
	for key, value := range out.selectors {
		if key == "" || value == "" {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: empty node selector", ErrInvalidConfig)
		}
	}

	var uses PrincipalUse
	for _, use := range p.Uses {
		if use != UseClient && use != UseServer {
			return compiledIdentityPolicy{}, fmt.Errorf("%w: unknown principal use %d", ErrInvalidConfig, uint8(use))
		}
		uses |= use
	}
	out.uses = uses
	return out, nil
}

func (p compiledIdentityPolicy) match(id WorkloadIdentity, role PrincipalUse) error {
	if id.TrustDomain != p.trustDomain {
		return fmt.Errorf("%w: trust domain %q is not %q", ErrPolicyDenied, id.TrustDomain, p.trustDomain)
	}

	if !p.pathMatches(id.SPIFFEID.Path) {
		return fmt.Errorf("%w: path %q is not allowed", ErrPolicyDenied, id.SPIFFEID.Path)
	}
	if role != 0 && p.uses != 0 && p.uses&role == 0 {
		return fmt.Errorf("%w: identity %s is not allowed for %s mTLS", ErrPolicyDenied, id.SPIFFEID, role)
	}

	for _, san := range id.SANs {
		switch san.Type {
		case SANDNS:
			if !p.matchesDNS(san.Value) {
				return fmt.Errorf("%w: DNS SAN %q is not allowed", ErrPolicyDenied, san.Value)
			}
		case SANIP:
			addr, err := netip.ParseAddr(san.Value)
			if err != nil || !p.matchesIP(addr) {
				return fmt.Errorf("%w: IP SAN %q is not allowed", ErrPolicyDenied, san.Value)
			}
		case SANURI:
			if san.Value != id.SPIFFEID.String() {
				return fmt.Errorf("%w: URI SAN %q is not the workload identity", ErrPolicyDenied, san.Value)
			}
		default:
			return fmt.Errorf("%w: unknown SAN type %q", ErrPolicyDenied, san.Type)
		}
	}

	for key, value := range p.selectors {
		if id.Node.Selectors[key] != value {
			return fmt.Errorf("%w: node selector %s=%s is required", ErrPolicyDenied, key, value)
		}
	}
	return nil
}

func (p compiledIdentityPolicy) pathMatches(path SPIFFEPath) bool {
	for _, exact := range p.paths {
		if path == exact {
			return true
		}
	}
	for _, prefix := range p.prefixes {
		if hasPathPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (p compiledIdentityPolicy) matchesDNS(name string) bool {
	for _, rule := range p.dnsNames {
		if rule.Match(name) {
			return true
		}
	}
	return false
}

func (p compiledIdentityPolicy) matchesIP(addr netip.Addr) bool {
	for _, prefix := range p.ipPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ConnectionPolicy applies both the caller and target identity policies for a
// named connection policy. An empty Actions slice permits any action; when
// actions are present they are matched exactly and case-sensitively.
type ConnectionPolicy struct {
	Caller  IdentityPolicy
	Target  IdentityPolicy
	Actions []string
}

// Validate checks both embedded identity policies.
func (p ConnectionPolicy) Validate() error {
	_, err := compileConnectionPolicy(p)
	return err
}

type compiledConnectionPolicy struct {
	caller  compiledIdentityPolicy
	target  compiledIdentityPolicy
	actions map[string]struct{}
}

func compileConnectionPolicy(p ConnectionPolicy) (compiledConnectionPolicy, error) {
	caller, err := compileIdentityPolicy(p.Caller)
	if err != nil {
		return compiledConnectionPolicy{}, fmt.Errorf("%w: caller policy: %v", ErrInvalidConfig, err)
	}
	target, err := compileIdentityPolicy(p.Target)
	if err != nil {
		return compiledConnectionPolicy{}, fmt.Errorf("%w: target policy: %v", ErrInvalidConfig, err)
	}
	var actions map[string]struct{}
	if len(p.Actions) != 0 {
		actions = make(map[string]struct{}, len(p.Actions))
		for i, action := range p.Actions {
			if action == "" {
				return compiledConnectionPolicy{}, fmt.Errorf("%w: action %d is empty", ErrInvalidConfig, i)
			}
			actions[action] = struct{}{}
		}
	}
	return compiledConnectionPolicy{caller: caller, target: target, actions: actions}, nil
}

func (p compiledConnectionPolicy) actionAllowed(action string) bool {
	if len(p.actions) == 0 {
		return true
	}
	_, ok := p.actions[action]
	return ok
}
