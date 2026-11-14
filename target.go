package netsec

import (
	"context"
	"crypto/tls"
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
	"unicode/utf8"
)

// LookupIPFunc resolves an ASCII host name. Implementations must not apply
// any additional name mapping; they receive the canonical host that will be
// used in the HTTP request.
type LookupIPFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// DialContextFunc has the same contract as net.Dialer.DialContext. The
// address passed by Validator is always the pinned, validated IP address
// rather than the host name from the URL.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Config configures a Validator.
//
// By default only publicly routable unicast addresses and the scheme's
// standard port (80 for HTTP, 443 for HTTPS) are accepted. Allowlists are an
// explicit operator trust decision; they do not relax parsing, scheme,
// userinfo, or fragment checks. Ports are relaxed only by AllowedPorts,
// AllowNonStandardPorts, or a bare AllowedHosts entry.
type Config struct {
	// Resolver is used for DNS lookups when LookupIP is nil. The zero value
	// uses net.DefaultResolver.
	Resolver *net.Resolver
	// LookupIP overrides Resolver. It is primarily useful for tests and for
	// environments with a controlled resolver.
	LookupIP LookupIPFunc
	// DialContext overrides the underlying dialer. The address is still the
	// validated, pinned IP; this hook must not resolve the host again.
	DialContext DialContextFunc
	// Dialer is used when DialContext is nil. Its timeout is left unchanged;
	// when Dialer is also nil, DialTimeout (default 10s) is used.
	Dialer *net.Dialer
	// DialTimeout bounds connection establishment. The default is 10 seconds.
	DialTimeout time.Duration
	// TLSClientConfig is cloned for the internal HTTP transport.
	TLSClientConfig *tls.Config
	// ClientTimeout is assigned to clients returned by Client.
	ClientTimeout time.Duration

	// AllowedHosts lists exact canonical host names that may resolve to
	// otherwise blocked address space (for example localhost). A bare host
	// entry permits any HTTP(S) port for that host; a host:port entry permits
	// only that port. The name is still resolved once and the resulting IP is
	// pinned.
	AllowedHosts []string
	// AllowedCIDRs lists address ranges that are explicitly permitted.
	AllowedCIDRs []string
	// AllowedPrefixes lists parsed address ranges that are explicitly
	// permitted, in addition to AllowedCIDRs.
	AllowedPrefixes []netip.Prefix
	// AllowedPorts lists non-default ports that are explicitly permitted.
	AllowedPorts []int
	// AllowNonStandardPorts permits any valid non-zero port for HTTP(S).
	AllowNonStandardPorts bool

	// AllowPrivate permits private address space (RFC 1918, IPv6 ULA, and
	// CGNAT), but not loopback or link-local space.
	AllowPrivate bool
	// AllowLoopback permits loopback address space.
	AllowLoopback bool
	// AllowLinkLocal permits link-local address space.
	AllowLinkLocal bool

	// MaxRedirects limits redirects. Zero means the default of 10; a
	// negative value disables redirects.
	MaxRedirects int
	// AllowConnectionReuse permits keep-alive reuse. It is off by default so
	// that every request dials exactly the address resolved and validated for
	// that request.
	AllowConnectionReuse bool
}

// Target is the result of validating one request hop.
type Target struct {
	// URL is a canonical clone of the parsed URL. It has an ASCII host, no
	// userinfo, no fragment, and no opaque component.
	URL *url.URL
	// Scheme is "http" or "https".
	Scheme string
	// Host is the canonical ASCII host name or canonical numeric IP.
	Host string
	// Port is the validated destination port.
	Port uint16
	// IPs holds every address returned by the resolver and accepted by the
	// address policy. The set is deduplicated.
	IPs []netip.Addr
	// IP is the primary pinned address. It is always the first element of IPs.
	IP netip.Addr

	canonical  string
	requestURL *url.URL
	dialHost   string
	dialPort   uint16
	dialIPs    []netip.Addr
	owner      *Validator
}

