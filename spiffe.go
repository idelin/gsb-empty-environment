package netsec

import (
	"net/url"
	"strings"
)

// parseSPIFFE parses raw as a SPIFFE ID and returns its trust domain.
//
// A valid SPIFFE ID uses the "spiffe" scheme, has no userinfo/port/query/
// fragment, carries a non-empty lowercase trust domain as host, and has an
// empty or slash-prefixed path. See https://github.com/spiffe/spiffe/blob/main/standards/SPIFFE.md.
func parseSPIFFE(raw string) (trustDomain string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "spiffe" {
		return "", false
	}
	if u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	trustDomain = u.Hostname()
	if trustDomain == "" || trustDomain != strings.ToLower(trustDomain) ||
		!isValidDomain(trustDomain) {
		return "", false
	}
	if u.Path != "" && !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	return trustDomain, true
}

// matchTrustDomain reports whether trustDomain equals one of the allowed
// trust domains (exact match; SPIFFE trust domains are host names, not
// suffixes).
func matchTrustDomain(trustDomain string, trustDomains []string) bool {
	for _, td := range trustDomains {
		if trustDomain == normalizeDomain(td) {
			return true
		}
	}
	return false
}

// hasURIPrefix reports whether raw starts with one of the allowed prefixes.
func hasURIPrefix(raw string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(raw, prefix) {
			return true
		}
	}
	return false
}
