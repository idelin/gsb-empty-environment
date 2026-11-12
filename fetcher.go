// Package netsec provides a hardened CRL retrieval component for use by
// certificate-chain revocation callbacks.
//
// The fetcher treats every CRL distribution point as attacker-controlled
// input. Only http:// distribution points are ever retrieved; ldap, ldaps,
// file, ftp and scheme-less entries are ignored. Every host is resolved and
// every resolved address must be a global unicast address before a connection
// is opened, and the same check is applied again to every redirect target
// (at most three redirects are followed).
package netsec

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxBodyBytes is the largest CRL response body accepted from a single
	// retrieval (8 MiB).
	MaxBodyBytes = 8 << 20
	// MaxDistributionPointsPerCert limits how many distribution points on a
	// single certificate are examined.
	MaxDistributionPointsPerCert = 4
	// MaxFetchesPerChain limits total retrievals across one verified chain.
	MaxFetchesPerChain = 12
	// MaxRedirects limits redirects followed during a single retrieval.
	MaxRedirects = 3
)

// Sentinel errors returned by the fetcher.
var (
	ErrSchemeNotAllowed = errors.New("netsec: only http distribution points are allowed")
	ErrInvalidAddress   = errors.New("netsec: distribution point address is not global unicast")
	ErrTooManyRedirects = errors.New("netsec: too many redirects")
	ErrBodyTooLarge     = errors.New("netsec: CRL response body exceeds 8 MiB")
	ErrFetchLimit       = errors.New("netsec: chain fetch limit reached")
	ErrBadURL           = errors.New("netsec: malformed distribution point URL")
)

// IPLookup resolves a host to its IP addresses.
type IPLookup func(ctx context.Context, host string) ([]netip.Addr, error)

// DialFunc opens a TCP connection.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Option customizes a Fetcher.
type Option func(*Fetcher)

// WithLookup overrides DNS resolution.
func WithLookup(lookup IPLookup) Option {
	return func(f *Fetcher) { f.lookup = lookup }
}

// WithDialer overrides the underlying TCP dialer.
func WithDialer(dial DialFunc) Option {
	return func(f *Fetcher) { f.dial = dial }
}

// Fetcher retrieves CRLs from certificate distribution points under the
// restrictions described in the package documentation.
type Fetcher struct {
	lookup IPLookup
	dial   DialFunc
}

// NewFetcher constructs a Fetcher with production-default resolver and dialer.
func NewFetcher(opts ...Option) *Fetcher {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	f := &Fetcher{
		lookup: defaultLookup,
		dial:   dialer.DialContext,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// ChainFetcher tracks the per-chain retrieval budget while validating a
// certificate chain. Each verified chain needs its own instance.
type ChainFetcher struct {
	fetcher *Fetcher
	fetches int
}

// NewChainFetcher wraps f for use during one chain verification callback.
func NewChainFetcher(f *Fetcher) *ChainFetcher {
	return &ChainFetcher{fetcher: f}
}

// Fetched reports how many retrievals have been charged to this chain.
func (c *ChainFetcher) Fetched() int { return c.fetches }

// FetchCRLForCert retrieves CRLs for the distribution points carried in the
// DER of cert. Entries that are not http distribution points, or that denote
// indirect CRLs (cRLIssuer or reasons present), are skipped. At most
// MaxDistributionPointsPerCert distribution points are inspected per
// certificate and at most MaxFetchesPerChain retrievals happen per chain.
func (c *ChainFetcher) FetchCRLForCert(ctx context.Context, cert *x509.Certificate) ([][]byte, error) {
	entries, err := parseCRLDistributionPoints(cert.Raw)
	if err != nil {
		return nil, err
	}
	if len(entries) > MaxDistributionPointsPerCert {
		entries = entries[:MaxDistributionPointsPerCert]
	}

	var bodies [][]byte
	for _, entry := range entries {
		// Indirect CRL: the CRL is not issued by this certificate's issuer.
		if entry.reasons || entry.crlIssuer {
			continue
		}
		for _, rawURL := range entry.uris {
			if c.fetches >= MaxFetchesPerChain {
				return bodies, fmt.Errorf("%w: %d", ErrFetchLimit, MaxFetchesPerChain)
			}
			// Non-http and scheme-less distribution points are ignored rather
			// than treated as revocation failures.
			target, perr := validateHTTPURL(rawURL)
			if perr != nil {
				if errors.Is(perr, ErrSchemeNotAllowed) || errors.Is(perr, ErrBadURL) {
					continue
				}
				return nil, perr
			}
			body, ferr := c.fetcher.fetch(ctx, target)
			c.fetches++
			if ferr != nil {
				return nil, ferr
			}
			bodies = append(bodies, body)
		}
	}
	return bodies, nil
}

// FetchCRL performs a single one-off retrieval outside of chain accounting.
// The same scheme, address, redirect and body restrictions apply.
func (f *Fetcher) FetchCRL(ctx context.Context, rawURL string) ([]byte, error) {
	target, err := validateHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	return f.fetch(ctx, target)
}

// fetch performs one retrieval, re-validating every redirect target and
// connecting only to validated addresses.
func (f *Fetcher) fetch(ctx context.Context, target *url.URL) ([]byte, error) {
	if err := f.preflight(ctx, target); err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: f.transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > MaxRedirects {
				return fmt.Errorf("%w: %d", ErrTooManyRedirects, MaxRedirects)
			}
			next, err := validateHTTPURL(req.URL.String())
			if err != nil {
				return err
			}
			// Each redirect is re-validated independently so a benign initial
			// host cannot bounce the client at an internal/metadata address.
			if err := f.preflight(req.Context(), next); err != nil {
				return err
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("netsec: CRL endpoint returned HTTP %d", resp.StatusCode)
	}

	// Read one byte beyond the limit so an exactly-8 MiB body still succeeds.
	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBodyBytes {
		return nil, ErrBodyTooLarge
	}
	return body, nil
}

// transport returns an HTTP transport whose dialer re-resolves the host and
// only ever dials validated global unicast addresses. This pins the actual
// connection to the addresses that passed validation and closes the DNS
// rebinding gap between resolution and dial time.
func (f *Fetcher) transport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           f.checkedDial,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

// checkedDial resolves host, demands that every resolved address is global
// unicast, and then dials one of those validated addresses.
func (f *Fetcher) checkedDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadURL, err)
	}
	addrs, err := f.resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("%w: invalid port %q", ErrBadURL, port)
	}

	var lastErr error
	for _, addr := range addrs {
		conn, derr := f.dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	return nil, fmt.Errorf("netsec: no validated address reachable for %q: %w", host, lastErr)
}

