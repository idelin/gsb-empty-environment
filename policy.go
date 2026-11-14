package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidTarget means the URL or policy configuration is not valid.
	ErrInvalidTarget = errors.New("netsec: invalid target")
	// ErrBlockedTarget means the destination was not in the allowlist.
	ErrBlockedTarget = errors.New("netsec: target is not allowed")
	// ErrBlockedAddress means the target resolved to a forbidden address.
	ErrBlockedAddress = errors.New("netsec: forbidden network address")
	// ErrHostOverride means the request attempted to set its own Host header.
	ErrHostOverride = errors.New("netsec: Host header override is not allowed")
	// ErrProxyBypass means a proxy or CONNECT-style bypass was attempted.
	ErrProxyBypass = errors.New("netsec: proxy bypass is not allowed")
)

// Target is the canonical destination used by the policy and credential
// provider.
type Target struct {
	Scheme string
	Host   string
	Port   int
}

// Addr returns the target as a dialable host:port string.
func (t Target) Addr() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// Config configures a Policy. At least one allowed domain and one allowed port
// are required. If no port is supplied, port 443 is used.
type Config struct {
	// AllowedDomains contains exact domains, leading-dot suffixes
	// (".example.com"), or wildcard suffixes ("*.example.com"). An
	// explicitly listed IP literal is also accepted; metadata and link-local
	// literals are still rejected. Entries may include a port
	// ("api.example.com:443"); otherwise AllowedPorts applies.
	AllowedDomains []string
	// AllowedHosts is an alias for AllowedDomains.
	AllowedHosts []string
	// AllowedPorts is the port allowlist for every allowed destination.
	AllowedPorts []int
	// Ports is an alias for AllowedPorts.
	Ports []int
	// AllowedSchemes defaults to http and https.
	AllowedSchemes []string

	// Credentials are added only after the destination has passed all policy
	// checks. They override same-named headers supplied on the request.
	Credentials http.Header
	// CredentialHeaders is an alias for Credentials.
	CredentialHeaders http.Header
	// Authorization is a convenience credential header.
	Authorization string
	// BearerToken adds "Authorization: Bearer <token>".
	BearerToken string
	// CredentialProvider can return credentials for a validated target. If it
	// returns false, no credentials are injected.
	CredentialProvider func(Target) (http.Header, bool)
	// CredentialsFor is an alias for CredentialProvider.
	CredentialsFor func(Target) (http.Header, bool)

	// LookupIP overrides DNS resolution. It is useful in tests and for
	// services with their own resolver. Every returned address is validated.
	LookupIP func(context.Context, string) ([]net.IP, error)
	// Resolver is used when LookupIP is nil.
	Resolver *net.Resolver

	Timeout     time.Duration
	DialTimeout time.Duration
	TLSConfig   *tls.Config

	// AllowLoopback permits hostnames that resolve to loopback addresses.
	// Explicitly allowlisted loopback IP literals and localhost names are
	// permitted without this flag. Metadata and link-local addresses remain
	// blocked.
	AllowLoopback bool
	// AllowPrivate permits hostnames that resolve to RFC1918 or IPv6 ULA
	// addresses. Explicitly allowlisted private IP literals are permitted
	// without this flag.
	AllowPrivate bool
	// AllowPrivateNetworks is an alias that enables both AllowLoopback and
	// AllowPrivate.
	AllowPrivateNetworks bool
	// AllowLocalNetworks is an alias that enables both AllowLoopback and
	// AllowPrivate.
	AllowLocalNetworks bool
}

// PolicyConfig is an alias for Config.
type PolicyConfig = Config

// OutboundPolicyConfig is an alias for Config.
type OutboundPolicyConfig = Config

type hostPattern struct {
	exact    bool
	apex     bool
	host     string
	suffix   string
	ip       netip.Addr
	isIP     bool
	original string
}

type Policy struct {
	patterns      []hostPattern
	ports         map[int]struct{}
	schemes       map[string]struct{}
	credentials   http.Header
	provider      func(Target) (http.Header, bool)
	lookupIP      func(context.Context, string) ([]net.IP, error)
	allowLoopback bool
	allowPrivate  bool
	timeout       time.Duration
	dialTimeout   time.Duration
	dial          func(context.Context, string, string) (net.Conn, error)
	base          *http.Transport
	client        *http.Client
}

// OutboundPolicy is an alias for Policy.
type OutboundPolicy = Policy

