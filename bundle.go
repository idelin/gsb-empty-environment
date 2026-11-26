package spiffefed

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sort"
	"strings"
)

// TrustBundle is the set of trust anchors for exactly one trust domain.
type TrustBundle struct {
	domain string
	certs  []*x509.Certificate
	pool   *x509.CertPool
}

// NewTrustBundle creates an isolated bundle for domain. Every certificate is
// used only as an anchor for that domain; no system roots are consulted.
func NewTrustBundle(domain string, certs ...*x509.Certificate) (*TrustBundle, error) {
	if err := ValidateTrustDomain(domain); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTrustBundle, err)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("%w: bundle %q has no certificates", ErrInvalidTrustBundle, domain)
	}

	pool := x509.NewCertPool()
	stored := make([]*x509.Certificate, 0, len(certs))
	seen := make(map[string]struct{}, len(certs))
	for i, cert := range certs {
		if cert == nil {
			return nil, fmt.Errorf("%w: bundle %q certificate %d is nil", ErrInvalidTrustBundle, domain, i)
		}
		if cert.Raw == nil || len(cert.Raw) == 0 {
			return nil, fmt.Errorf("%w: bundle %q certificate %d has no DER encoding", ErrInvalidTrustBundle, domain, i)
		}
		if !cert.IsCA {
			return nil, fmt.Errorf("%w: bundle %q certificate %d is not a CA", ErrInvalidTrustBundle, domain, i)
		}
		key := string(cert.Raw)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		stored = append(stored, cert)
		pool.AddCert(cert)
	}
	if len(stored) == 0 {
		return nil, fmt.Errorf("%w: bundle %q has no unique certificates", ErrInvalidTrustBundle, domain)
	}
	return &TrustBundle{domain: domain, certs: stored, pool: pool}, nil
}

// ParseTrustBundlePEM parses all CERTIFICATE blocks in data into one domain
// bundle. Non-whitespace data outside PEM certificate blocks is rejected.
func ParseTrustBundlePEM(domain string, data []byte) (*TrustBundle, error) {
	rest := data
	var certs []*x509.Certificate
	for {
		block, tail := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: unexpected PEM block %q", ErrInvalidTrustBundle, block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: parse certificate: %v", ErrInvalidTrustBundle, err)
		}
		certs = append(certs, cert)
		rest = tail
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: trailing non-PEM data", ErrInvalidTrustBundle)
	}
	return NewTrustBundle(domain, certs...)
}

// TrustDomain returns the single domain this bundle belongs to.
func (b *TrustBundle) TrustDomain() string {
	if b == nil {
		return ""
	}
	return b.domain
}

// Certificates returns a shallow copy of the bundle's certificate slice.
func (b *TrustBundle) Certificates() []*x509.Certificate {
	if b == nil {
		return nil
	}
	return append([]*x509.Certificate(nil), b.certs...)
}

// BundleSet is an immutable, exact-match map from trust domain to bundle.
type BundleSet struct {
	bundles map[string]*TrustBundle
}

// NewBundleSet creates a bundle set. Duplicate domains are rejected so there
// is no merge or fallback ordering ambiguity.
func NewBundleSet(bundles ...*TrustBundle) (*BundleSet, error) {
	if len(bundles) == 0 {
		return nil, fmt.Errorf("%w: bundle set is empty", ErrInvalidTrustBundle)
	}
	set := &BundleSet{bundles: make(map[string]*TrustBundle, len(bundles))}
	for _, bundle := range bundles {
		if bundle == nil {
			return nil, fmt.Errorf("%w: nil bundle", ErrInvalidTrustBundle)
		}
		if _, exists := set.bundles[bundle.domain]; exists {
			return nil, fmt.Errorf("%w: duplicate bundle for trust domain %q", ErrInvalidTrustBundle, bundle.domain)
		}
		set.bundles[bundle.domain] = bundle
	}
	return set, nil
}

// Bundle returns only the bundle registered for trustDomain. There is no
// fallback to another domain.
func (s *BundleSet) Bundle(trustDomain string) (*TrustBundle, bool) {
	if s == nil {
		return nil, false
	}
	bundle, ok := s.bundles[trustDomain]
	return bundle, ok
}

// TrustDomains returns the sorted domains in the set.
func (s *BundleSet) TrustDomains() []string {
	if s == nil {
		return nil
	}
	domains := make([]string, 0, len(s.bundles))
	for domain := range s.bundles {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}