// Address returns the primary pinned host:port dial address.
func (t *Target) Address() string {
	ip := t.IP
	if len(t.dialIPs) > 0 {
		ip = t.dialIPs[0]
	}
	port := t.Port
	if t.dialPort != 0 {
		port = t.dialPort
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))
}

// AllowsAddress reports whether addr is one of the validated addresses.
func (t *Target) AllowsAddress(addr netip.Addr) bool {
	addr, err := normalizeAddr(addr)
	if err != nil {
		return false
	}
	for _, candidate := range t.dialIPs {
		if candidate == addr {
			return true
		}
	}
	return false
}

// Validator parses, resolves, and pins outbound HTTP(S) targets. It also
// implements http.RoundTripper, so each redirect hop is validated again.
type Validator struct {
	cfg            Config
	lookup         LookupIPFunc
	dial           DialContextFunc
	allowedHosts   map[string]struct{}
	anyPortHosts   map[string]struct{}
	allowedPrefixs []netip.Prefix
	allowedPorts   map[uint16]struct{}
	maxRedirects   int
	transport      *http.Transport
}

// NewValidator validates configuration and returns a reusable Validator.
func NewValidator(cfg Config) (*Validator, error) {
	v := &Validator{
		cfg:          cfg,
		allowedHosts: make(map[string]struct{}),
		anyPortHosts: make(map[string]struct{}),
		allowedPorts: make(map[uint16]struct{}),
		maxRedirects: 10,
	}
	if cfg.MaxRedirects < 0 {
		v.maxRedirects = 0
	} else if cfg.MaxRedirects > 0 {
		v.maxRedirects = cfg.MaxRedirects
	}

	for _, host := range cfg.AllowedHosts {
		hostPart, portPart, hasPort, splitErr := splitAuthority(host)
		if splitErr != nil {
			hostPart, hasPort = host, false
		}
		normalized, err := NormalizeHost(hostPart)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid allowed host %q: %w", host, err)
		}
		v.allowedHosts[normalized] = struct{}{}
		if hasPort {
			port, err := parsePort(portPart)
			if err != nil {
				return nil, fmt.Errorf("netsec: invalid allowed host port %q: %w", host, err)
			}
			v.allowedPorts[port] = struct{}{}
		} else {
			v.anyPortHosts[normalized] = struct{}{}
		}
	}
	for _, cidr := range cfg.AllowedCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid allowed CIDR %q: %w", cidr, err)
		}
		v.allowedPrefixs = append(v.allowedPrefixs, prefix.Masked())
	}
	for _, prefix := range cfg.AllowedPrefixes {
		if !prefix.IsValid() {
			return nil, fmt.Errorf("netsec: invalid allowed prefix %v", prefix)
		}
		v.allowedPrefixs = append(v.allowedPrefixs, prefix.Masked())
	}
	for _, port := range cfg.AllowedPorts {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("netsec: invalid allowed port %d", port)
		}
		v.allowedPorts[uint16(port)] = struct{}{}
	}

	if cfg.LookupIP != nil {
		v.lookup = cfg.LookupIP
	} else {
		resolver := cfg.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		v.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return resolver.LookupNetIP(ctx, "ip", host)
		}
	}

	if cfg.DialContext != nil {
		v.dial = cfg.DialContext
	} else {
		dialer := cfg.Dialer
		if dialer == nil {
			dialer = &net.Dialer{Timeout: cfg.DialTimeout}
			if dialer.Timeout <= 0 {
				dialer.Timeout = 10 * time.Second
			}
		}
		v.dial = dialer.DialContext
	}

	tlsConfig := (*tls.Config)(nil)
	if cfg.TLSClientConfig != nil {
		tlsConfig = cfg.TLSClientConfig.Clone()
	}
	v.transport = &http.Transport{
		Proxy:               nil,
		DialContext:         v.dialPinned,
		TLSClientConfig:     tlsConfig,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   !cfg.AllowConnectionReuse,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
	}
	return v, nil
}

