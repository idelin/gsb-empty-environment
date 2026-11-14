package netsec

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// Forwarding header names understood or removed at ingress.
const (
	HeaderXForwardedFor = "X-Forwarded-For"
	HeaderForwarded     = "Forwarded"
	HeaderXRealIP       = "X-Real-IP"
)

// HeaderSource identifies where a resolved address came from.
type HeaderSource string

const (
	SourcePeer          HeaderSource = "peer"
	SourceXForwardedFor HeaderSource = "x-forwarded-for"
	SourceForwarded     HeaderSource = "forwarded"
	SourceXRealIP       HeaderSource = "x-real-ip"
)

const defaultMaxForwardedHops = 32

var forwardingHeaders = []string{
	HeaderXForwardedFor,
	HeaderForwarded,
	HeaderXRealIP,
	"X-Client-IP",
	"True-Client-IP",
	"CF-Connecting-IP",
	"Fastly-Client-IP",
	"X-Cluster-Client-IP",
	"X-Original-Forwarded-For",
	"Forwarded-For",
}

// Identity is the tuple used by Limiter. Address is always the normalized
// direct peer or the validated rightmost non-trusted forwarding hop.
type Identity struct {
	Address       string
	Account       string
	Device        string
	DirectAddress string
	Chain         []string
	TrustedPeer   bool
	ChainComplete bool
	Source        HeaderSource
	HeaderError   string
}

// Empty reports whether the identity has no address.
func (i Identity) Empty() bool {
	return i.Address == ""
}

// ResolverOption configures a Resolver.
type ResolverOption func(*Resolver)

// WithAccountExtractor replaces the default account extraction logic.
func WithAccountExtractor(fn func(*http.Request) string) ResolverOption {
	return func(r *Resolver) {
		if fn != nil {
			r.accountFn = fn
		}
	}
}

// WithDeviceExtractor replaces the default device extraction logic.
func WithDeviceExtractor(fn func(*http.Request) string) ResolverOption {
	return func(r *Resolver) {
		if fn != nil {
			r.deviceFn = fn
		}
	}
}

// WithMaxForwardedHops limits the accepted forwarding chain length.
func WithMaxForwardedHops(n int) ResolverOption {
	return func(r *Resolver) {
		if n > 0 {
			r.maxHops = n
		}
	}
}

// WithXRealIPFallback enables X-Real-IP as a fallback when neither
// X-Forwarded-For nor Forwarded is present. It is disabled by default because
// a single header is not a forwarding chain.
func WithXRealIPFallback() ResolverOption {
	return func(r *Resolver) {
		r.realIPFallback = true
	}
}

// Resolver resolves client identity from a request. It never caches a
// resolution, so replacing the trusted proxy set takes effect on the next
// request.
type Resolver struct {
	trusted        atomic.Pointer[TrustedProxies]
	maxHops        int
	accountFn      func(*http.Request) string
	deviceFn       func(*http.Request) string
	realIPFallback bool
}

