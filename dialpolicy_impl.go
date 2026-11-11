package dialpolicy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"syscall"
)

// ErrForbidden is returned when the active policy rejects the address that a
// request would otherwise be sent to. It is wrapped inside net.OpError or the
// redirect error, so callers detect it with errors.Is(err, ErrForbidden).
var ErrForbidden = errors.New("dialpolicy: address rejected by policy")

// maxRedirects mirrors net/http's default redirect limit.
const maxRedirects = 10

// Policy decides whether an IP address is allowed to be contacted. A Policy is
// treated as immutable while installed in a Guard; replacing it at runtime is
// done through Guard.UpdatePolicy.
type Policy interface {
	Allow(netip.Addr) bool
}

// Resolver resolves a host name to IP addresses. It is the single seam that
// makes name resolution injectable for tests and alternative DNS stacks.
type Resolver interface {
	LookupIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ResolverFunc adapts a plain function into a Resolver.
type ResolverFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

func (f ResolverFunc) LookupIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

// netResolver adapts the standard library's net.Resolver to the Resolver
// interface, converting net.IP values into the netip representation used by the
// policy layer.
type netResolver struct{ r *net.Resolver }

func (n netResolver) LookupIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	ips, err := n.r.LookupIP(ctx, network, host)
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, addr.Unmap())
		}
	}
	return addrs, nil
}

// Option configures a Guard at construction time.
type Option func(*Guard)

// WithResolver installs a custom resolver. Without it the system resolver
// (net.DefaultResolver) is used.
func WithResolver(r Resolver) Option {
	return func(g *Guard) {
		if r != nil {
			g.resolver = r
		}
	}
}

// WithDialer sets the base dialer. The Guard never mutates this dialer; it is
// copied per dial so that the platform Control hook can be layered on top.
func WithDialer(d *net.Dialer) Option {
	return func(g *Guard) {
		if d != nil {
			g.baseDialer = d
		}
	}
}

// WithTLSConfig supplies a base TLS configuration for the Transport. The
// ServerName is always derived from the request URL, never from the resolved
// IP, so this configuration must not hard-code a ServerName.
func WithTLSConfig(c *tls.Config) Option {
	return func(g *Guard) {
		if c != nil {
			g.tlsConfig = c
		}
	}
}

// Guard enforces a dial policy on every network connection made by the
// transports and clients it produces. All checks happen against concrete IP
// addresses, so a host name that changes meaning between requests cannot
// bypass the policy.
type Guard struct {
	policy     atomic.Pointer[policyHolder]
	resolver   Resolver
	baseDialer *net.Dialer
	tlsConfig  *tls.Config
	transport  atomic.Pointer[http.Transport]
	dialFn     func(ctx context.Context, network, address string) (net.Conn, error)
	tracker    connTracker
}

type policyHolder struct{ policy Policy }

// New builds a Guard enforcing the given policy. The policy may be replaced at
// any time with UpdatePolicy.
func New(policy Policy, opts ...Option) *Guard {
	if policy == nil {
		policy = PrefixPolicy{}
	}
	g := &Guard{
		resolver:   netResolver{r: net.DefaultResolver},
		baseDialer: &net.Dialer{},
	}
	g.dialFn = g.defaultDial
	g.policy.Store(&policyHolder{policy: policy})
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Policy returns the policy currently in force.
func (g *Guard) Policy() Policy {
	return g.policy.Load().policy
}

// UpdatePolicy atomically swaps the active policy and closes every idle
// connection that was pooled under the previous policy, and also closes every
// tracked connection whose concrete peer is rejected by the new policy.
// Closing the underlying socket unblocks I/O that is already waiting on an
// in-flight request, so no connection can ever complete a read or write to a
// forbidden peer after the swap.
func (g *Guard) UpdatePolicy(p Policy) {
	if p == nil {
		return
	}
	g.policy.Store(&policyHolder{policy: p})
	if tr := g.transport.Load(); tr != nil {
		tr.CloseIdleConnections()
	}
	g.tracker.dropIf(func(addr netip.Addr) bool { return !p.Allow(addr) })
}

func (g *Guard) addrAllowed(addr netip.Addr) bool {
	return g.Policy().Allow(addr)
}

// DialContext is a net.Dialer-style dial function. It resolves the host,
// filters the result set through the active policy, and only ever dials
// verified addresses. On multi-answer DNS responses the candidates are tried
// in order until one connects; rejected addresses are never dialed, even as a
// fallback.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: nil, Err: err}
	}

	addrs, err := g.resolve(ctx, network, host)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: nil, Err: err}
	}

	candidates, err := filterCandidates(network, addrs, g.addrAllowed)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: nil, Err: err}
	}

	var errs []error
	for _, candidate := range candidates {
		dialAddr := net.JoinHostPort(candidate.String(), port)
		conn, dialErr := g.dialFn(ctx, network, dialAddr)
		if dialErr != nil {
			errs = append(errs, dialErr)
			continue
		}
		return g.tracker.wrap(conn, g, candidate), nil
	}

	if len(errs) == 0 {
		errs = []error{ErrForbidden}
	}
	return nil, &net.OpError{
		Op:  "dial",
		Net: network,
		Err: errors.Join(errs...),
	}
}