// New is an alias for NewValidator.
func New(cfg Config) (*Validator, error) {
	return NewValidator(cfg)
}

// MustNewValidator is like NewValidator but panics on invalid configuration.
func MustNewValidator(cfg Config) *Validator {
	v, err := NewValidator(cfg)
	if err != nil {
		panic(err)
	}
	return v
}

// MustNew is an alias for MustNewValidator.
func MustNew(cfg Config) *Validator {
	return MustNewValidator(cfg)
}

// Validate parses rawURL exactly once with net/url and validates the target.
func (v *Validator) Validate(rawURL string) (*Target, error) {
	return v.ValidateContext(context.Background(), rawURL)
}

// Check validates rawURL and returns only the error.
func (v *Validator) Check(rawURL string) error {
	_, err := v.Validate(rawURL)
	return err
}

// ValidateTarget is an alias for Validate.
func (v *Validator) ValidateTarget(rawURL string) (*Target, error) {
	return v.Validate(rawURL)
}

// ValidateTargetURL is an alias for Validate.
func (v *Validator) ValidateTargetURL(rawURL string) (*Target, error) {
	return v.Validate(rawURL)
}

// ParseTarget is an alias for Validate.
func (v *Validator) ParseTarget(rawURL string) (*Target, error) {
	return v.Validate(rawURL)
}

// ValidateContext is Validate with a context used for DNS resolution.
func (v *Validator) ValidateContext(ctx context.Context, rawURL string) (*Target, error) {
	if err := checkRawURL(rawURL); err != nil {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, validationError(fmt.Errorf("%w: %v", ErrInvalidURL, err), redactRawURL(rawURL), "", "")
	}
	return v.ValidateURLContext(ctx, u)
}

// ValidateURL validates an already parsed URL. It never reparses the URL
// string, which avoids parser differentials between validation and request
// execution.
func (v *Validator) ValidateURL(u *url.URL) (*Target, error) {
	return v.ValidateURLContext(context.Background(), u)
}

// ValidateURLContext is ValidateURL with a context used for DNS resolution.
func (v *Validator) ValidateURLContext(ctx context.Context, u *url.URL) (*Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	inspected, err := inspectURL(u)
	if err != nil {
		return nil, err
	}
	if err := v.checkPort(inspected.host, inspected.port, inspected.scheme); err != nil {
		return nil, validationError(err, inspected.u.String(), inspected.host, "")
	}

	var addrs []netip.Addr
	if inspected.ip.IsValid() {
		addrs = []netip.Addr{inspected.ip}
	} else {
		addrs, err = v.resolve(ctx, inspected.host)
		if err != nil {
			return nil, err
		}
	}

	unique := make([]netip.Addr, 0, len(addrs))
	seen := make(map[netip.Addr]struct{}, len(addrs))
	for _, rawAddr := range addrs {
		addr, err := normalizeAddr(rawAddr)
		if err != nil {
			return nil, validationError(err, inspected.u.String(), inspected.host, "")
		}
		if err := v.checkAddress(inspected.host, addr); err != nil {
			return nil, err
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		unique = append(unique, addr)
	}
	if len(unique) == 0 {
		return nil, validationError(ErrNoAddress, inspected.u.String(), inspected.host, "")
	}

	canonicalURL := canonicalizeURL(inspected.u, inspected.host, inspected.port, inspected.explicit)
	dialIPs := append([]netip.Addr(nil), unique...)
	return &Target{
		URL:        canonicalURL,
		Scheme:     inspected.scheme,
		Host:       inspected.host,
		Port:       inspected.port,
		IPs:        unique,
		IP:         unique[0],
		canonical:  canonicalURL.String(),
		requestURL: cloneURL(canonicalURL),
		dialHost:   inspected.host,
		dialPort:   inspected.port,
		dialIPs:    dialIPs,
		owner:      v,
	}, nil
}

func (v *Validator) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := v.lookup(ctx, host)
	if err != nil {
		return nil, validationError(fmt.Errorf("%w: %w", ErrDNS, err), "http://"+host+"/", host, "")
	}
	if len(addrs) == 0 {
		return nil, validationError(ErrNoAddress, "http://"+host+"/", host, "")
	}
	return addrs, nil
}