// preflight resolves the target host and validates every address before any
// HTTP request is issued; the dialer performs the same check again.
func (f *Fetcher) preflight(ctx context.Context, target *url.URL) error {
	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrBadURL)
	}
	if _, err := f.resolveHost(ctx, host); err != nil {
		return err
	}
	return nil
}

func (f *Fetcher) resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if !isGlobalUnicast(addr) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidAddress, addr)
		}
		return []netip.Addr{addr}, nil
	}

	addrs, err := f.lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolving %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("netsec: no addresses for host %q", host)
	}
	for _, addr := range addrs {
		if !isGlobalUnicast(addr) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrInvalidAddress, host, addr)
		}
	}
	return addrs, nil
}

// validateHTTPURL accepts only absolute http:// URLs with a host and no
// embedded userinfo. Everything else (ldap, ldaps, file, ftp, scheme-less,
// https, ...) is rejected with ErrSchemeNotAllowed or ErrBadURL.
func validateHTTPURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, ErrBadURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadURL, err)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("%w: %q", ErrSchemeNotAllowed, raw)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: missing host in %q", ErrBadURL, raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: userinfo not allowed in %q", ErrBadURL, raw)
	}
	return u, nil
}

// nonUnicastPrefixes lists ranges that must never be contacted by the
// fetcher. netip.Addr.IsGlobalUnicast already excludes loopback, multicast,
// link-local and unspecified addresses; these prefixes additionally remove
// shared-private (RFC 1918, CGNAT, ULA, ...) and reserved ranges.
var nonUnicastPrefixes = func() []netip.Prefix {
	cidrs := []string{
		"0.0.0.0/8",          // "this" network
		"10.0.0.0/8",         // RFC 1918
		"100.64.0.0/10",      // CGNAT
		"169.254.0.0/16",     // link-local (incl. 169.254.169.254)
		"172.16.0.0/12",      // RFC 1918
		"192.0.0.0/24",       // IETF protocol assignments
		"192.0.2.0/24",       // TEST-NET-1
		"192.88.99.0/24",     // 6to4 relay anycast
		"192.168.0.0/16",     // RFC 1918
		"198.18.0.0/15",      // benchmarking
		"198.51.100.0/24",    // TEST-NET-2
		"203.0.113.0/24",     // TEST-NET-3
		"224.0.0.0/4",        // multicast
		"240.0.0.0/4",        // reserved
		"255.255.255.255/32", // limited broadcast
		"::1/128",            // IPv6 loopback
		"fc00::/7",           // unique local
		"fe80::/10",          // IPv6 link-local
		"2001:db8::/32",      // documentation
	}
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

// isGlobalUnicast reports whether addr may be dialed. In addition to the
// standard-library definition it rejects RFC 1918/shared-private and other
// reserved ranges, scoped (zoned) addresses and IPv4-mapped equivalents of
// any blocked address.
func isGlobalUnicast(addr netip.Addr) bool {
	if addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range nonUnicastPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// defaultLookup uses the system resolver through the net package.
func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	return ips, nil
}
