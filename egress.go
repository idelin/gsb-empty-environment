package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

// Sentinel errors returned by the package. Callers should use errors.Is.
var (
	// ErrNotAllowed means the scheme+authority pair is not in the allow list.
	ErrNotAllowed = errors.New("netsec: scheme and authority are not in the allow list")
	// ErrBlockedAddress means a resolved (or literal) address points at a
	// loopback, link-local, private, unspecified range or the cloud metadata
	// endpoint 169.254.169.254.
	ErrBlockedAddress = errors.New("netsec: address resolves to a blocked network range")
	// ErrHostMismatch means the request Host header does not match the
	// request authority after normalization.
	ErrHostMismatch = errors.New("netsec: Host header does not match authority")
	// ErrForbiddenHeader means the request carries a Connection,
	// Transfer-Encoding or Upgrade header.
	ErrForbiddenHeader = errors.New("netsec: forbidden request header")
	// ErrNoAddress means name resolution returned no usable addresses.
	ErrNoAddress = errors.New("netsec: resolver returned no usable addresses")
)

// Resolver performs the single, authoritative name lookup for an allowed
// host. It is an interface so tests (and hosts with custom resolution) can
// avoid touching the network. net.DefaultResolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Entry is one caller-registered allow-list item. Only exact scheme +
// authority matches are allowed; there is no wildcard or suffix matching.
type Entry struct {
	// Scheme is "http" or "https" (case-insensitive).
	Scheme string
	// Authority is host[:port]. Host names are matched case-insensitively;
	// a missing port defaults to 80 (http) or 443 (https); IPv6 literals
	// must be bracketed, e.g. "[2001:db8::1]:8443".
	Authority string
}

// CheckInput is the outgoing request information the wasi:http
// outgoing-handler must hand to Authorizer.Check before dialing.
type CheckInput struct {
	Scheme    string
	Authority string
	// Host is the value the component wants to send on the Host header.
	// When empty, Decision.Apply fills it with the canonical authority.
	Host string
	// Header carries the request headers. Canonical MIME keys are used for
	// the forbidden-header scan, as net/http does.
	Header http.Header
}

// Option customizes an Authorizer.
type Option func(*Authorizer)

// WithResolver replaces the default net.DefaultResolver. The supplied
// resolver is the only place name resolution happens for an authorized
// request, which is what makes DNS-rebinding pinning possible.
func WithResolver(r Resolver) Option {
	return func(a *Authorizer) {
		if r != nil {
			a.resolver = r
		}
	}
}

// Authorizer holds a component's registered allow list and enforces every
// outbound request against it. A single Authorizer is safe for concurrent
// use after construction; use New to build a new one for updated entries.
type Authorizer struct {
	allowed map[key]struct{}

	resolver Resolver
	baseDial func(ctx context.Context, network, address string) (net.Conn, error)
}

// New builds an Authorizer from the registered entries. Entries pointing at
// literal loopback/link-local/private/metadata addresses are rejected up
// front so an allow list can never legitimately target sensitive ranges.
func New(entries []Entry, opts ...Option) (*Authorizer, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	a := &Authorizer{
		allowed:  make(map[key]struct{}, len(entries)),
		resolver: net.DefaultResolver,
		baseDial: dialer.DialContext,
	}
	for _, opt := range opts {
		opt(a)
	}

	for _, entry := range entries {
		auth, err := normalizeAuthority(entry.Scheme, entry.Authority)
		if err != nil {
			return nil, err
		}
		if addr, err := netip.ParseAddr(auth.host); err == nil {
			if err := validateIP(addr); err != nil {
				return nil, fmt.Errorf("netsec: entry %s://%s: %w", entry.Scheme, entry.Authority, err)
			}
		}
		a.allowed[auth.mapKey()] = struct{}{}
	}
	return a, nil
}

