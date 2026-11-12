package netsec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RFC9298PathPrefix is the fixed part of the CONNECT-UDP URI template from
// RFC 9298, Section 3.
const RFC9298PathPrefix = "/.well-known/masque/udp/"

var (
	// ErrInvalidTargetPath reports a malformed or non-canonical CONNECT-UDP
	// request path.
	ErrInvalidTargetPath = errors.New("netsec: invalid CONNECT-UDP target path")

	// ErrTargetNotAllowed reports a target whose resolved address set is not
	// acceptable under the built-in policy or the caller's allowlist.
	ErrTargetNotAllowed = errors.New("netsec: CONNECT-UDP target is not allowed")
)

// Target is a validated CONNECT-UDP destination. Addresses contains every
// address returned by the resolver, in resolver order, with duplicates
// removed. A handler must dial Addresses (or AddrPorts), never resolve Host
// again: doing so would reintroduce a DNS-rebinding window.
type Target struct {
	Host      string
	Port      uint16
	Addresses []netip.Addr
}

// AddrPorts returns the fixed, already-validated destination addresses.
func (t Target) AddrPorts() []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(t.Addresses))
	for _, addr := range t.Addresses {
		out = append(out, netip.AddrPortFrom(addr, t.Port))
	}
	return out
}

// Resolver is implemented by net.Resolver. It exists so callers and tests can
// inject a resolver whose complete answer set is inspected before use.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Admitter validates CONNECT-UDP targets. Allow is mandatory: a target is
// admitted only when IsPublicAddress returns true for every resolved address
// and Allow returns true for every resolved address. A nil Allow rejects every
// target rather than silently allowing one.
//
// A nil Resolver uses net.DefaultResolver.
type Admitter struct {
	Resolver Resolver
	Allow    func(netip.Addr) bool
}

// NewAdmitter constructs an Admitter. allow should be the caller's destination
// allowlist predicate. Passing nil is safe but rejects every target.
func NewAdmitter(resolver Resolver, allow func(netip.Addr) bool) *Admitter {
	return &Admitter{Resolver: resolver, Allow: allow}
}

// ParseTarget parses the escaped request path form of the RFC 9298 template:
//
//	/.well-known/masque/udp/{target_host}/{target_port}/
//
// ParseTarget validates the path and its two variables but does not resolve
// DNS or enforce address policy; use Admit for that.
func ParseTarget(escapedPath string) (Target, error) {
	if !strings.HasPrefix(escapedPath, RFC9298PathPrefix) {
		return Target{}, pathError("missing RFC 9298 path prefix")
	}

	rest := strings.TrimPrefix(escapedPath, RFC9298PathPrefix)
	segments := strings.Split(rest, "/")
	if len(segments) != 3 || segments[2] != "" {
		return Target{}, pathError("path must contain exactly host, port, and a trailing slash")
	}
	if segments[0] == "" {
		return Target{}, pathError("empty host segment")
	}
	if segments[1] == "" {
		return Target{}, pathError("empty port segment")
	}

	host, err := decodeSegment(segments[0], "host")
	if err != nil {
		return Target{}, err
	}
	if err := validateHost(host); err != nil {
		return Target{}, err
	}

	portText, err := decodeSegment(segments[1], "port")
	if err != nil {
		return Target{}, err
	}
	for i := 0; i < len(portText); i++ {
		if portText[i] < '0' || portText[i] > '9' {
			return Target{}, pathError("port is not decimal")
		}
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return Target{}, pathError("port must be between 1 and 65535")
	}

	return Target{Host: host, Port: uint16(port)}, nil
}

// ParseTargetPath is an explicit alias for ParseTarget.
func ParseTargetPath(escapedPath string) (Target, error) {
	return ParseTarget(escapedPath)
}

