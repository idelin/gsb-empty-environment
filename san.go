package netsec

import (
	"crypto/x509"
	"net"
	"strings"
)

// leafCoversHost reports whether the verified leaf certificate is valid for
// the (already canonicalized) request host. Only subjectAltName extensions are
// consulted; the legacy commonName is intentionally never used as a fallback.
//
// Matching rules:
//   - IP SANs match request IP literals by address equality;
//   - DNS SANs match after lower-casing, Punycode conversion and trailing-dot
//     removal;
//   - a "*" wildcard is accepted only as a complete left-most label and
//     matches exactly one label (never multiple labels, never an empty label).
func leafCoversHost(leaf *x509.Certificate, host string) bool {
	if leaf == nil || host == "" {
		return false
	}

	if ip := net.ParseIP(host); ip != nil {
		for _, candidate := range leaf.IPAddresses {
			if candidate.Equal(ip) {
				return true
			}
		}
		return false
	}

	for _, pattern := range leaf.DNSNames {
		if dnsNameMatches(pattern, host) {
			return true
		}
	}
	return false
}

// dnsNameMatches compares a certificate DNS SAN against a canonical request
// host. The SAN is reduced through the same canonicalization as the host so
// that U-label certificates and inconsistent casing/trailing dots behave
// identically.
func dnsNameMatches(rawPattern, host string) bool {
	pattern, err := canonicalHost(rawPattern)
	if err != nil {
		return false
	}

	// Wildcard: only "*.example.com" style patterns are honored. Partial
	// wildcards such as "a*.example.com" or labels containing additional dots
	// are treated as literal (and therefore non-matching) names.
	if labels := strings.Split(pattern, "."); labels[0] == "*" {
		hostLabels := strings.Split(host, ".")
		if len(labels) != len(hostLabels) || hostLabels[0] == "" {
			return false
		}
		return strings.Join(labels[1:], ".") == strings.Join(hostLabels[1:], ".")
	}

	return pattern == host
}
