// Package netsec provides a request-identity component for Kubernetes-style
// aggregated API servers. Identity headers (e.g. X-Remote-User) are only
// honored when the request arrives from a configured front-proxy address over
// mTLS with a client certificate whose SAN is on the front-proxy trust list.
package netsec

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"sync"
)

var (
	// ErrUntrustedProxyAddr is returned when the request does not originate
	// from a configured front-proxy source address.
	ErrUntrustedProxyAddr = errors.New("netsec: request source is not a configured front-proxy address")
	// ErrNoClientCert is returned when the connection carries no mTLS client
	// certificate from the front proxy.
	ErrNoClientCert = errors.New("netsec: front-proxy client certificate required")
	// ErrUntrustedProxyCert is returned when the client certificate does not
	// chain to the configured CA pool or its SAN is not on the trust list.
	ErrUntrustedProxyCert = errors.New("netsec: client certificate is not a trusted front-proxy certificate")
	// ErrDuplicateHeader is returned when an identity header appears more
	// than once.
	ErrDuplicateHeader = errors.New("netsec: duplicate identity header")
	// ErrNonCanonicalHeader is returned when an identity header is sent with
	// non-canonical casing (e.g. "x-remote-user").
	ErrNonCanonicalHeader = errors.New("netsec: identity header uses non-canonical casing")
	// ErrInvalidValue is returned when an identity header value does not
	// match the accepted value format.
	ErrInvalidValue = errors.New("netsec: identity header value has invalid format")
	// ErrMissingUser is returned when a trusted proxy request omits the user
	// identity header.
	ErrMissingUser = errors.New("netsec: trusted proxy request is missing the user identity header")
	// ErrMissingAuditID is returned when a trusted proxy request omits the
	// request audit ID.
	ErrMissingAuditID = errors.New("netsec: trusted proxy request is missing the audit ID")
	// ErrStaleGeneration is returned when the proxy presents a certificate
	// generation that has been superseded by a newer one (cert rotation).
	ErrStaleGeneration = errors.New("netsec: proxy certificate generation has been superseded")
)