func (v *Validator) checkAddress(host string, addr netip.Addr) error {
	if !addr.IsValid() || addr.Zone() != "" {
		return validationError(ErrBlockedAddress, "http://"+host+"/", host, "")
	}
	for _, prefix := range v.allowedPrefixs {
		if prefix.Contains(addr) {
			return nil
		}
	}
	class := classifyAddress(addr)
	if _, ok := v.allowedHosts[host]; ok {
		// AllowedHosts is an explicit trust decision. Even reserved ranges
		// are permitted, but the address is still pinned for the dial.
		return nil
	}
	switch {
	case class.blocked:
		return validationError(ErrBlockedAddress, "http://"+host+"/", host, addr.String())
	case class.loopback && !v.cfg.AllowLoopback:
		return validationError(ErrLoopbackAddress, "http://"+host+"/", host, addr.String())
	case class.linkLoc && !v.cfg.AllowLinkLocal:
		return validationError(ErrLinkLocalAddress, "http://"+host+"/", host, addr.String())
	case class.private && !v.cfg.AllowPrivate:
		return validationError(ErrPrivateAddress, "http://"+host+"/", host, addr.String())
	}
	return nil
}

func (v *Validator) checkPort(host string, port uint16, scheme string) error {
	defaultPort := uint16(80)
	if scheme == "https" {
		defaultPort = 443
	}
	if port != defaultPort {
		if v.cfg.AllowNonStandardPorts {
			return nil
		}
		if _, ok := v.anyPortHosts[host]; ok {
			return nil
		}
		if _, ok := v.allowedPorts[port]; !ok {
			return ErrPort
		}
	}
	return nil
}

type inspectedTarget struct {
	u        *url.URL
	scheme   string
	host     string
	port     uint16
	explicit bool
	ip       netip.Addr
}

func inspectURL(u *url.URL) (*inspectedTarget, error) {
	if u == nil {
		return nil, validationError(ErrInvalidURL, "", "", "")
	}
	if u.User != nil {
		return nil, validationError(ErrUserInfo, u.Redacted(), "", "")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return nil, validationError(ErrFragment, u.Redacted(), "", "")
	}
	if u.Opaque != "" {
		return nil, validationError(ErrInvalidURL, u.Redacted(), "", "")
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, validationError(ErrScheme, u.Redacted(), "", "")
	}
	if u.Host == "" {
		return nil, validationError(ErrHost, u.Redacted(), "", "")
	}
	if strings.ContainsAny(u.Host, "\\?#@") {
		return nil, validationError(ErrHost, u.Redacted(), "", "")
	}

	hostPart, portPart, hasPort, err := splitAuthority(u.Host)
	if err != nil {
		return nil, validationError(fmt.Errorf("%w: %v", ErrHost, err), u.Redacted(), "", "")
	}
	host, ip, isIP, err := normalizeHost(hostPart)
	if err != nil {
		return nil, validationError(err, u.Redacted(), hostPart, "")
	}

	defaultPort := uint16(80)
	if scheme == "https" {
		defaultPort = 443
	}
	port := defaultPort
	if hasPort {
		port, err = parsePort(portPart)
		if err != nil {
			return nil, validationError(fmt.Errorf("%w: %v", ErrPort, err), u.Redacted(), host, "")
		}
	}

	clone := *u
	clone.Scheme = scheme
	clone.User = nil
	clone.Fragment = ""
	clone.RawFragment = ""
	clone.Opaque = ""

	return &inspectedTarget{
		u:        &clone,
		scheme:   scheme,
		host:     host,
		port:     port,
		explicit: hasPort,
		ip:       ipIfValid(isIP, ip),
	}, nil
}

