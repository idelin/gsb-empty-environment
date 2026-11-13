package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// SubresourceStatus is the value of Options.Subresource when the admission
// request targets the status subresource.
const SubresourceStatus = "status"

// FieldError describes a single rejected field.
type FieldError struct {
	// Field is a dotted path relative to the Service object, e.g.
	// "spec.externalIPs[0]" or "status.loadBalancer.ingress[0].ip".
	Field string
	// Reason is a human-readable explanation of the rejection.
	Reason string
}

func (e *FieldError) Error() string {
	return e.Field + ": " + e.Reason
}

// ReservedSet is the cluster-wide set of addresses and ranges that no tenant
// may claim as an external address. Callers populate it with the cluster
// Service CIDR(s), the Pod CIDR(s), node addresses and the DNS Service
// addresses.
type ReservedSet struct {
	// CIDRs are reserved network ranges: the cluster Service CIDR(s) and
	// Pod CIDR(s), plus any other reserved range supplied by the caller.
	CIDRs []string
	// IPs are individual reserved addresses, typically node addresses and
	// the in-cluster DNS Service IP(s).
	IPs []string
}

// Options carries the inputs of an admission evaluation.
type Options struct {
	// AllowedCIDRs is the list of address ranges this namespace is permitted
	// to use for external IPs. An empty list rejects every address.
	AllowedCIDRs []string
	// Reserved is the cluster-wide reserved address set.
	Reserved ReservedSet
	// Subresource is empty for a regular create/update and "status" for a
	// status subresource update.
	Subresource string
}

// Policy is a compiled, reusable admission policy. Compiling once and reusing
// the result avoids re-parsing CIDRs on every admission request.
type Policy struct {
	allowed       []netip.Prefix
	reservedCIDRs []netip.Prefix
	reservedIPs   map[netip.Addr]struct{}
}

// NewPolicy compiles the namespace-allowed ranges and the reserved set. It
// returns an error if any supplied CIDR or reserved IP is malformed, so that
// misconfiguration fails closed at startup rather than on the first request.
func NewPolicy(opts Options) (*Policy, error) {
	policy := &Policy{
		reservedIPs: make(map[netip.Addr]struct{}),
	}

	var buildErrs []error
	for idx, cidr := range opts.AllowedCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			buildErrs = append(buildErrs, fmt.Errorf("allowedCIDRs[%d] %q: %w", idx, cidr, err))
			continue
		}
		policy.allowed = append(policy.allowed, prefix.Masked())
	}
	for idx, cidr := range opts.Reserved.CIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			buildErrs = append(buildErrs, fmt.Errorf("reserved.CIDRs[%d] %q: %w", idx, cidr, err))
			continue
		}
		policy.reservedCIDRs = append(policy.reservedCIDRs, prefix.Masked())
	}
	for idx, ip := range opts.Reserved.IPs {
		addr, ok := parseStrictIP(strings.TrimSpace(ip))
		if !ok {
			buildErrs = append(buildErrs, fmt.Errorf("reserved.IPs[%d] %q is not a valid IP address", idx, ip))
			continue
		}
		policy.reservedIPs[addr] = struct{}{}
	}
	if err := errors.Join(buildErrs...); err != nil {
		return nil, err
	}
	return policy, nil
}

// Validate compiles a policy for one-off use and evaluates svc against it.
// Repeated evaluations should construct a Policy with NewPolicy instead.
func Validate(svc *Service, opts Options) error {
	policy, err := NewPolicy(opts)
	if err != nil {
		return err
	}
	return policy.Validate(svc, opts.Subresource)
}