// defaultDial performs the actual socket connection through a shallow-copied
// base dialer with the platform Control hook attached. Tests replace dialFn
// instead of touching this path.
func (g *Guard) defaultDial(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := *g.baseDialer
	dialer.Control = g.control
	return dialer.DialContext(ctx, network, address)
}

// control is the platform dial hook. It sees the concrete IP the kernel is
// about to connect to, providing defense in depth independent of the
// resolution path above.
func (g *Guard) control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return &net.OpError{Op: "dial", Net: network, Err: err}
	}
	if !g.addrAllowed(addr.Unmap()) {
		return &net.OpError{Op: "dial", Net: network, Addr: &net.IPAddr{IP: net.IP(addr.AsSlice())}, Err: ErrForbidden}
	}
	return nil
}

func (g *Guard) resolve(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	lookupNetwork := "ip"
	switch network {
	case "tcp4", "udp4", "ip4":
		lookupNetwork = "ip4"
	case "tcp6", "udp6", "ip6":
		lookupNetwork = "ip6"
	}
	return g.resolver.LookupIP(ctx, lookupNetwork, host)
}

func filterCandidates(network string, addrs []netip.Addr, allowed func(netip.Addr) bool) ([]netip.Addr, error) {
	candidates := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !familyMatches(network, addr) {
			continue
		}
		if !allowed(addr) {
			continue
		}
		candidates = append(candidates, addr)
	}
	if len(candidates) == 0 {
		return nil, ErrForbidden
	}
	return candidates, nil
}

func familyMatches(network string, addr netip.Addr) bool {
	switch network {
	case "tcp4", "udp4", "ip4":
		return addr.Is4()
	case "tcp6", "udp6", "ip6":
		return addr.Is6()
	default:
		return true
	}
}

// CheckRedirect is an http.Client.CheckRedirect function. Every redirect hop is
// validated here before any new connection is attempted: the scheme must be
// http or https and the target host must resolve solely to allowed addresses.
// The dial layer independently re-validates the chosen address.
func (g *Guard) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("dialpolicy: stopped after 10 redirects")
	}
	return g.assertURLAllowed(req)
}

func (g *Guard) assertURLAllowed(req *http.Request) error {
	switch req.URL.Scheme {
	case "http", "https":
	default:
		return &urlError{url: req.URL.String(), err: ErrForbidden}
	}
	host := req.URL.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		if !g.addrAllowed(addr.Unmap()) {
			return &urlError{url: req.URL.String(), err: ErrForbidden}
		}
		return nil
	}
	addrs, err := g.resolver.LookupIP(req.Context(), "ip", host)
	if err != nil {
		return &urlError{url: req.URL.String(), err: err}
	}
	for _, addr := range addrs {
		if !g.addrAllowed(addr.Unmap()) {
			return &urlError{url: req.URL.String(), err: ErrForbidden}
		}
	}
	return nil
}

// Transport returns a shared *http.Transport whose every connection is mediated
// by the Guard. The original request host name is preserved end-to-end (DNS is
// performed here instead of inside the dialer), so TLS ServerName/certificate
// verification still uses the host name rather than the resolved IP. Proxying
// is disabled to avoid routing traffic around the policy.
func (g *Guard) Transport() *http.Transport {
	if tr := g.transport.Load(); tr != nil {
		return tr
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           g.DialContext,
		TLSClientConfig:       g.tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       defaultIdleTimeout,
		TLSHandshakeTimeout:   defaultTLSTimeout,
		ExpectContinueTimeout: defaultExpectContinueTimeout,
	}
	if !g.transport.CompareAndSwap(nil, tr) {
		return g.transport.Load()
	}
	return tr
}

// Client returns an http.Client using the guarded transport with the standard
// ten-hop redirect limit, each hop validated by CheckRedirect.
func (g *Guard) Client() *http.Client {
	return &http.Client{
		Transport:     g.Transport(),
		CheckRedirect: g.CheckRedirect,
	}
}

type urlError struct {
	url string
	err error
}

func (e *urlError) Error() string { return "dialpolicy: " + e.url + ": " + e.err.Error() }
func (e *urlError) Unwrap() error { return e.err }