// identityValuePattern is the single value-format rule applied to every
// identity header value: printable, no whitespace, no commas, bounded length.
// It accepts Kubernetes-style usernames such as "system:serviceaccount:ns:sa"
// and e-mail style names.
var identityValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,255}$`)

// Config configures an Authenticator.
type Config struct {
	// ClusterID is bound into every accepted identity. Required.
	ClusterID string
	// UserHeader carries the authenticated user. Defaults to "X-Remote-User".
	UserHeader string
	// GroupHeader carries comma-separated groups. Defaults to "X-Remote-Group".
	GroupHeader string
	// AuditIDHeader carries the per-request audit ID. Defaults to "Audit-Id"
	// (the canonical MIME form of "Audit-ID").
	AuditIDHeader string
	// ProxyCIDRs lists the allowed front-proxy source addresses, as CIDRs or
	// bare IPs. At least one entry is required.
	ProxyCIDRs []string
	// TrustedProxySANs lists DNS or URI SANs that front-proxy client
	// certificates must present. At least one entry is required.
	TrustedProxySANs []string
	// ClientCAs is the CA pool used to verify front-proxy client
	// certificates. Required.
	ClientCAs *x509.CertPool
}

// Identity is an authenticated request identity. It is bound to the cluster,
// the proxy instance (matched certificate SAN), the proxy certificate
// generation (serial number) and the request audit ID, so identities minted
// on a previous connection or certificate generation cannot be replayed.
type Identity struct {
	User           string
	Groups         []string
	ClusterID      string
	ProxyInstance  string
	CertGeneration string
	AuditID        string
}

// Authenticator verifies front-proxy request identity.
type Authenticator struct {
	clusterID     string
	userHeader    string
	groupHeader   string
	auditIDHeader string
	proxyNets     []*net.IPNet
	trustedSANs   map[string]struct{}
	clientCAs     *x509.CertPool
	headerNames   []string

	mu          sync.Mutex
	generations map[string]*big.Int // proxy instance -> newest accepted cert serial
}

// NewAuthenticator validates cfg and returns an Authenticator.
func NewAuthenticator(cfg Config) (*Authenticator, error) {
	if cfg.ClusterID == "" {
		return nil, errors.New("netsec: ClusterID is required")
	}
	if cfg.ClientCAs == nil {
		return nil, errors.New("netsec: ClientCAs is required")
	}
	a := &Authenticator{
		clusterID:     cfg.ClusterID,
		userHeader:    orDefault(cfg.UserHeader, "X-Remote-User"),
		groupHeader:   orDefault(cfg.GroupHeader, "X-Remote-Group"),
		auditIDHeader: orDefault(cfg.AuditIDHeader, "Audit-Id"),
		clientCAs:     cfg.ClientCAs,
		trustedSANs:   make(map[string]struct{}, len(cfg.TrustedProxySANs)),
		generations:   make(map[string]*big.Int),
	}
	for _, h := range []string{a.userHeader, a.groupHeader, a.auditIDHeader} {
		if textproto.CanonicalMIMEHeaderKey(h) != h {
			return nil, fmt.Errorf("netsec: header name %q is not canonical", h)
		}
	}
	a.headerNames = []string{a.userHeader, a.groupHeader, a.auditIDHeader}
	if len(cfg.ProxyCIDRs) == 0 {
		return nil, errors.New("netsec: at least one proxy CIDR is required")
	}
	for _, c := range cfg.ProxyCIDRs {
		n, err := parseCIDROrIP(c)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid proxy CIDR %q: %w", c, err)
		}
		a.proxyNets = append(a.proxyNets, n)
	}
	if len(cfg.TrustedProxySANs) == 0 {
		return nil, errors.New("netsec: at least one trusted proxy SAN is required")
	}
	for _, san := range cfg.TrustedProxySANs {
		a.trustedSANs[san] = struct{}{}
	}
	return a, nil
}

// HeaderNames returns the identity header names managed by this component.
func (a *Authenticator) HeaderNames() []string {
	out := make([]string, len(a.headerNames))
	copy(out, a.headerNames)
	return out
}

// StripIdentityHeaders removes every occurrence of the given identity header
// names from h, regardless of casing. It reports whether anything was
// removed. It must be applied to every inbound request before it reaches the
// backend so client-supplied identity headers never survive.
func StripIdentityHeaders(h http.Header, names ...string) bool {
	removed := false
	for key := range h {
		for _, name := range names {
			if strings.EqualFold(key, name) {
				delete(h, key)
				removed = true
				break
			}
		}
	}
	return removed
}

// Authenticate verifies that r comes from a trusted front proxy and, if so,
// returns the bound Identity. Every check is performed against the live
// connection state, so nothing is cached across connections: a reconnect or
// certificate rotation invalidates previously accepted identities.
func (a *Authenticator) Authenticate(r *http.Request) (*Identity, error) {
	if !a.trustedAddr(r.RemoteAddr) {
		return nil, ErrUntrustedProxyAddr
	}
	leaf, err := a.verifiedPeerCert(r)
	if err != nil {
		return nil, err
	}
	instance, err := a.matchSAN(leaf)
	if err != nil {
		return nil, err
	}
	user, err := a.singleValue(r.Header, a.userHeader)
	if err != nil {
		return nil, err
	}
	if user == "" {
		return nil, ErrMissingUser
	}
	if !identityValuePattern.MatchString(user) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidValue, a.userHeader)
	}
	groups, err := a.groupValues(r.Header)
	if err != nil {
		return nil, err
	}
	auditID, err := a.singleValue(r.Header, a.auditIDHeader)
	if err != nil {
		return nil, err
	}
	if auditID == "" {
		return nil, ErrMissingAuditID
	}
	if !identityValuePattern.MatchString(auditID) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidValue, a.auditIDHeader)
	}
	generation := leaf.SerialNumber
	if err := a.checkGeneration(instance, generation); err != nil {
		return nil, err
	}
	return &Identity{
		User:           user,
		Groups:         groups,
		ClusterID:      a.clusterID,
		ProxyInstance:  instance,
		CertGeneration: generation.Text(16),
		AuditID:        auditID,
	}, nil
}

// Middleware enforces proxy authentication. Requests that fail proxy
// authentication have any client-supplied identity headers stripped before
// they can reach the backend; spoofing attempts (identity headers from an
// unverified source, or any failed request from a proxy address) are rejected
// with 403. Plain direct clients without identity headers pass through
// without an identity so other authenticators may run.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := a.Authenticate(r)
		if err != nil {
			hadIdentityHeaders := StripIdentityHeaders(r.Header, a.headerNames...)
			if errors.Is(err, ErrUntrustedProxyAddr) && !hadIdentityHeaders {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(ContextWithIdentity(r.Context(), id)))
	})
}

type identityContextKey struct{}

// ContextWithIdentity returns a context carrying id.
func ContextWithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// IdentityFromContext returns the Identity stored in ctx, if any.
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityContextKey{}).(*Identity)
	return id, ok
}

func (a *Authenticator) trustedAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range a.proxyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *Authenticator) verifiedPeerCert(r *http.Request) (*x509.Certificate, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil, ErrNoClientCert
	}
	leaf := r.TLS.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, cert := range r.TLS.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         a.clientCAs,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUntrustedProxyCert, err)
	}
	return leaf, nil
}

func (a *Authenticator) matchSAN(leaf *x509.Certificate) (string, error) {
	for _, name := range leaf.DNSNames {
		if _, ok := a.trustedSANs[name]; ok {
			return name, nil
		}
	}
	for _, uri := range leaf.URIs {
		if _, ok := a.trustedSANs[uri.String()]; ok {
			return uri.String(), nil
		}
	}
	return "", ErrUntrustedProxyCert
}

// singleValue applies the single parsing rule for identity headers: the
// canonical header name must appear exactly once, with no case variants and
// no duplicates. Value-format validation is left to the caller.
func (a *Authenticator) singleValue(h http.Header, canonical string) (string, error) {
	matches := 0
	for key := range h {
		if !strings.EqualFold(key, canonical) {
			continue
		}
		if key != canonical {
			return "", fmt.Errorf("%w: %q", ErrNonCanonicalHeader, key)
		}
		matches++
	}
	if matches > 1 {
		return "", fmt.Errorf("%w: %q", ErrDuplicateHeader, canonical)
	}
	values := h[canonical]
	if len(values) > 1 {
		return "", fmt.Errorf("%w: %q", ErrDuplicateHeader, canonical)
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[0], nil
}

func (a *Authenticator) groupValues(h http.Header) ([]string, error) {
	line, err := a.singleValue(h, a.groupHeader)
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, nil
	}
	groups := strings.Split(line, ",")
	for _, g := range groups {
		if !identityValuePattern.MatchString(g) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidValue, a.groupHeader)
		}
	}
	return groups, nil
}

// checkGeneration enforces certificate-generation monotonicity per proxy
// instance: once a newer certificate serial has been seen, older generations
// are rejected, so connections authenticated under a rotated-out certificate
// become invalid.
func (a *Authenticator) checkGeneration(instance string, serial *big.Int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.generations[instance]; ok && serial.Cmp(current) < 0 {
		return ErrStaleGeneration
	}
	a.generations[instance] = new(big.Int).Set(serial)
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parseCIDROrIP(s string) (*net.IPNet, error) {
	if _, n, err := net.ParseCIDR(s); err == nil {
		return n, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, errors.New("not an IP or CIDR")
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}