// NewResolver creates a resolver backed by trusted. trusted may be nil, which
// means no peer is trusted.
func NewResolver(trusted *TrustedProxies, opts ...ResolverOption) *Resolver {
	r := &Resolver{
		maxHops:   defaultMaxForwardedHops,
		accountFn: defaultAccount,
		deviceFn:  defaultDevice,
	}
	r.trusted.Store(trusted)
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// TrustedProxies returns the currently installed proxy set.
func (r *Resolver) TrustedProxies() *TrustedProxies {
	if r == nil {
		return nil
	}
	return r.trusted.Load()
}

// SetTrustedProxies atomically changes the proxy set. Existing policy state is
// not rewritten; the next request is evaluated against the new proxy topology.
func (r *Resolver) SetTrustedProxies(trusted *TrustedProxies) {
	if r == nil {
		return
	}
	r.trusted.Store(trusted)
}

// ReloadTrustedProxies replaces the active proxy set. It creates a new set
// when the resolver was constructed without one.
func (r *Resolver) ReloadTrustedProxies(entries ...string) error {
	if r == nil {
		return fmt.Errorf("netsec: nil resolver")
	}
	trusted := r.trusted.Load()
	if trusted == nil {
		next, err := NewTrustedProxies(entries...)
		if err != nil {
			return err
		}
		r.trusted.Store(next)
		return nil
	}
	return trusted.Replace(entries...)
}

// ProxyRevision reports the revision of the active proxy set.
func (r *Resolver) ProxyRevision() uint64 {
	if r == nil {
		return 0
	}
	trusted := r.trusted.Load()
	return trusted.Revision()
}

// Resolve returns the client identity for req.
//
// A forwarding header is considered only when the direct peer is trusted. The
// chain must be syntactically complete and internally consistent; otherwise
// the direct peer becomes the client address.
func (r *Resolver) Resolve(req *http.Request) (Identity, error) {
	if req == nil {
		return Identity{}, fmt.Errorf("netsec: nil request")
	}
	identity, err := r.ResolveAddr(req.RemoteAddr, req.Header)
	if err != nil {
		return identity, err
	}
	accountFn := r.accountFn
	if accountFn == nil {
		accountFn = defaultAccount
	}
	deviceFn := r.deviceFn
	if deviceFn == nil {
		deviceFn = defaultDevice
	}
	identity.Account = cleanDimensionValue(accountFn(req))
	identity.Device = cleanDimensionValue(deviceFn(req))
	return identity, nil
}

// ResolveAddr resolves an identity from a raw RemoteAddr and headers.
func (r *Resolver) ResolveAddr(remoteAddr string, header http.Header) (Identity, error) {
	direct, err := parseIP(remoteAddr)
	if err != nil {
		return Identity{}, fmt.Errorf("netsec: invalid remote address %q: %w", remoteAddr, err)
	}

	identity := Identity{
		Address:       direct.String(),
		DirectAddress: direct.String(),
		Source:        SourcePeer,
	}
	trusted := r.TrustedProxies()
	if !trusted.Contains(direct) {
		return identity, nil
	}
	identity.TrustedPeer = true
	maxHops := r.maxHops
	if maxHops <= 0 {
		maxHops = defaultMaxForwardedHops
	}

	xffValues := headerValues(header, HeaderXForwardedFor)
	forwardedValues := headerValues(header, HeaderForwarded)

	var (
		xffChain       []net.IP
		xffOK          bool
		xffErr         error
		forwardedChain []net.IP
		forwardedOK    bool
		forwardedErr   error
	)
	if len(xffValues) > 0 {
		xffChain, xffErr = parseXForwardedFor(xffValues, maxHops)
		xffOK = xffErr == nil
	}
	if len(forwardedValues) > 0 {
		forwardedChain, forwardedErr = parseForwarded(forwardedValues, maxHops)
		forwardedOK = forwardedErr == nil
	}

	switch {
	case xffOK && forwardedOK:
		xffClient, xffFound := rightmostUntrusted(xffChain, trusted)
		forwardedClient, forwardedFound := rightmostUntrusted(forwardedChain, trusted)
		if xffFound && forwardedFound && xffClient.Equal(forwardedClient) {
			return identityWithChain(identity, xffChain, xffClient, SourceXForwardedFor), nil
		}
		identity.HeaderError = "conflicting or incomplete forwarding chains"
		return identity, nil
	case xffOK:
		client, found := rightmostUntrusted(xffChain, trusted)
		if !found {
			identity.HeaderError = "forwarding chain contains no untrusted hop"
			return identity, nil
		}
		return identityWithChain(identity, xffChain, client, SourceXForwardedFor), nil
	case forwardedOK:
		client, found := rightmostUntrusted(forwardedChain, trusted)
		if !found {
			identity.HeaderError = "forwarding chain contains no untrusted hop"
			return identity, nil
		}
		return identityWithChain(identity, forwardedChain, client, SourceForwarded), nil
	}

	if len(xffValues) > 0 || len(forwardedValues) > 0 {
		switch {
		case xffErr != nil:
			identity.HeaderError = xffErr.Error()
		case forwardedErr != nil:
			identity.HeaderError = forwardedErr.Error()
		default:
			identity.HeaderError = "invalid forwarding chain"
		}
		return identity, nil
	}

	realIPValues := headerValues(header, HeaderXRealIP)
	if !r.realIPFallback {
		identity.HeaderError = "forwarding chain missing"
		return identity, nil
	}
	if len(realIPValues) == 0 {
		identity.HeaderError = "forwarding chain missing"
		return identity, nil
	}
	if len(realIPValues) != 1 {
		identity.HeaderError = "multiple X-Real-IP values"
		return identity, nil
	}
	client, err := parseIP(realIPValues[0])
	if err != nil || trusted.Contains(client) {
		identity.HeaderError = "invalid X-Real-IP"
		return identity, nil
	}
	return identityWithChain(identity, []net.IP{client}, client, SourceXRealIP), nil
}

func identityWithChain(base Identity, chain []net.IP, client net.IP, source HeaderSource) Identity {
	addresses := make([]string, len(chain))
	for i, ip := range chain {
		addresses[i] = ip.String()
	}
	base.Address = client.String()
	base.Chain = addresses
	base.ChainComplete = true
	base.Source = source
	base.HeaderError = ""
	return base
}

func rightmostUntrusted(chain []net.IP, trusted *TrustedProxies) (net.IP, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		if !trusted.Contains(chain[i]) {
			return chain[i], true
		}
	}
	return nil, false
}

