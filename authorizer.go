package netsec

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Decision actions returned by the gateway authorization layer.
type Action int

const (
	// ActionAllow permits the request to proceed.
	ActionAllow Action = iota
	// ActionDeny rejects the request; Status carries the HTTP status code.
	ActionDeny
	// ActionRedirect issues a redirect; Location carries the target URL.
	ActionRedirect
)

// maxResolveDepth bounds IPNS resolution chains so a cyclic or overly deep
// chain cannot exhaust the gateway.
const maxResolveDepth = 32

// Common failure reasons surfaced on [Decision].
var (
	// ErrHostNotAllowed indicates the request Host is not a gateway domain.
	ErrHostNotAllowed = errors.New("netsec: host is not an allowed gateway domain")
	// ErrContentBlocked indicates a multihash on the blocklist was reached.
	ErrContentBlocked = errors.New("netsec: content is on the blocklist")
	// ErrInvalidPath indicates the path is not a valid /ipfs/ or /ipns/ path.
	ErrInvalidPath = errors.New("netsec: not a gateway content path")
	// ErrResolveFailed indicates an IPNS name could not be resolved. The
	// authorizer fails closed and treats an unresolvable name as a denial.
	ErrResolveFailed = errors.New("netsec: IPNS resolution failed")
)

// Resolver maps an IPNS name (the segment immediately after /ipns/) to the
// gateway path it points at, which must be an /ipfs/ or /ipns/ path. It is the
// seam the gateway supplies to connect its name system to the authorizer.
type Resolver interface {
	Resolve(name string) (targetPath string, err error)
}

// ResolverFunc adapts a plain function to the [Resolver] interface.
type ResolverFunc func(name string) (string, error)

// Resolve calls f(name).
func (f ResolverFunc) Resolve(name string) (string, error) {
	return f(name)
}

// Decision is the result of evaluating a request.
type Decision struct {
	Action   Action
	Status   int
	Location string
	Reason   error

	// Path is the normalized gateway path the request was evaluated against.
	Path string
	// CID is the content CID reached after any IPNS resolution, when known.
	CID CID
}

// Config configures an [Authorizer].
type Config struct {
	// Blocklist is the multihash blocklist enforced for every request.
	Blocklist *Blocklist

	// Resolver resolves /ipns/ names. Required to authorize IPNS traffic.
	Resolver Resolver

	// AllowedHosts enumerates the gateway domains permitted to serve traffic.
	// Host names are matched case-insensitively on whole labels; a request
	// whose Host is outside this set is rejected. Both the path gateway and
	// the subdomain gateway host(s) should be listed.
	AllowedHosts []string

	// SubdomainHost is the origin-isolation gateway host used to build
	// subdomain redirects for executable content (for example
	// "ipfs.example.net"). When empty, executable content on the path gateway
	// is denied rather than served from a shared origin.
	SubdomainHost string

	// ExecutableTypes lists MIME media types that must be isolated on a
	// per-content subdomain. When nil a built-in default set is used.
	ExecutableTypes []string
}

// Authorizer authorizes gateway requests. Create one with [New].
type Authorizer struct {
	blocklist       *Blocklist
	resolver        Resolver
	allowedHosts    map[string]struct{}
	subdomainHost   string
	executableTypes map[string]struct{}
}

// defaultExecutableTypes mirrors the content classes the reference gateway
// isolates into subdomains because they can execute in a user's origin.
var defaultExecutableTypes = []string{
	"text/html",
	"application/xhtml+xml",
	"image/svg+xml",
	"application/pdf",
}

// New builds an [Authorizer] from cfg.
func New(cfg Config) *Authorizer {
	hosts := make(map[string]struct{}, len(cfg.AllowedHosts))
	for _, h := range cfg.AllowedHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts[h] = struct{}{}
		}
	}

	types := cfg.ExecutableTypes
	if types == nil {
		types = defaultExecutableTypes
	}
	typeSet := make(map[string]struct{}, len(types))
	for _, t := range types {
		typeSet[mediaType(t)] = struct{}{}
	}

	return &Authorizer{
		blocklist:       cfg.Blocklist,
		resolver:        cfg.Resolver,
		allowedHosts:    hosts,
		subdomainHost:   strings.ToLower(strings.TrimSpace(cfg.SubdomainHost)),
		executableTypes: typeSet,
	}
}

// Authorize applies the blocklist to a request.
//
// It first rejects any Host outside the configured gateway domains. For an
// /ipfs/ path the CID is normalized to its multihash before the blocklist is
// consulted. For an /ipns/ path the name is resolved (following /ipns/ chains
// up to [maxResolveDepth] hops) and every reached CID is checked the same way.
// Resolution failure or a blocked multihash yields [ActionDeny]; the
// authorizer never fails open.
func (a *Authorizer) Authorize(r *http.Request) Decision {
	host := requestHost(r)
	if !a.hostAllowed(host) {
		return deny(http.StatusForbidden, ErrHostNotAllowed, r.URL.Path, CID{})
	}
	return a.authorizePath(r.URL.Path)
}

// AuthorizePath authorizes a gateway path independently of an [http.Request],
// which is convenient at the routing layer before a full request exists. Host
// validation is skipped here because there is no Host; callers that receive
// real requests should use [Authorizer.Authorize].
func (a *Authorizer) AuthorizePath(path string) Decision {
	return a.authorizePath(path)
}

func (a *Authorizer) authorizePath(path string) Decision {
	kind, root, _, err := splitGatewayPath(path)
	if err != nil {
		return deny(http.StatusBadRequest, err, path, CID{})
	}

	switch kind {
	case "ipfs":
		cid, err := ParseCID(root)
		if err != nil {
			return deny(http.StatusBadRequest, err, path, CID{})
		}
		return a.checkCID(path, cid)
	case "ipns":
		return a.authorizeIPNS(path, root)
	default:
		return deny(http.StatusBadRequest, ErrInvalidPath, path, CID{})
	}
}