func ipIfValid(isIP bool, ip netip.Addr) netip.Addr {
	if !isIP {
		return netip.Addr{}
	}
	return ip
}

func splitAuthority(authority string) (host, port string, hasPort bool, err error) {
	if authority == "" {
		return "", "", false, errors.New("empty authority")
	}
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "", "", false, errors.New("missing closing bracket in IPv6 literal")
		}
		host = authority[1:end]
		rest := authority[end+1:]
		switch {
		case rest == "":
			return host, "", false, nil
		case strings.HasPrefix(rest, ":"):
			if len(rest) == 1 {
				return "", "", false, errors.New("empty port")
			}
			return host, rest[1:], true, nil
		default:
			return "", "", false, errors.New("invalid characters after IPv6 literal")
		}
	}
	if strings.ContainsAny(authority, "[]") {
		return "", "", false, errors.New("unexpected bracket in host")
	}
	if strings.Count(authority, ":") > 1 {
		return "", "", false, errors.New("unbracketed IPv6 literal")
	}
	if i := strings.IndexByte(authority, ':'); i >= 0 {
		if i == len(authority)-1 {
			return "", "", false, errors.New("empty port")
		}
		return authority[:i], authority[i+1:], true, nil
	}
	return authority, "", false, nil
}

func parsePort(port string) (uint16, error) {
	if port == "" {
		return 0, errors.New("empty port")
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return 0, fmt.Errorf("invalid port %q", port)
		}
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", port)
	}
	if value == 0 {
		return 0, errors.New("port zero is not allowed")
	}
	return uint16(value), nil
}

func canonicalizeURL(u *url.URL, host string, port uint16, explicit bool) *url.URL {
	clone := *u
	clone.Scheme = strings.ToLower(u.Scheme)
	clone.User = nil
	clone.Fragment = ""
	clone.RawFragment = ""
	clone.Opaque = ""
	if strings.Contains(host, ":") {
		if explicit {
			clone.Host = net.JoinHostPort(host, strconv.Itoa(int(port)))
		} else {
			clone.Host = "[" + host + "]"
		}
	} else if explicit {
		clone.Host = net.JoinHostPort(host, strconv.Itoa(int(port)))
	} else {
		clone.Host = host
	}
	return &clone
}

func checkRawURL(rawURL string) error {
	if rawURL == "" {
		return validationError(ErrInvalidURL, "", "", "")
	}
	if strings.TrimSpace(rawURL) != rawURL {
		return validationError(fmt.Errorf("%w: leading or trailing whitespace", ErrInvalidURL), "<redacted>", "", "")
	}
	if !utf8.ValidString(rawURL) {
		return validationError(fmt.Errorf("%w: invalid UTF-8", ErrInvalidURL), "<redacted>", "", "")
	}
	for _, r := range rawURL {
		if r == '\r' || r == '\n' || r == '\x00' || r < 0x20 || r == 0x7f {
			return validationError(fmt.Errorf("%w: control character", ErrInvalidURL), "<redacted>", "", "")
		}
	}
	if strings.Contains(rawURL, "\\") {
		return validationError(fmt.Errorf("%w: backslash in URL", ErrInvalidURL), redactRawURL(rawURL), "", "")
	}
	if strings.Contains(rawURL, "#") {
		return validationError(ErrFragment, redactRawURL(rawURL), "", "")
	}
	return nil
}

func redactRawURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<redacted>"
	}
	return u.Redacted()
}

// NewRequest parses rawURL once and returns both the request and the
// validated target. The returned request is already pinned to the target's
// addresses; use a client returned by Client (or Validator itself as the
// RoundTripper) to preserve that pinning.
func (v *Validator) NewRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, *Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target, err := v.ValidateContext(ctx, rawURL)
	if err != nil {
		return nil, nil, err
	}
	req := newRequestWithURL(ctx, method, target.URL, body)
	req = req.WithContext(withTarget(req.Context(), target))
	return req, target, nil
}