func headerValues(header http.Header, name string) []string {
	if header == nil {
		return nil
	}
	values := header.Values(name)
	if len(values) > 0 {
		return values
	}
	for key, candidate := range header {
		if strings.EqualFold(key, name) {
			return candidate
		}
	}
	return nil
}

// StripForwardingHeaders removes every client-controlled address header.
// Deletion is case-insensitive because callers may construct http.Header
// directly instead of using the canonicalizing Set/Add methods.
func StripForwardingHeaders(header http.Header) {
	for key := range header {
		for _, name := range forwardingHeaders {
			if strings.EqualFold(key, name) {
				delete(header, key)
				break
			}
		}
	}
}

// Sanitize resolves req and then strips all forwarding headers. Applications
// should use the returned identity, or IdentityFromContext when Middleware is
// used, instead of reading address headers.
func (r *Resolver) Sanitize(req *http.Request) (Identity, error) {
	identity, err := r.Resolve(req)
	if req != nil && req.Header != nil {
		StripForwardingHeaders(req.Header)
	}
	return identity, err
}

type identityContextKey struct{}

// WithIdentity stores id in ctx.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// IdentityFromContext retrieves an identity stored by Middleware or
// WithIdentity.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey{}).(Identity)
	return id, ok
}

// Middleware resolves the identity, removes client-supplied forwarding
// headers, and passes the request to next with the identity in its context.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		identity, err := r.Resolve(req)
		StripForwardingHeaders(req.Header)
		if err != nil {
			http.Error(w, "invalid client address", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, req.WithContext(WithIdentity(req.Context(), identity)))
	})
}

// ResolveClient is a convenience wrapper for one-off resolutions.
func ResolveClient(req *http.Request, trusted *TrustedProxies, opts ...ResolverOption) (Identity, error) {
	return NewResolver(trusted, opts...).Resolve(req)
}

// ClientIP resolves and returns just the client address.
func ClientIP(req *http.Request, trusted *TrustedProxies, opts ...ResolverOption) (string, error) {
	identity, err := ResolveClient(req, trusted, opts...)
	return identity.Address, err
}

// ClientIP resolves and returns just the client address.
func (r *Resolver) ClientIP(req *http.Request) (string, error) {
	identity, err := r.Resolve(req)
	return identity.Address, err
}

func defaultAccount(req *http.Request) string {
	for _, name := range []string{"X-Account-ID", "X-User-ID", "X-Account", "X-User", "X-Login", "X-Username"} {
		if value := strings.TrimSpace(req.Header.Get(name)); value != "" {
			return value
		}
	}
	if req.URL != nil {
		query := req.URL.Query()
		for _, name := range []string{"account", "username", "email"} {
			if value := strings.TrimSpace(query.Get(name)); value != "" {
				return value
			}
		}
	}
	for _, form := range []url.Values{req.PostForm, req.Form} {
		for _, name := range []string{"account", "username", "email"} {
			if value := strings.TrimSpace(form.Get(name)); value != "" {
				return value
			}
		}
	}
	return ""
}

func defaultDevice(req *http.Request) string {
	for _, name := range []string{"X-Device-ID", "X-Device-Fingerprint", "X-Client-ID", "X-Device", "Device-ID"} {
		if value := strings.TrimSpace(req.Header.Get(name)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(req.UserAgent())
}

func cleanDimensionValue(value string) string {
	value = strings.TrimSpace(value)
	const max = 512
	if len(value) > max {
		value = value[:max]
	}
	return value
}