func (a *Authorizer) checkCID(path string, cid CID) Decision {
	if a.blocklist.Contains(cid.Multihash()) {
		return deny(http.StatusGone, ErrContentBlocked, path, cid)
	}
	return Decision{Action: ActionAllow, Path: path, CID: cid}
}

// authorizeIPNS resolves the name and applies the blocklist to whatever CID
// the chain terminates on. Intermediate /ipns/ hops are followed so an
// attacker cannot hide blocked content behind a chain of names.
func (a *Authorizer) authorizeIPNS(originalPath, name string) Decision {
	if a.resolver == nil {
		return deny(http.StatusForbidden, ErrResolveFailed, originalPath, CID{})
	}

	visited := make(map[string]struct{})
	current := name

	for depth := 0; depth < maxResolveDepth; depth++ {
		if _, seen := visited[current]; seen {
			return deny(http.StatusForbidden, ErrResolveFailed, originalPath, CID{})
		}
		visited[current] = struct{}{}

		target, err := a.resolver.Resolve(current)
		if err != nil {
			return deny(http.StatusForbidden, ErrResolveFailed, originalPath, CID{})
		}

		kind, root, _, err := splitGatewayPath(target)
		if err != nil {
			return deny(http.StatusForbidden, ErrResolveFailed, originalPath, CID{})
		}

		switch kind {
		case "ipfs":
			cid, err := ParseCID(root)
			if err != nil {
				return deny(http.StatusForbidden, ErrResolveFailed, originalPath, CID{})
			}
			return a.checkCID(originalPath, cid)
		case "ipns":
			current = root
			continue
		}
	}

	return deny(http.StatusForbidden, ErrResolveFailed, originalPath, CID{})
}

// ContentPolicy enforces origin isolation after [Authorizer.Authorize] has
// allowed a request. When the resolved content type is executable and the
// request arrives on the path gateway, the response is a redirect to a
// per-CID subdomain. Non-executable types are served in place. Host
// validation is applied again so this step cannot be reached from a
// non-gateway domain.
func (a *Authorizer) ContentPolicy(r *http.Request, contentType string, allowed Decision) Decision {
	host := requestHost(r)
	if !a.hostAllowed(host) {
		return deny(http.StatusForbidden, ErrHostNotAllowed, r.URL.Path, allowed.CID)
	}

	if !a.isExecutable(contentType) {
		return allowed
	}

	if a.subdomainHost == "" {
		// No isolation origin configured: never serve active content from the
		// shared path-gateway origin.
		return deny(http.StatusForbidden, ErrHostNotAllowed, r.URL.Path, allowed.CID)
	}

	location, err := a.subdomainURL(r, allowed.CID)
	if err != nil {
		return deny(http.StatusBadRequest, err, r.URL.Path, allowed.CID)
	}

	return Decision{
		Action:   ActionRedirect,
		Status:   http.StatusFound,
		Location: location,
		Path:     r.URL.Path,
		CID:      allowed.CID,
	}
}

// subdomainURL builds https?://<label>.<subdomain-host><path>[?query]. The
// label is the CIDv1 base32 encoding for /ipfs/ content.
func (a *Authorizer) subdomainURL(r *http.Request, cid CID) (string, error) {
	if cid.Multihash().IsZero() {
		return "", ErrInvalidPath
	}
	label, err := cid.DNSLabel()
	if err != nil {
		return "", err
	}

	scheme := "https"
	if r.TLS == nil && !isForwardedHTTPS(r) {
		scheme = "http"
	}

	target := url.URL{
		Scheme:   scheme,
		Host:     label + "." + a.subdomainHost,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	return target.String(), nil
}

func isForwardedHTTPS(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// hostAllowed reports whether host (already stripped of its port) is one of the
// configured gateway domains. Matching is case-insensitive on the full label;
// subdomains of an allowed host are not implicitly trusted.
func (a *Authorizer) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	_, ok := a.allowedHosts[strings.ToLower(host)]
	return ok
}

func (a *Authorizer) isExecutable(contentType string) bool {
	_, ok := a.executableTypes[mediaType(contentType)]
	return ok
}

func deny(status int, reason error, path string, cid CID) Decision {
	return Decision{Action: ActionDeny, Status: status, Reason: reason, Path: path, CID: cid}
}

// requestHost returns the request host without any port suffix.
func requestHost(r *http.Request) string {
	h := r.Host
	if h == "" {
		h = r.URL.Host
	}
	return stripPort(strings.ToLower(h))
}

func stripPort(host string) string {
	// Keep bracket-less IPv6 literals intact by only trimming after the last
	// colon when there is no closing bracket.
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		return host[:i]
	}
	if host == "" {
		return host
	}
	return strings.TrimSuffix(host, ".")
}

// mediaType reduces a Content-Type value to its type/subtype, lower-cased and
// without parameters or surrounding whitespace.
func mediaType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// splitGatewayPath splits "/ipfs/<cid-or-name>/remainder" or the equivalent
// /ipns/ path into its namespace, root identifier and remaining path. The
// remainder always starts with "/" when non-empty.
func splitGatewayPath(path string) (namespace, root, remainder string, err error) {
	path = cleanPath(path)
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) < 2 || parts[1] == "" {
		return "", "", "", ErrInvalidPath
	}

	namespace = parts[0]
	root = parts[1]
	if namespace != "ipfs" && namespace != "ipns" {
		return "", "", "", ErrInvalidPath
	}
	if len(parts) == 3 {
		remainder = "/" + parts[2]
	}
	return namespace, root, remainder, nil
}

func cleanPath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}