// New constructs a Policy. It accepts Config, *Config, AllowedDomains followed
// by AllowedPorts ([]string, []int), or a single []string (port 443).
func New(args ...any) (*Policy, error) {
	cfg, err := configFromArgs(args)
	if err != nil {
		return nil, err
	}
	return newPolicy(cfg)
}

// NewPolicy is equivalent to New.
func NewPolicy(args ...any) (*Policy, error) {
	return New(args...)
}

// NewOutboundPolicy is equivalent to New.
func NewOutboundPolicy(args ...any) (*Policy, error) {
	return New(args...)
}

// MustNew is New that panics on invalid configuration.
func MustNew(args ...any) *Policy {
	p, err := New(args...)
	if err != nil {
		panic(err)
	}
	return p
}

// NewClient returns an HTTP client backed by the policy.
func NewClient(args ...any) (*http.Client, error) {
	p, err := New(args...)
	if err != nil {
		return nil, err
	}
	return p.Client(), nil
}

// NewHTTPClient is equivalent to NewClient.
func NewHTTPClient(args ...any) (*http.Client, error) {
	return NewClient(args...)
}

// NewRoundTripper returns the policy as a validating http.RoundTripper.
func NewRoundTripper(args ...any) (http.RoundTripper, error) {
	p, err := New(args...)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func configFromArgs(args []any) (Config, error) {
	if len(args) == 0 {
		return Config{}, fmt.Errorf("%w: configuration is required", ErrInvalidTarget)
	}
	var cfg Config
	var hosts []string
	var ports []int
	for _, arg := range args {
		switch value := arg.(type) {
		case Config:
			cfg = value
		case *Config:
			if value == nil {
				return Config{}, fmt.Errorf("%w: nil config", ErrInvalidTarget)
			}
			cfg = *value
		case []string:
			hosts = append(hosts, value...)
		case []int:
			ports = append(ports, value...)
		case http.Header:
			cfg.Credentials = value
		default:
			return Config{}, fmt.Errorf("%w: unsupported constructor argument %T", ErrInvalidTarget, arg)
		}
	}
	cfg.AllowedDomains = append(cfg.AllowedDomains, hosts...)
	cfg.AllowedPorts = append(cfg.AllowedPorts, ports...)
	return cfg, nil
}

func newPolicy(cfg Config) (*Policy, error) {
	domains := append(append([]string(nil), cfg.AllowedDomains...), cfg.AllowedHosts...)
	if len(domains) == 0 {
		return nil, fmt.Errorf("%w: no allowed domains", ErrInvalidTarget)
	}

	patterns := make([]hostPattern, 0, len(domains))
	seenPatterns := make(map[string]struct{})
	var entryPorts []int
	for _, domain := range domains {
		host, port, hasPort, err := splitAllowedEntry(domain)
		if err != nil {
			return nil, err
		}
		if hasPort {
			entryPorts = append(entryPorts, port)
		}
		pattern, err := parseHostPattern(host)
		if err != nil {
			return nil, err
		}
		key := pattern.original
		if _, ok := seenPatterns[key]; ok {
			continue
		}
		seenPatterns[key] = struct{}{}
		patterns = append(patterns, pattern)
	}

	ports := append(append(append([]int(nil), cfg.AllowedPorts...), cfg.Ports...), entryPorts...)
	if len(ports) == 0 {
		ports = []int{443}
	}
	portSet := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("%w: invalid allowed port %d", ErrInvalidTarget, port)
		}
		portSet[port] = struct{}{}
	}

	schemes := cfg.AllowedSchemes
	if len(schemes) == 0 {
		schemes = []string{"http", "https"}
	}
	schemeSet := make(map[string]struct{}, len(schemes))
	for _, scheme := range schemes {
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		if scheme != "http" && scheme != "https" {
			return nil, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidTarget, scheme)
		}
		schemeSet[scheme] = struct{}{}
	}

	credentials := make(http.Header)
	replaceHeaders(credentials, cfg.CredentialHeaders)
	replaceHeaders(credentials, cfg.Credentials)
	if cfg.BearerToken != "" {
		credentials.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	if cfg.Authorization != "" {
		credentials.Set("Authorization", cfg.Authorization)
	}
	removeUnsafeHeaders(credentials)

	provider := cfg.CredentialProvider
	if provider == nil {
		provider = cfg.CredentialsFor
	}

	lookup := cfg.LookupIP
	if lookup == nil {
		resolver := cfg.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return resolver.LookupIP(ctx, "ip", host)
		}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}

	p := &Policy{
		patterns:      patterns,
		ports:         portSet,
		schemes:       schemeSet,
		credentials:   credentials,
		provider:      provider,
		lookupIP:      lookup,
		allowLoopback: cfg.AllowLoopback || cfg.AllowPrivateNetworks || cfg.AllowLocalNetworks,
		allowPrivate:  cfg.AllowPrivate || cfg.AllowPrivateNetworks || cfg.AllowLocalNetworks,
		timeout:       timeout,
		dialTimeout:   dialTimeout,
		dial:          dialer.DialContext,
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSConfig != nil {
		tlsConfig = cfg.TLSConfig.Clone()
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
		tlsConfig.ServerName = ""
		tlsConfig.InsecureSkipVerify = false
	}

	p.base = &http.Transport{
		Proxy:                  nil,
		DialContext:            p.dialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    10,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 1 << 20,
		TLSClientConfig:        tlsConfig,
	}

	p.client = &http.Client{
		Transport: p,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Return the redirect response to the caller instead of following
			// it. A caller can inspect Location, but the credential-bearing
			// request is never replayed to another destination.
			return http.ErrUseLastResponse
		},
	}
	return p, nil
}