// Admit parses an escaped path and resolves its target. It rejects the whole
// target if any DNS answer is disallowed, and returns concrete addresses so a
// subsequent dial does not perform another DNS lookup.
func (a *Admitter) Admit(ctx context.Context, escapedPath string) (Target, error) {
	target, err := ParseTarget(escapedPath)
	if err != nil {
		return Target{}, err
	}
	if a == nil {
		return Target{}, ErrTargetNotAllowed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	addresses, err := a.resolve(ctx, target.Host)
	if err != nil {
		return Target{}, err
	}
	if len(addresses) == 0 {
		return Target{}, fmt.Errorf("%w: resolver returned no addresses for %q", ErrTargetNotAllowed, target.Host)
	}

	for _, addr := range addresses {
		if !IsPublicAddress(addr) {
			return Target{}, fmt.Errorf("%w: resolved address %s is in a denied network", ErrTargetNotAllowed, addr)
		}
		if a.Allow == nil || !a.Allow(addr) {
			return Target{}, fmt.Errorf("%w: resolved address %s is not in the caller allowlist", ErrTargetNotAllowed, addr)
		}
	}

	target.Addresses = addresses
	return target, nil
}

// AdmitRequest admits the target of a CONNECT-UDP HTTP/3 request. It uses the
// request's escaped URL path, so percent-encoded separators cannot be hidden by
// net/url's Path field.
func (a *Admitter) AdmitRequest(r *http.Request) (Target, error) {
	if r == nil || r.URL == nil {
		return Target{}, pathError("request has no URL")
	}
	return a.Admit(r.Context(), r.URL.EscapedPath())
}

func (a *Admitter) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{normalizeAddr(addr)}, nil
	}

	resolver := a.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	raw, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve %q: %v", ErrTargetNotAllowed, host, err)
	}

	seen := make(map[netip.Addr]struct{}, len(raw))
	addrs := make([]netip.Addr, 0, len(raw))
	for _, addr := range raw {
		addr = normalizeAddr(addr)
		if !addr.IsValid() || addr.Zone() != "" {
			return nil, fmt.Errorf("%w: resolver returned invalid address %q", ErrTargetNotAllowed, addr)
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

func normalizeAddr(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return addr.Unmap()
	}
	return addr
}

func decodeSegment(raw, name string) (string, error) {
	if raw == "" {
		return "", pathError("empty %s segment", name)
	}

	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", pathError("invalid percent-encoding in %s segment", name)
	}
	if decoded == "" {
		return "", pathError("empty %s segment", name)
	}
	if strings.Contains(decoded, "%") {
		return "", pathError("%s segment contains double-encoded data", name)
	}
	if !utf8.ValidString(decoded) {
		return "", pathError("%s segment is not valid UTF-8", name)
	}
	for _, r := range decoded {
		switch {
		case r == '/' || r == '\\' || r == '?' || r == '#':
			return "", pathError("%s segment contains a path delimiter", name)
		case unicode.IsControl(r):
			return "", pathError("%s segment contains a control character", name)
		}
	}
	return decoded, nil
}

func validateHost(host string) error {
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return pathError("scoped IPv6 literals are not allowed")
		}
		return nil
	}

	if len(host) > 253 {
		return pathError("host name is too long")
	}
	if strings.Contains(host, ":") || strings.Contains(host, "@") {
		return pathError("host contains an invalid character")
	}
	for _, r := range host {
		if r > unicode.MaxASCII {
			return pathError("host name must be ASCII or an IP literal")
		}
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '.', r == '_', r == '~':
		default:
			return pathError("host contains an invalid character")
		}
	}
	if strings.HasPrefix(host, ".") {
		return pathError("host has an empty label")
	}

	name := strings.TrimSuffix(host, ".")
	if name == "" {
		return pathError("host has an empty label")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return pathError("host has an empty label")
		}
		if len(label) > 63 {
			return pathError("host label is too long")
		}
	}
	return nil
}

func pathError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTargetPath, fmt.Sprintf(format, args...))
}

var deniedPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// IsPublicAddress reports whether addr may be used as a public CONNECT-UDP
// destination. It rejects invalid, unspecified, loopback, link-local, private,
// shared, multicast, and other non-global addresses by default. IPv4-mapped
// IPv6 addresses are unmapped and evaluated using their IPv4 address.
func IsPublicAddress(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = normalizeAddr(addr)
	if addr.Zone() != "" {
		return false
	}
	if !addr.IsGlobalUnicast() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() ||
		addr.IsPrivate() {
		return false
	}
	for _, prefix := range deniedPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// AllowPrefixes returns an allowlist predicate for addr contained in prefixes.
// Input is normalized the same way as Admit, so callers may use IPv4 or IPv6
// prefixes regardless of whether the resolver returned a mapped address.
func AllowPrefixes(prefixes ...netip.Prefix) func(netip.Addr) bool {
	copied := make([]netip.Prefix, len(prefixes))
	copy(copied, prefixes)
	return func(addr netip.Addr) bool {
		if !addr.IsValid() {
			return false
		}
		addr = normalizeAddr(addr)
		for _, prefix := range copied {
			if prefix.IsValid() && prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
}