// Validate evaluates svc against the compiled policy. subresource is empty for
// a create/update of the whole object and SubresourceStatus for a status
// subresource update. The status subresource is validated with exactly the
// same IP rules and cannot be used to bypass the policy.
func (p *Policy) Validate(svc *Service, subresource string) error {
	if svc == nil {
		return &FieldError{Field: "service", Reason: "must not be nil"}
	}

	var errs []error

	// Status is validated on every request path, including dedicated status
	// subresource updates, so loadBalancer.ingress IPs can never bypass the
	// checks.
	for idx, ingress := range svc.Status.LoadBalancer.Ingress {
		field := fmt.Sprintf("status.loadBalancer.ingress[%d].ip", idx)
		if strings.TrimSpace(ingress.IP) == "" {
			// A hostname-only ingress entry is legitimate.
			continue
		}
		errs = append(errs, p.validateExternalIP(field, ingress.IP)...)
	}

	if subresource != SubresourceStatus {
		for idx, ip := range svc.Spec.ExternalIPs {
			field := fmt.Sprintf("spec.externalIPs[%d]", idx)
			errs = append(errs, p.validateExternalIP(field, ip)...)
		}
		if svc.Spec.Type == ServiceTypeExternalName {
			errs = append(errs, validateExternalName(svc.Namespace, svc.Spec.ExternalName)...)
		}
	}

	return errors.Join(errs...)
}

// validateExternalIP applies the full set of IP rules to one external IP
// literal.
func (p *Policy) validateExternalIP(field, literal string) []error {
	addr, ok := parseStrictIP(literal)
	if !ok {
		return []error{&FieldError{
			Field:  field,
			Reason: fmt.Sprintf("%q is not a valid IPv4 or IPv6 address (octal, hexadecimal and leading-zero IPv4 notation are rejected)", literal),
		}}
	}

	var errs []error
	switch {
	case addr.IsLoopback():
		errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " is a loopback address"})
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " is a link-local address"})
	case addr.IsMulticast():
		errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " is a multicast address"})
	case addr.IsUnspecified():
		errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " is the unspecified address"})
	}

	if _, blocked := p.reservedIPs[addr]; blocked {
		errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " is a reserved cluster address"})
	}
	for _, prefix := range p.reservedCIDRs {
		if prefixContains(prefix, addr) {
			errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " falls within reserved range " + prefix.String()})
			break
		}
	}

	allowed := false
	for _, prefix := range p.allowed {
		if prefixContains(prefix, addr) {
			allowed = true
			break
		}
	}
	if !allowed {
		errs = append(errs, &FieldError{Field: field, Reason: addr.String() + " is not within any address range allowed for this namespace"})
	}

	return errs
}

// prefixContains reports whether addr belongs to prefix. Address family must
// match; netip.Prefix.Contains already enforces this, but the check is kept
// explicit to document that an IPv4 range never covers an IPv6 address and
// vice versa.
func prefixContains(prefix netip.Prefix, addr netip.Addr) bool {
	if prefix.Addr().Is4() != addr.Is4() {
		return false
	}
	return prefix.Contains(addr)
}

// validateExternalName enforces the ExternalName-specific rules: the value
// must not be an IP literal and must not reference a Service in another
// namespace through the in-cluster DNS names ending in ".svc" or
// ".cluster.local".
func validateExternalName(namespace, externalName string) []error {
	const field = "spec.externalName"
	name := strings.TrimSpace(externalName)
	if name == "" {
		return []error{&FieldError{Field: field, Reason: "must not be empty for type ExternalName"}}
	}

	var errs []error
	if looksLikeIPLiteral(name) {
		errs = append(errs, &FieldError{Field: field, Reason: externalName + " is an IP literal; ExternalName requires a DNS name"})
	}

	// A single trailing dot denotes an FQDN and does not change the suffix.
	trimmed := strings.TrimSuffix(strings.ToLower(name), ".")
	if !strings.HasSuffix(trimmed, ".svc") && !strings.HasSuffix(trimmed, ".cluster.local") {
		return errs
	}

	labels := strings.Split(trimmed, ".")
	// Locate the "svc" label of an in-cluster Service DNS name
	// (<service>.<ns>.svc[.cluster-domain]).
	svcIdx := -1
	for i, label := range labels {
		if label == "svc" {
			svcIdx = i
			break
		}
	}
	switch {
	case svcIdx < 2:
		errs = append(errs, &FieldError{Field: field, Reason: externalName + " points into the cluster DNS zone but is not a valid in-cluster Service DNS name"})
	case labels[svcIdx-1] != namespace:
		errs = append(errs, &FieldError{
			Field:  field,
			Reason: fmt.Sprintf("%s points to Service %q in namespace %q, which is not the current namespace %q", externalName, labels[svcIdx-2], labels[svcIdx-1], namespace),
		})
	}
	return errs
}