func splitAllowedEntry(value string) (host string, port int, hasPort bool, err error) {
	if strings.HasPrefix(value, "[") {
		end := strings.IndexByte(value, ']')
		if end < 0 {
			return "", 0, false, fmt.Errorf("%w: malformed IPv6 allowlist entry %q", ErrInvalidTarget, value)
		}
		host = value[:end+1]
		rest := value[end+1:]
		if rest == "" {
			return host, 0, false, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, false, fmt.Errorf("%w: malformed allowlist entry %q", ErrInvalidTarget, value)
		}
		port, err = strconv.Atoi(rest[1:])
		if err != nil || port < 1 || port > 65535 {
			return "", 0, false, fmt.Errorf("%w: invalid port in allowlist entry %q", ErrInvalidTarget, value)
		}
		return host, port, true, nil
	}

	if strings.Count(value, ":") == 1 {
		index := strings.LastIndexByte(value, ':')
		host = value[:index]
		port, err = strconv.Atoi(value[index+1:])
		if err != nil || port < 1 || port > 65535 {
			return "", 0, false, fmt.Errorf("%w: invalid port in allowlist entry %q", ErrInvalidTarget, value)
		}
		return host, port, true, nil
	}
	return value, 0, false, nil
}

func parseHostPattern(value string) (hostPattern, error) {
	original := strings.TrimSpace(value)
	if original == "" {
		return hostPattern{}, fmt.Errorf("%w: empty allowed domain", ErrInvalidTarget)
	}
	if original != value {
		return hostPattern{}, fmt.Errorf("%w: allowed domain contains surrounding whitespace", ErrInvalidTarget)
	}
	if strings.ContainsAny(original, "/\\@?#") {
		return hostPattern{}, fmt.Errorf("%w: allowed domain %q must be a bare host", ErrInvalidTarget, original)
	}
	if original == "*" {
		return hostPattern{}, fmt.Errorf("%w: bare wildcard is not allowed", ErrInvalidTarget)
	}

	host := normalizeHost(original)
	apex := false
	wildcard := false
	switch {
	case strings.HasPrefix(host, "*."):
		wildcard = true
		host = strings.TrimPrefix(host, "*.")
	case strings.HasPrefix(host, "."):
		wildcard = true
		apex = true
		host = strings.TrimPrefix(host, ".")
	}
	if !wildcard && strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}

	if ip, ok := parseIPLiteral(host); ok {
		if wildcard {
			return hostPattern{}, fmt.Errorf("%w: wildcard IP address %q", ErrInvalidTarget, original)
		}
		if !ip.IsValid() {
			return hostPattern{}, fmt.Errorf("%w: invalid IP address %q", ErrInvalidTarget, original)
		}
		return hostPattern{original: "ip:" + canonicalAddrString(ip), ip: ip, isIP: true}, nil
	}
	if wildcard && host == "" {
		return hostPattern{}, fmt.Errorf("%w: empty wildcard domain", ErrInvalidTarget)
	}
	if err := validateDomainName(host); err != nil {
		return hostPattern{}, fmt.Errorf("%w: allowed domain %q: %v", ErrInvalidTarget, original, err)
	}

	if wildcard {
		return hostPattern{
			original: "suffix:" + host,
			suffix:   "." + host,
			apex:     apex,
		}, nil
	}
	return hostPattern{original: "exact:" + host, exact: true, host: host}, nil
}

