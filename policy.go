package netsec

import (
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// Violation codes returned on the Decision.
const (
	ViolationInvalidPolicy          = "InvalidPolicy"
	ViolationEmptyNamespace         = "EmptyNamespace"
	ViolationUnknownNamespace       = "UnknownNamespace"
	ViolationCSRMismatch            = "CSRMismatch"
	ViolationInvalidDNSName         = "InvalidDNSName"
	ViolationSuffixNotAllowed       = "SuffixNotAllowed"
	ViolationEmailDomainNotAllowed  = "EmailDomainNotAllowed"
	ViolationIPNotAllowed           = "IPNotAllowed"
	ViolationURINotAllowed          = "URINotAllowed"
	ViolationSPIFFETrustDomain      = "SPIFFETrustDomain"
	ViolationSPIFFEInvalid          = "SPIFFEInvalid"
	ViolationIsCAForbidden          = "IsCAForbidden"
	ViolationDurationTooLong        = "DurationTooLong"
	ViolationRSAKeyTooShort         = "RSAKeyTooShort"
	ViolationECDSACurveNotAllowed   = "ECDSACurveNotAllowed"
	ViolationKeyAlgorithmNotAllowed = "KeyAlgorithmNotAllowed"
	ViolationKeyUsageNotAllowed     = "KeyUsageNotAllowed"
)

// Validate checks the issuer policy itself for misconfiguration.
func (p IssuerPolicy) Validate() error {
	switch {
	case p.MinRSAKeySize < 0, p.MinRSAKeySize > 0 && p.MinRSAKeySize < 2048:
		return Decision{Violations: []Violation{{
			Code:    ViolationInvalidPolicy,
			Message: "MinRSAKeySize must be zero or at least 2048",
		}}}
	case p.MaxDuration < 0:
		return Decision{Violations: []Violation{{
			Code:    ViolationInvalidPolicy,
			Message: "MaxDuration must not be negative",
		}}}
	}
	for _, curve := range p.AllowedECDSACurves {
		switch curve {
		case CurveP256, CurveP384, CurveP521:
		default:
			return Decision{Violations: []Violation{{
				Code:    ViolationInvalidPolicy,
				Message: "unknown ECDSA curve in AllowedECDSACurves: " + curve,
			}}}
		}
	}
	for _, algo := range p.AllowedKeyAlgorithms {
		switch algo {
		case KeyAlgorithmRSA, KeyAlgorithmECDSA, KeyAlgorithmEd25519:
		default:
			return Decision{Violations: []Violation{{
				Code:    ViolationInvalidPolicy,
				Message: "unknown key algorithm in AllowedKeyAlgorithms: " + algo,
			}}}
		}
	}
	return nil
}

func (p IssuerPolicy) minRSAKeySize() int {
	if p.MinRSAKeySize == 0 {
		return 2048
	}
	return p.MinRSAKeySize
}

func (p IssuerPolicy) keyAlgorithmAllowed(algo string) bool {
	if len(p.AllowedKeyAlgorithms) == 0 {
		return algo == KeyAlgorithmRSA || algo == KeyAlgorithmECDSA
	}
	return slices.Contains(p.AllowedKeyAlgorithms, algo)
}

// Evaluate applies the issuer policy to a certificate request and returns
// every detected violation. The request is allowed only when no violations
// are present.
func Evaluate(p IssuerPolicy, req Request) Decision {
	var violations []Violation
	add := func(code, message string) {
		violations = append(violations, Violation{Code: code, Message: message})
	}

	if err := p.Validate(); err != nil {
		return Decision{Allowed: false, Violations: err.(Decision).Violations}
	}

	ns := req.Requester.Namespace
	if strings.TrimSpace(ns) == "" {
		add(ViolationEmptyNamespace, "requester namespace is empty")
		return Decision{Allowed: false, Violations: violations}
	}

	knownNamespace := map[string]bool{}
	for key := range p.AllowedDomainSuffixes {
		knownNamespace[key] = true
	}
	for key := range p.TrustDomains {
		knownNamespace[key] = true
	}
	for key := range p.AllowedEmailDomains {
		knownNamespace[key] = true
	}
	for key := range p.AllowedIPPrefixes {
		knownNamespace[key] = true
	}
	for key := range p.AllowedURIPrefixes {
		knownNamespace[key] = true
	}
	if !knownNamespace[ns] {
		add(ViolationUnknownNamespace, "namespace "+ns+" has no issuer policy grants")
		return Decision{Allowed: false, Violations: violations}
	}

	checkConsistency(&violations, req)
	checkKeyAndCA(p, &violations, req)
	checkDNS(p, &violations, ns, req)
	checkEmails(p, &violations, ns, req)
	checkIPs(p, &violations, ns, req)
	checkURIs(p, &violations, ns, req)

	return Decision{Allowed: len(violations) == 0, Violations: violations}
}

// checkConsistency verifies that the CSR carries exactly the fields declared
// on the Certificate resource.
func checkConsistency(violations *[]Violation, req Request) {
	csr := req.CSR
	decl := req.Declaration
	add := func(message string) {
		*violations = append(*violations, Violation{Code: ViolationCSRMismatch, Message: message})
	}

	if normalizeDomain(csr.CommonName) != normalizeDomain(decl.CommonName) {
		add("CSR commonName does not match the Certificate declaration")
	}
	if csr.IsCA != decl.IsCA {
		add("CSR isCA flag does not match the Certificate declaration")
	}
	if !sameStringSet(csr.DNSNames, decl.DNSNames, normalizeDomain) {
		add("CSR DNS SANs do not match the Certificate declaration")
	}
	if !sameStringSet(csr.URIs, decl.URIs, func(s string) string { return s }) {
		add("CSR URI SANs do not match the Certificate declaration")
	}
	if !sameStringSet(csr.EmailAddresses, decl.EmailAddresses, normalizeDomain) {
		add("CSR email SANs do not match the Certificate declaration")
	}
	if !sameStringSet(csr.KeyUsages, decl.KeyUsages, func(s string) string { return s }) {
		add("CSR key usages do not match the Certificate declaration")
	}
	if !sameIPSet(csr.IPAddresses, decl.IPAddresses) {
		add("CSR IP SANs do not match the Certificate declaration")
	}
}

// checkKeyAndCA enforces basic constraints, validity and key policy.
func checkKeyAndCA(p IssuerPolicy, violations *[]Violation, req Request) {
	csr := req.CSR

	if p.ForbidCA && csr.IsCA {
		*violations = append(*violations, Violation{
			Code: ViolationIsCAForbidden, Message: "CA certificates are forbidden by issuer policy",
		})
	}

	if p.MaxDuration > 0 && req.Declaration.Duration > p.MaxDuration {
		*violations = append(*violations, Violation{
			Code:    ViolationDurationTooLong,
			Message: "requested duration exceeds the issuer maximum",
		})
	}

	switch csr.KeyAlgorithm {
	case KeyAlgorithmRSA:
		if csr.KeySize < p.minRSAKeySize() {
			*violations = append(*violations, Violation{
				Code:    ViolationRSAKeyTooShort,
				Message: "RSA key size below the required minimum",
			})
		}
	case KeyAlgorithmECDSA:
		if len(p.AllowedECDSACurves) == 0 || !slices.Contains(p.AllowedECDSACurves, csr.ECDSACurve) {
			*violations = append(*violations, Violation{
				Code:    ViolationECDSACurveNotAllowed,
				Message: "ECDSA curve " + csr.ECDSACurve + " is not allowed",
			})
		}
	default:
		if !p.keyAlgorithmAllowed(csr.KeyAlgorithm) {
			*violations = append(*violations, Violation{
				Code:    ViolationKeyAlgorithmNotAllowed,
				Message: "key algorithm " + csr.KeyAlgorithm + " is not allowed",
			})
		}
	}

	for _, usage := range csr.KeyUsages {
		if len(p.AllowedKeyUsages) > 0 && !slices.Contains(p.AllowedKeyUsages, usage) {
			*violations = append(*violations, Violation{
				Code:    ViolationKeyUsageNotAllowed,
				Message: "key usage " + usage + " is not allowed by issuer policy",
			})
		}
	}
}

// checkDNS validates the CN (when DNS shaped) and every DNS SAN against the
// namespace suffix grants, rejecting bad wildcard forms.
func checkDNS(p IssuerPolicy, violations *[]Violation, ns string, req Request) {
	suffixes := p.AllowedDomainSuffixes[ns]

	seen := map[string]bool{}
	names := req.CSR.DNSNames
	if cn := normalizeDomain(req.CSR.CommonName); cn != "" {
		names = append(slices.Clone(names), cn)
	}
	for _, raw := range names {
		name := normalizeDomain(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if !validateWildcard(name) {
			*violations = append(*violations, Violation{
				Code: ViolationInvalidDNSName, Message: "invalid DNS name or wildcard: " + raw,
			})
			continue
		}
		if !matchesSuffixSet(name, suffixes) {
			*violations = append(*violations, Violation{
				Code: ViolationSuffixNotAllowed, Message: "DNS name outside allowed suffixes: " + raw,
			})
		}
	}
}

// checkEmails restricts email SAN domains to the namespace email grants.
func checkEmails(p IssuerPolicy, violations *[]Violation, ns string, req Request) {
	domains := p.AllowedEmailDomains[ns]
	for _, raw := range req.CSR.EmailAddresses {
		addr := strings.ToLower(strings.TrimSpace(raw))
		local, domain, found := strings.Cut(addr, "@")
		if !found || local == "" || domain == "" || strings.Contains(domain, "@") ||
			!isValidDomain(domain) || !matchesSuffixSet(domain, domains) {
			*violations = append(*violations, Violation{
				Code: ViolationEmailDomainNotAllowed, Message: "email SAN not allowed: " + raw,
			})
		}
	}
}

// checkIPs requires every IP SAN to fall inside a granted CIDR prefix.
func checkIPs(p IssuerPolicy, violations *[]Violation, ns string, req Request) {
	prefixes := p.AllowedIPPrefixes[ns]
	for _, addr := range req.CSR.IPAddresses {
		allowed := false
		for _, prefix := range prefixes {
			if prefix.Contains(addr) {
				allowed = true
				break
			}
		}
		if !allowed {
			*violations = append(*violations, Violation{
				Code: ViolationIPNotAllowed, Message: "IP SAN outside allowed prefixes: " + addr.String(),
			})
		}
	}
}

// checkURIs separates SPIFFE IDs (trust-domain matched) from other URIs
// (prefix matched) and applies the matching rules independently.
func checkURIs(p IssuerPolicy, violations *[]Violation, ns string, req Request) {
	trustDomains := p.TrustDomains[ns]
	prefixes := p.AllowedURIPrefixes[ns]

	for _, raw := range req.CSR.URIs {
		if trustDomain, ok := parseSPIFFE(raw); ok {
			if !matchTrustDomain(trustDomain, trustDomains) {
				*violations = append(*violations, Violation{
					Code:    ViolationSPIFFETrustDomain,
					Message: "SPIFFE trust domain not trusted for namespace " + ns,
				})
			}
			continue
		}

		if strings.HasPrefix(raw, "spiffe:") {
			*violations = append(*violations, Violation{
				Code: ViolationSPIFFEInvalid, Message: "malformed SPIFFE ID: " + raw,
			})
			continue
		}

		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || !hasURIPrefix(raw, prefixes) {
			*violations = append(*violations, Violation{
				Code: ViolationURINotAllowed, Message: "URI SAN not allowed: " + raw,
			})
		}
	}
}

// sameStringSet compares two string slices as sets after applying norm to
// each element.
func sameStringSet(a, b []string, norm func(string) string) bool {
	set := make(map[string]int, len(a))
	for _, s := range a {
		set[norm(s)]++
	}
	for _, s := range b {
		key := norm(s)
		if set[key] == 0 {
			return false
		}
		set[key]--
	}
	for _, remaining := range set {
		if remaining != 0 {
			return false
		}
	}
	return true
}

// sameIPSet compares two IP address slices as sets.
func sameIPSet(a, b []netip.Addr) bool {
	set := make(map[string]int, len(a))
	for _, addr := range a {
		set[addr.String()]++
	}
	for _, addr := range b {
		key := addr.String()
		if set[key] == 0 {
			return false
		}
		set[key]--
	}
	for _, remaining := range set {
		if remaining != 0 {
			return false
		}
	}
	return true
}