// Decision is the result of a successful Check. It carries the addresses
// produced by the single authorized resolution; DialContext and the
// Transport/Client helpers connect only to those addresses and never
// re-resolve the host name.
type Decision struct {
	authority authority
	addrs     []netip.Addr

	baseDial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Addrs returns the validated addresses the connection is pinned to.
// Callers dialing manually must use these instead of resolving again.
func (d *Decision) Addrs() []netip.Addr {
	out := make([]netip.Addr, len(d.addrs))
	copy(out, d.addrs)
	return out
}

// Authority returns the canonical authority (lower-cased host, default port
// filled in, IPv6 bracketed) that must appear on the Host header.
func (d *Decision) Authority() string {
	return d.authority.String()
}

// Check authorizes one outgoing request: it matches the allow list,
// validates the Host header and forbidden headers, then performs the single
// name resolution and rejects any sensitive destination address.
func (a *Authorizer) Check(ctx context.Context, in CheckInput) (*Decision, error) {
	auth, err := normalizeAuthority(in.Scheme, in.Authority)
	if err != nil {
		return nil, err
	}
	if _, ok := a.allowed[auth.mapKey()]; !ok {
		return nil, fmt.Errorf("%w: %s://%s", ErrNotAllowed, auth.scheme, auth.String())
	}

	if in.Host != "" && !sameAuthority(auth.scheme, auth.String(), in.Host) {
		return nil, fmt.Errorf("%w: Host %q vs authority %q", ErrHostMismatch, in.Host, auth.String())
	}
	if err := checkForbiddenHeaders(in.Header); err != nil {
		return nil, err
	}

	var addrs []netip.Addr
	if addr, err := netip.ParseAddr(auth.host); err == nil {
		if err := validateIP(addr); err != nil {
			return nil, fmt.Errorf("%w: %s", err, addr)
		}
		addrs = []netip.Addr{addr}
	} else {
		resolved, err := a.resolver.LookupNetIP(ctx, "ip", auth.host)
		if err != nil {
			return nil, fmt.Errorf("netsec: resolving %s: %w", auth.host, err)
		}
		for _, addr := range resolved {
			if err := validateIP(addr); err != nil {
				return nil, fmt.Errorf("%w: %s resolves to %s", err, auth.host, addr)
			}
			addrs = append(addrs, addr)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("%w: %s", ErrNoAddress, auth.host)
		}
	}

	return &Decision{authority: auth, addrs: addrs, baseDial: a.baseDial}, nil
}

// DialContext connects to one of the validated addresses on the authorized
// port. The address passed to the underlying dialer is always an IP literal,
// so the host name is never resolved a second time.
func (d *Decision) DialContext(ctx context.Context, network string) (net.Conn, error) {
	var candidates []netip.Addr
	for _, addr := range d.addrs {
		switch network {
		case "tcp", "tcp4", "tcp6":
			if network == "tcp4" && !addr.Is4() {
				continue
			}
			if network == "tcp6" && !addr.Is6() {
				continue
			}
		}
		candidates = append(candidates, addr)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no address for network %s", ErrNoAddress, network)
	}

	var errs []error
	for _, addr := range candidates {
		target := net.JoinHostPort(addr.String(), strconv.Itoa(d.authority.port))
		conn, err := d.baseDial(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// Apply enforces the wire-level Host header: req.Host is set to the
// canonical authority. Call it on the request before sending.
func (d *Decision) Apply(req *http.Request) {
	req.Host = d.authority.String()
}

// Transport returns an http.Transport whose dialer is pinned to the
// validated addresses. Proxy configuration from the environment is ignored
// so traffic cannot be steered around the allow list, and TLS verifies
// against the authorized host name while dialing the pinned IP.
func (d *Decision) Transport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return d.DialContext(ctx, network)
	}
	if d.authority.scheme == "https" {
		tr.TLSClientConfig.MinVersion = tls.VersionTLS12
		tr.TLSClientConfig.ServerName = d.authority.host
	}
	return tr
}

// Client returns an http.Client built on the pinned Transport that never
// follows redirects: any 3xx response is returned to the caller as-is.
func (d *Decision) Client() *http.Client {
	return &http.Client{
		Transport: d.Transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// InputFromRequest extracts the CheckInput for an http.Request. The request
// URL must be absolute.
func InputFromRequest(req *http.Request) CheckInput {
	return CheckInput{
		Scheme:    req.URL.Scheme,
		Authority: req.URL.Host,
		Host:      req.Host,
		Header:    req.Header,
	}
}

var forbiddenHeaders = []string{"Connection", "Transfer-Encoding", "Upgrade"}

func checkForbiddenHeaders(h http.Header) error {
	if h == nil {
		return nil
	}
	for _, name := range forbiddenHeaders {
		if values := h.Values(name); len(values) > 0 {
			return fmt.Errorf("%w: %s", ErrForbiddenHeader, name)
		}
	}
	return nil
}

// validateIP rejects every address class that could reach cloud metadata or
// internal infrastructure. 169.254.169.254 is named explicitly because it is
// the canonical cloud metadata endpoint even though it is also link-local.
func validateIP(addr netip.Addr) error {
	if addr.Is4In6() {
		addr = addr.Unmap()
	}

	switch {
	case addr == netip.MustParseAddr("169.254.169.254"):
		return fmt.Errorf("%w: cloud metadata endpoint %s", ErrBlockedAddress, addr)
	case addr.IsUnspecified():
		return fmt.Errorf("%w: unspecified address %s", ErrBlockedAddress, addr)
	case addr.IsLoopback():
		return fmt.Errorf("%w: loopback address %s", ErrBlockedAddress, addr)
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return fmt.Errorf("%w: link-local address %s", ErrBlockedAddress, addr)
	case addr.IsPrivate():
		return fmt.Errorf("%w: private address %s", ErrBlockedAddress, addr)
	}
	return nil
}
