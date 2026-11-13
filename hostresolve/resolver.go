package hostresolve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Result is the single authority that routing, caching and authentication
// must share. Key is canonical and therefore safe to use directly as both a
// routing key and a cache key; never derive a second key from req.Host or a
// forwarding header.
type Result struct {
	// Key is the canonical host[:port] authority.
	Key string
	// Source records which input supplied Key.
	Source Source
	// Host is the host part of Key without the port.
	Host string
	// Port is the port part of Key, or "" when no port was supplied.
	Port string
}

// Resolver resolves request authorities. The zero value is usable and trusts
// no peer, so X-Forwarded-Host is always ignored until IsTrustedProxy is set.
type Resolver struct {
	// IsTrustedProxy reports whether the direct peer IP (taken from
	// http.Request.RemoteAddr) is allowed to set X-Forwarded-Host. It is
	// called at most once per resolution. A nil function trusts nobody.
	IsTrustedProxy func(peerIP net.IP) bool
}

// Request is the transport-level input needed for resolution. Build it from
// an *http.Request with [Resolver.ResolveRequest], or construct it directly in
// tests and custom frontends.
type Request struct {
	// RequestTarget is the raw request target from the HTTP request line
	// (http.Request.RequestURI on the server side).
	RequestTarget string
	// HostHeader holds every raw Host field line received on the wire.
	HostHeader []string
	// ForwardedHost holds every raw X-Forwarded-Host field line.
	ForwardedHost []string
	// RemoteAddr is the "host:port" of the direct TCP peer.
	RemoteAddr string
}

type resultContextKey struct{}

// Resolve applies the RFC 9112 section 3.2.2 authority rules.
func (r *Resolver) Resolve(in *Request) (*Result, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInvalidRequestTarget)
	}

	hostKey, source, err := r.resolveAuthority(in)
	if err != nil {
		return nil, err
	}

	if r.trusts(in.RemoteAddr) {
		forwarded, err := parseForwardedHost(in.ForwardedHost)
		if err != nil {
			return nil, err
		}
		if forwarded != "" {
			hostKey = forwarded
			source = SourceForwardedHost
		}
	}

	host, port := splitKey(hostKey)
	return &Result{Key: hostKey, Source: source, Host: host, Port: port}, nil
}

// resolveAuthority picks between the absolute-form request target authority
// and the Host header. The Host header is structurally validated in every
// case, but its value is ignored for routing once an absolute-form target is
// present.
func (r *Resolver) resolveAuthority(in *Request) (string, Source, error) {
	hostKey, err := singleHostValue(in.HostHeader)
	if err != nil {
		return "", SourceUnknown, err
	}

	target := strings.TrimSpace(in.RequestTarget)
	switch {
	case target == "":
		// Server requests always carry a request target; an empty one means
		// the caller lost the request line, which is not a routable request.
		return "", SourceUnknown, fmt.Errorf("%w: empty request target", ErrInvalidRequestTarget)
	case target == "*":
		// asterisk-form (OPTIONS *): no per-resource authority to route.
		return "", SourceUnknown, fmt.Errorf("%w: asterisk-form request target is not routable", ErrInvalidRequestTarget)
	case strings.HasPrefix(target, "/"):
		if err := validateOriginForm(target); err != nil {
			return "", SourceUnknown, err
		}
		return hostKey, SourceHost, nil
	default:
		key, err := parseAbsoluteForm(target)
		if err != nil {
			return "", SourceUnknown, err
		}
		// Absolute form wins; the validated Host header is deliberately ignored.
		return key, SourceRequestTarget, nil
	}
}

// singleHostValue enforces exactly one Host field line carrying exactly one
// non-empty authority. Multiple field lines and comma-separated lists are
// both treated as duplicated Host headers.
func singleHostValue(lines []string) (string, error) {
	if len(lines) == 0 {
		return "", ErrMissingHost
	}
	if len(lines) > 1 {
		return "", fmt.Errorf("%w: %d field lines", ErrDuplicateHost, len(lines))
	}
	raw := lines[0]
	if raw == "" {
		return "", fmt.Errorf("%w: empty field value", ErrMissingHost)
	}
	if strings.Contains(raw, ",") {
		return "", fmt.Errorf("%w: comma-separated value %q", ErrDuplicateHost, raw)
	}
	key, err := parseAuthority(raw)
	if err != nil {
		return "", err
	}
	return key, nil
}