// NewRequestForTarget builds a pinned request from a previously validated
// Target without parsing or resolving the URL again.
func (v *Validator) NewRequestForTarget(ctx context.Context, method string, target *Target, body io.Reader) (*http.Request, error) {
	return target.NewRequest(ctx, method, body)
}

// NewRequest builds a pinned request from an already validated Target. It
// does not parse or resolve the URL again.
func (t *Target) NewRequest(ctx context.Context, method string, body io.Reader) (*http.Request, error) {
	if t == nil || t.requestURL == nil || t.canonical == "" {
		return nil, ErrInvalidURL
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req := newRequestWithURL(ctx, method, t.requestURL, body)
	return req.WithContext(withTarget(req.Context(), t)), nil
}

// newRequestWithURL constructs a request around an already parsed URL. Unlike
// http.NewRequest, it never parses a URL string again.
func newRequestWithURL(ctx context.Context, method string, u *url.URL, body io.Reader) *http.Request {
	if method == "" {
		method = http.MethodGet
	}
	req := &http.Request{
		Method:     method,
		URL:        cloneURL(u),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Host:       u.Host,
	}
	req = req.WithContext(ctx)
	if body != nil {
		switch v := body.(type) {
		case io.ReadCloser:
			req.Body = v
		default:
			req.Body = io.NopCloser(body)
		}
		req.ContentLength = -1
		switch v := body.(type) {
		case interface{ Len() int }:
			req.ContentLength = int64(v.Len())
		}
	}
	return req
}

func cloneURL(u *url.URL) *url.URL {
	clone := *u
	return &clone
}

type targetContextKey struct{}

func withTarget(ctx context.Context, target *Target) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, targetContextKey{}, target)
}

func targetFromContext(ctx context.Context) *Target {
	if ctx == nil {
		return nil
	}
	target, _ := ctx.Value(targetContextKey{}).(*Target)
	return target
}

func (v *Validator) hostHeaderMatches(target *Target, hostHeader string) bool {
	if hostHeader == "" {
		return true
	}
	hostPart, portPart, hasPort, err := splitAuthority(hostHeader)
	if err != nil {
		return false
	}
	host, _, _, err := normalizeHost(hostPart)
	if err != nil || host != target.dialHost {
		return false
	}
	if hasPort {
		port, err := parsePort(portPart)
		if err != nil || port != target.dialPort {
			return false
		}
	}
	return true
}

func (v *Validator) dialPinned(ctx context.Context, network, address string) (net.Conn, error) {
	target := targetFromContext(ctx)
	if target == nil || len(target.dialIPs) == 0 || !target.dialIPs[0].IsValid() {
		return nil, ErrUnpinnedDial
	}

	hostPart, portPart, _, err := splitAuthority(address)
	if err != nil {
		return nil, validationError(fmt.Errorf("%w: %v", ErrTargetMismatch, err), address, "", "")
	}
	port, err := parsePort(portPart)
	if err != nil || port != target.dialPort {
		return nil, validationError(ErrTargetMismatch, address, target.dialHost, target.Address())
	}
	host, _, _, err := normalizeHost(hostPart)
	if err != nil || host != target.dialHost {
		return nil, validationError(ErrTargetMismatch, address, target.dialHost, target.Address())
	}

	var lastErr error
	for _, addr := range target.dialIPs {
		if network == "tcp4" && !addr.Is4() {
			continue
		}
		if network == "tcp6" && !addr.Is6() {
			continue
		}
		conn, err := v.dial(ctx, network, net.JoinHostPort(addr.String(), strconv.Itoa(int(target.dialPort))))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no validated address could be dialed")
	}
	return nil, fmt.Errorf("%w: %w", ErrDial, lastErr)
}
