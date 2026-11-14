package netsec

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// LookupFunc resolves a domain name to its addresses. It is replaceable for
// tests and for applications that use a custom resolver.
type LookupFunc func(context.Context, string) ([]netip.Addr, error)

// Resolver validates domain names, URL hosts, and redirect hops. A nil Lookup
// uses net.DefaultResolver.
type Resolver struct {
	Lookup LookupFunc
}

// NewResolver returns a Resolver backed by net.DefaultResolver.
func NewResolver() *Resolver {
	return &Resolver{}
}

// Resolve resolves host and classifies every answer. IP literals, including
// mapped, compatible, 6to4, Teredo, zoned, decimal, and octal forms, are
// classified directly without a DNS lookup.
func (r *Resolver) Resolve(ctx context.Context, host string) ([]Result, error) {
	if r == nil {
		r = defaultResolver
	}
	if ctx == nil {
		ctx = context.Background()
	}
	addresses, err := r.lookupHost(ctx, host)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(addresses))
	for _, addr := range addresses {
		result := ClassifyAddr(addr)
		result.Input = strings.TrimSpace(host)
		results = append(results, result)
	}
	return results, nil
}

// ResolveAndCheck resolves host and returns a BlockedError if any answer is
// non-public. All results are returned, including the blocked one, so callers
// can log the complete DNS answer set.
func (r *Resolver) ResolveAndCheck(ctx context.Context, host string) ([]Result, error) {
	results, err := r.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}

	for _, result := range results {
		if !result.Public {
			return results, &BlockedError{
				Input:   strings.TrimSpace(host),
				Result:  result,
				Results: results,
			}
		}
	}
	return results, nil
}

// CheckHost returns a BlockedError if host is an IP literal that is not
// public, if any DNS answer is not public, or if resolution fails.
func (r *Resolver) CheckHost(ctx context.Context, host string) error {
	_, err := r.ResolveAndCheck(ctx, host)
	return err
}

// ValidateHost is an alias for CheckHost.
func (r *Resolver) ValidateHost(ctx context.Context, host string) error {
	return r.CheckHost(ctx, host)
}

// CheckHostPort validates a host:port endpoint.
func (r *Resolver) CheckHostPort(ctx context.Context, hostPort string) error {
	host, _, err := ParseEndpoint(hostPort)
	if err != nil {
		return err
	}
	return r.CheckHost(ctx, host)
}

// CheckURL validates the host of an http or https URL. The URL host is
// classified with the same rules as a bare address or DNS answer.
func (r *Resolver) CheckURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(preprocessURL(strings.TrimSpace(rawURL)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	return r.checkURL(ctx, parsed)
}

// ValidateURL is an alias for CheckURL.
func (r *Resolver) ValidateURL(ctx context.Context, rawURL string) error {
	return r.CheckURL(ctx, rawURL)
}

func (r *Resolver) checkURL(ctx context.Context, parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("%w: nil URL", ErrInvalidAddress)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("%w: %q", ErrUnsupportedScheme, parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("%w: URL has no host", ErrInvalidAddress)
	}
	if port := parsed.Port(); port != "" {
		if err := validatePort(port); err != nil {
			return err
		}
	}
	return r.CheckHost(ctx, host)
}

// CheckRequest validates the destination URL of an HTTP request.
func (r *Resolver) CheckRequest(ctx context.Context, request *http.Request) error {
	if request == nil || request.URL == nil {
		return fmt.Errorf("%w: nil request URL", ErrInvalidAddress)
	}
	return r.checkURL(ctx, request.URL)
}

// lookupHost returns the effective addresses for host. IP literals are parsed
// without consulting the resolver; everything else goes through Lookup.
func (r *Resolver) lookupHost(ctx context.Context, rawHost string) ([]netip.Addr, error) {
	host, _, err := ParseEndpoint(strings.TrimSpace(rawHost))
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, fmt.Errorf("%w: empty host", ErrInvalidAddress)
	}

	if addr, err := parseLiteral(host); err == nil {
		return []netip.Addr{addr}, nil
	} else if looksLikeIPLiteral(host) {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	if strings.ContainsAny(host, " \t\r\n/%\\[]@?#") || containsControl(host) {
		return nil, fmt.Errorf("%w: invalid host %q", ErrInvalidAddress, host)
	}

	lookup := r.Lookup
	if lookup == nil {
		lookup = defaultLookup
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve %q: %w", host, err)
	}

	seen := make(map[netip.Addr]struct{}, len(addresses))
	filtered := make([]netip.Addr, 0, len(addresses))
	for _, addr := range addresses {
		if !addr.IsValid() {
			continue
		}
		canonical := addr.WithZone("")
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		filtered = append(filtered, addr)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("netsec: resolve %q: no addresses returned", host)
	}
	return filtered, nil
}

func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

var defaultResolver = NewResolver()

// Resolve resolves and classifies host using the default resolver.
func Resolve(ctx context.Context, host string) ([]Result, error) {
	return defaultResolver.Resolve(ctx, host)
}

// ResolveAndCheck resolves and validates host using the default resolver.
func ResolveAndCheck(ctx context.Context, host string) ([]Result, error) {
	return defaultResolver.ResolveAndCheck(ctx, host)
}

// CheckHost validates host using the default resolver.
func CheckHost(ctx context.Context, host string) error {
	return defaultResolver.CheckHost(ctx, host)
}

// CheckHostPort validates a host:port endpoint using the default resolver.
func CheckHostPort(ctx context.Context, hostPort string) error {
	return defaultResolver.CheckHostPort(ctx, hostPort)
}

// CheckURL validates an URL host using the default resolver.
func CheckURL(ctx context.Context, rawURL string) error {
	return defaultResolver.CheckURL(ctx, rawURL)
}

// ValidateHost validates host using the default resolver.
func ValidateHost(ctx context.Context, host string) error {
	return defaultResolver.CheckHost(ctx, host)
}

// ValidateURL validates an URL host using the default resolver.
func ValidateURL(ctx context.Context, rawURL string) error {
	return defaultResolver.CheckURL(ctx, rawURL)
}

// CheckRequest validates an HTTP request URL using the default resolver.
func CheckRequest(ctx context.Context, request *http.Request) error {
	return defaultResolver.CheckRequest(ctx, request)
}