// validateOriginForm rejects origin-form targets containing whitespace,
// control characters or a fragment component. A fragment is forbidden on the
// wire and would otherwise create key/target ambiguity downstream.
func validateOriginForm(target string) error {
	for _, r := range target {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("%w: whitespace or control character in request target %q", ErrInvalidRequestTarget, target)
		}
	}
	if strings.Contains(target, "#") {
		return fmt.Errorf("%w: fragment in request target %q", ErrInvalidRequestTarget, target)
	}
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequestTarget, err)
	}
	if u.Scheme != "" || u.Host != "" {
		return fmt.Errorf("%w: origin-form target must be path-absolute: %q", ErrInvalidRequestTarget, target)
	}
	return nil
}

// parseAbsoluteForm parses an absolute-form request target and returns the
// canonical key of its authority. CONNECT authority-form targets have no
// scheme and are rejected as unsupported for origin routing.
func parseAbsoluteForm(target string) (string, error) {
	for _, r := range target {
		if r <= 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: whitespace or control character in request target %q", ErrInvalidRequestTarget, target)
		}
	}
	if strings.Contains(target, "#") {
		return "", fmt.Errorf("%w: fragment in request target %q", ErrInvalidRequestTarget, target)
	}

	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequestTarget, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme %q in absolute-form target", ErrInvalidRequestTarget, u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: userinfo not allowed in request target authority", ErrInvalidRequestTarget)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: absolute-form target without authority: %q", ErrInvalidRequestTarget, target)
	}
	key, err := parseAuthority(u.Host)
	if err != nil {
		return "", err
	}
	return key, nil
}

// parseForwardedHost applies the same single-authority rules to
// X-Forwarded-Host. Multiple field lines / comma lists are rejected rather
// than guessed at: a trusted proxy that forwards a chain must normalize the
// chain before reaching this entry point.
func parseForwardedHost(lines []string) (string, error) {
	if len(lines) == 0 {
		return "", nil
	}
	if len(lines) > 1 {
		return "", fmt.Errorf("%w: multiple X-Forwarded-Host field lines", ErrInvalidHost)
	}
	raw := lines[0]
	if raw == "" {
		return "", fmt.Errorf("%w: empty X-Forwarded-Host field value", ErrInvalidHost)
	}
	if strings.Contains(raw, ",") {
		return "", fmt.Errorf("%w: comma-separated X-Forwarded-Host value %q", ErrInvalidHost, raw)
	}
	return parseAuthority(raw)
}

// trusts reports whether the direct peer is a trusted proxy.
func (r *Resolver) trusts(remoteAddr string) bool {
	if r == nil || r.IsTrustedProxy == nil {
		return false
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return r.IsTrustedProxy(ip)
}

// splitKey splits a canonical host[:port] key produced by parseAuthority.
func splitKey(key string) (host, port string) {
	if h, p, err := net.SplitHostPort(key); err == nil {
		return h, p
	}
	return key, ""
}

// ResolveRequest adapts a server-side *http.Request. It reads the raw Host
// and X-Forwarded-Host field lines from the wire (http.Header preserves
// duplicate lines) and the request target from RequestURI.
func (r *Resolver) ResolveRequest(req *http.Request) (*Result, error) {
	in := &Request{
		RequestTarget: req.RequestURI,
		HostHeader:    req.Header[http.CanonicalHeaderKey("Host")],
		ForwardedHost: req.Header[http.CanonicalHeaderKey("X-Forwarded-Host")],
		RemoteAddr:    req.RemoteAddr,
	}
	// net/http deletes Host from Header after populating req.Host, so rebuild
	// the single field line when the header map no longer carries it.
	if len(in.HostHeader) == 0 && req.Host != "" {
		in.HostHeader = []string{req.Host}
	}
	return r.Resolve(in)
}

// Middleware returns an http.Handler wrapper that resolves the authority,
// stores the [Result] in the request context and rejects malformed requests
// with 400 Bad Request before the inner handler runs.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	if r == nil {
		r = &Resolver{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		result, err := r.ResolveRequest(req)
		if err != nil {
			http.Error(w, "Bad Request: invalid host authority", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, req.WithContext(WithResult(req.Context(), result)))
	})
}

// WithResult stores a resolution result in ctx.
func WithResult(ctx context.Context, result *Result) context.Context {
	return context.WithValue(ctx, resultContextKey{}, result)
}

// FromContext returns the resolution result stored by [Resolver.Middleware],
// or nil when the request did not pass through the middleware.
func FromContext(ctx context.Context) *Result {
	result, _ := ctx.Value(resultContextKey{}).(*Result)
	return result
}

// RouteKey and CacheKey return the same canonical key. Both accessors exist
// so call sites document which concern they serve while being guaranteed to
// agree; a disagreement between routing and cache keys is the exact
// confusion that enables cross-tenant cache poisoning.
func (res *Result) RouteKey() string { return res.Key }

// CacheKey returns the same value as [Result.RouteKey].
func (res *Result) CacheKey() string { return res.Key }