func validateDomainName(host string) error {
	if host == "" {
		return errors.New("empty host")
	}
	if len(host) > 253 {
		return errors.New("host is too long")
	}
	for _, r := range host {
		if r > 127 {
			return errors.New("internationalized domain names must be supplied in ASCII/punycode form")
		}
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return errors.New("empty label")
		}
		if len(label) > 63 {
			return errors.New("label is too long")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("label starts or ends with a hyphen")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' {
				return fmt.Errorf("invalid character %q", c)
			}
		}
	}
	return nil
}

func (p *Policy) matchesHost(host string, ip netip.Addr, hasIP bool) bool {
	if hasIP {
		canonical := canonicalAddrString(ip)
		for _, pattern := range p.patterns {
			if pattern.isIP && canonicalAddrString(pattern.ip) == canonical {
				return true
			}
		}
		return false
	}

	for _, pattern := range p.patterns {
		if pattern.isIP {
			continue
		}
		if pattern.exact && host == pattern.host {
			return true
		}
		if !pattern.exact {
			if host == strings.TrimPrefix(pattern.suffix, ".") && pattern.apex {
				return true
			}
			if strings.HasSuffix(host, pattern.suffix) && len(host) > len(pattern.suffix) {
				return true
			}
		}
	}
	return false
}

// Client returns the policy-backed HTTP client. Redirects are returned to the
// caller rather than followed, and environment proxies are ignored.
func (p *Policy) Client() *http.Client {
	if p == nil {
		return nil
	}
	return p.client
}

// HTTPClient is an alias for Client.
func (p *Policy) HTTPClient() *http.Client {
	return p.Client()
}

// Transport returns the policy itself as a validating RoundTripper.
func (p *Policy) Transport() http.RoundTripper {
	if p == nil {
		return nil
	}
	return p
}

// Do sends a request through the policy.
func (p *Policy) Do(req *http.Request) (*http.Response, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("%w: nil policy", ErrInvalidTarget)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInvalidTarget)
	}
	return p.client.Do(req)
}

// ValidateURL parses and resolves rawURL without sending a request. The DNS
// answer is not retained; RoundTrip performs a fresh lookup and pins that
// answer before dialing.
func (p *Policy) ValidateURL(rawURL string) error {
	_, _, err := p.Resolve(rawURL)
	return err
}

// CheckURL is an alias for ValidateURL.
func (p *Policy) CheckURL(rawURL string) error {
	return p.ValidateURL(rawURL)
}

// Validate is an alias for ValidateURL.
func (p *Policy) Validate(rawURL string) error {
	return p.ValidateURL(rawURL)
}

