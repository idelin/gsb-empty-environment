package spiffefed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ServicePolicy is the per-target-service federation allowlist. A federated
// trust domain is accepted only when it is explicitly listed here.
type ServicePolicy struct {
	FederatesWith []string `json:"federates_with"`
}

// FederationPolicyConfig is a JSON-friendly configuration representation.
type FederationPolicyConfig struct {
	LocalTrustDomain string                   `json:"local_trust_domain"`
	Services         map[string]ServicePolicy `json:"services"`
}

// FederationPolicy enforces that external trust domains are explicitly
// federated with the target service.
type FederationPolicy struct {
	local          string
	serviceDomains map[string]map[string]struct{}
}

// NewFederationPolicy constructs a validated, immutable policy.
func NewFederationPolicy(localTrustDomain string, services map[string]ServicePolicy) (*FederationPolicy, error) {
	if err := ValidateTrustDomain(localTrustDomain); err != nil {
		return nil, fmt.Errorf("local trust domain: %w", err)
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("%w: at least one target service must be configured", ErrUnknownService)
	}

	p := &FederationPolicy{
		local:          localTrustDomain,
		serviceDomains: make(map[string]map[string]struct{}, len(services)),
	}
	for service, svc := range services {
		if err := validateServiceName(service); err != nil {
			return nil, err
		}
		domains := make(map[string]struct{}, len(svc.FederatesWith))
		for _, domain := range svc.FederatesWith {
			if err := ValidateTrustDomain(domain); err != nil {
				return nil, fmt.Errorf("service %q federates_with: %w", service, err)
			}
			if _, duplicate := domains[domain]; duplicate {
				return nil, fmt.Errorf("service %q lists trust domain %q more than once", service, domain)
			}
			domains[domain] = struct{}{}
		}
		p.serviceDomains[service] = domains
	}
	return p, nil
}

// ParseFederationPolicyJSON parses a strict JSON FederationPolicyConfig.
func ParseFederationPolicyJSON(data []byte) (*FederationPolicy, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg FederationPolicyConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode federation policy: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, err
	}
	return NewFederationPolicy(cfg.LocalTrustDomain, cfg.Services)
}

// LocalTrustDomain returns the protected local trust domain.
func (p *FederationPolicy) LocalTrustDomain() string {
	if p == nil {
		return ""
	}
	return p.local
}

// FederatesWith returns a sorted copy of the allowlist for a service.
func (p *FederationPolicy) FederatesWith(service string) ([]string, bool) {
	if p == nil {
		return nil, false
	}
	domains, ok := p.serviceDomains[service]
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(domains))
	for domain := range domains {
		out = append(out, domain)
	}
	sort.Strings(out)
	return out, true
}

// Allows applies fail-closed service and federation checks. Local trust-domain
// identities are permitted for a configured service; every external identity
// must be explicitly listed in that service's federates_with set.
func (p *FederationPolicy) Allows(service, trustDomain string) bool {
	if p == nil {
		return false
	}
	if err := ValidateTrustDomain(trustDomain); err != nil {
		return false
	}
	domains, ok := p.serviceDomains[service]
	if !ok {
		return false
	}
	if trustDomain == p.local {
		return true
	}
	_, ok = domains[trustDomain]
	return ok
}

// Authorize returns a descriptive error when a service/domain pair is denied.
func (p *FederationPolicy) Authorize(service, trustDomain string) error {
	if p == nil {
		return fmt.Errorf("%w: federation policy is not configured", ErrFederationDenied)
	}
	if _, ok := p.serviceDomains[service]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownService, service)
	}
	if err := ValidateTrustDomain(trustDomain); err != nil {
		return err
	}
	if trustDomain == p.local {
		return nil
	}
	if _, ok := p.serviceDomains[service][trustDomain]; !ok {
		return fmt.Errorf("%w: service %q and trust domain %q", ErrFederationDenied, service, trustDomain)
	}
	return nil
}

func validateServiceName(service string) error {
	if service == "" || strings.TrimSpace(service) != service {
		return fmt.Errorf("%w: target service name must be non-empty without surrounding whitespace", ErrUnknownService)
	}
	if strings.ContainsAny(service, "*?") {
		return fmt.Errorf("%w: wildcard target service names are forbidden", ErrUnknownService)
	}
	if !isASCII(service) {
		return fmt.Errorf("%w: target service name must be ASCII", ErrUnknownService)
	}
	for i := 0; i < len(service); i++ {
		if service[i] < 0x21 || service[i] > 0x7e {
			return fmt.Errorf("%w: target service name contains control or whitespace", ErrUnknownService)
		}
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}