// Resolve validates rawURL and returns its canonical target and validated
// addresses.
func (p *Policy) Resolve(rawURL string) (Target, []net.IP, error) {
	if p == nil {
		return Target{}, nil, fmt.Errorf("%w: nil policy", ErrInvalidTarget)
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	return p.resolveTarget(ctx, rawURL)
}

func (p *Policy) resolveTarget(ctx context.Context, rawURL string) (Target, []net.IP, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(rawURL) == 0 {
		return Target{}, nil, fmt.Errorf("%w: empty URL", ErrInvalidTarget)
	}
	if len(rawURL) > 8192 {
		return Target{}, nil, fmt.Errorf("%w: URL is too long", ErrInvalidTarget)
	}
	if strings.ContainsAny(rawURL, "\r\n\x00") {
		return Target{}, nil, fmt.Errorf("%w: URL contains control characters", ErrInvalidTarget)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return Target{}, nil, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	if u.Opaque != "" {
		return Target{}, nil, fmt.Errorf("%w: opaque URLs are not allowed", ErrInvalidTarget)
	}
	if u.User != nil {
		return Target{}, nil, fmt.Errorf("%w: URL userinfo is not allowed", ErrInvalidTarget)
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := p.schemes[scheme]; !ok {
		return Target{}, nil, fmt.Errorf("%w: scheme %q is not allowed", ErrBlockedTarget, u.Scheme)
	}
	if u.Host == "" {
		return Target{}, nil, fmt.Errorf("%w: URL has no host", ErrInvalidTarget)
	}

	rawHost := u.Hostname()
	if rawHost == "" {
		return Target{}, nil, fmt.Errorf("%w: URL has no hostname", ErrInvalidTarget)
	}
	host := normalizeHost(rawHost)
	ip, hasIP := parseIPLiteral(host)
	if !hasIP {
		if err := validateDomainName(host); err != nil {
			return Target{}, nil, fmt.Errorf("%w: invalid hostname %q: %v", ErrInvalidTarget, rawHost, err)
		}
	} else if !ip.IsValid() {
		return Target{}, nil, fmt.Errorf("%w: invalid IP literal %q", ErrInvalidTarget, rawHost)
	}
	if !hasIP {
		if _, blocked := metadataHostnames[host]; blocked {
			return Target{}, nil, fmt.Errorf("%w: metadata hostname %q", ErrBlockedAddress, host)
		}
	} else if alwaysForbidden(ip) {
		return Target{}, nil, fmt.Errorf("%w: %s is a link-local, metadata, multicast, or unspecified address", ErrBlockedAddress, canonicalAddrString(ip))
	}

	port := 0
	if value := u.Port(); value != "" {
		port, err = strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return Target{}, nil, fmt.Errorf("%w: invalid port %q", ErrInvalidTarget, value)
		}
	} else {
		switch scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		}
	}
	if _, ok := p.ports[port]; !ok {
		return Target{}, nil, fmt.Errorf("%w: port %d is not allowed", ErrBlockedTarget, port)
	}

	if !p.matchesHost(host, ip, hasIP) {
		return Target{}, nil, fmt.Errorf("%w: host %q", ErrBlockedTarget, host)
	}

	target := Target{Scheme: scheme, Host: host, Port: port}
	if hasIP {
		return target, []net.IP{net.IP(ip.AsSlice())}, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, p.dialTimeout)
	defer cancel()
	ips, err := p.lookupIP(lookupCtx, host)
	if err != nil {
		return Target{}, nil, fmt.Errorf("%w: resolve %q: %v", ErrBlockedTarget, host, err)
	}
	if len(ips) == 0 {
		return Target{}, nil, fmt.Errorf("%w: resolve %q: no addresses", ErrBlockedTarget, host)
	}

	validated := make([]net.IP, 0, len(ips))
	seen := make(map[string]struct{}, len(ips))
	allowLoopback := p.allowLoopback || host == "localhost" || strings.HasSuffix(host, ".localhost")
	for _, rawIP := range ips {
		addr, ok := netip.AddrFromSlice(rawIP)
		if !ok {
			return Target{}, nil, fmt.Errorf("%w: resolver returned an invalid address", ErrBlockedAddress)
		}
		addr = addr.Unmap()
		if err := validateResolvedAddress(addr, allowLoopback, p.allowPrivate); err != nil {
			return Target{}, nil, err
		}
		canonical := canonicalAddrString(addr)
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		validated = append(validated, net.IP(addr.AsSlice()))
	}
	if len(validated) == 0 {
		return Target{}, nil, fmt.Errorf("%w: resolve %q: no usable addresses", ErrBlockedTarget, host)
	}
	// Prefer IPv4 when a name is dual-stack. Literal dialing does not provide
	// net.Dialer's Happy Eyeballs fallback, so trying IPv4 first avoids a long
	// wait on an unreachable IPv6 address while still retaining both pin sets.
	sort.SliceStable(validated, func(i, j int) bool {
		return validated[i].To4() != nil && validated[j].To4() == nil
	})
	return target, validated, nil
}

func mergeHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func replaceHeaders(dst, src http.Header) {
	for key, values := range src {
		for existing := range dst {
			if strings.EqualFold(existing, key) {
				delete(dst, existing)
			}
		}
		if len(values) == 0 {
			continue
		}
		dst[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
}

func removeUnsafeHeaders(header http.Header) {
	for key := range header {
		canonical := http.CanonicalHeaderKey(key)
		switch canonical {
		case "Host",
			"Content-Length",
			"Transfer-Encoding",
			"Connection",
			"Proxy-Authorization",
			"Proxy-Connection",
			"Forwarded",
			"X-Forwarded-For",
			"X-Forwarded-Host",
			"X-Forwarded-Port",
			"X-Forwarded-Proto",
			"X-Host",
			"X-Original-URL",
			"X-Rewrite-URL":
			delete(header, key)
		}
	}
}
