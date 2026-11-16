// Package netsec provides referral-following decisions for LDAP directory
// clients. It decides whether a referral returned by a directory server may
// be followed without leaking bind credentials to an attacker-controlled
// server: transport encryption must never be downgraded, referral hosts must
// be explicitly trusted, referral DNs must stay under the caller's search
// root, chains are bounded in depth and checked for loops, and bind
// credentials are rebound at most once and never forwarded along a chain.
package netsec

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// DefaultMaxReferralDepth is the maximum number of referral hops followed
// when Policy.MaxDepth is not set.
const DefaultMaxReferralDepth = 3

// Policy configures a Follower.
type Policy struct {
	// TrustedHosts is the caller-provided whitelist of directory hosts that
	// may receive bind credentials. Comparison is case-insensitive.
	TrustedHosts []string
	// SearchRoot is the base DN (e.g. "dc=example,dc=org") that referral
	// DNs must stay within. An empty root matches any DN.
	SearchRoot string
	// MaxDepth bounds referral chain length. Values <= 0 select
	// DefaultMaxReferralDepth.
	MaxDepth int
}

// ConnInfo describes the original connection that received the referral.
type ConnInfo struct {
	// Host, Port and DN identify the original request; they seed loop
	// detection so a referral pointing back at the origin is rejected.
	Host string
	Port int
	DN   string
	// StartTLS reports whether the original connection already negotiated
	// StartTLS.
	StartTLS bool
	// Anonymous reports whether the original connection is anonymously
	// bound. Anonymous connections never rebind credentials on referrals.
	Anonymous bool
}

// Decision is the verdict for a single referral URL.
type Decision struct {
	// Referral is the URL as received from the server.
	Referral string
	// Follow reports whether the referral may be followed.
	Follow bool
	// Rebind reports whether the caller's bind credentials may be sent to
	// this host. It is true at most once per Follower, only for a
	// whitelisted host, and never for anonymous connections.
	Rebind bool
	// Reason explains a refusal; empty when Follow is true.
	Reason string
	// Host, Port and DN are the normalized referral target.
	Host string
	Port int
	DN   string
}

// Follower tracks referral chain state (depth, visited targets, credential
// rebinding) across the hops of one logical operation. It is not safe for
// concurrent use.
type Follower struct {
	trusted  map[string]struct{}
	root     string
	maxDepth int
	depth    int
	visited  map[string]struct{}
	rebound  bool
	seeded   bool
}

// NewFollower builds a Follower from p.
func NewFollower(p Policy) *Follower {
	maxDepth := p.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxReferralDepth
	}
	f := &Follower{
		trusted:  make(map[string]struct{}, len(p.TrustedHosts)),
		root:     normalizeDN(p.SearchRoot),
		maxDepth: maxDepth,
		visited:  make(map[string]struct{}),
	}
	for _, h := range p.TrustedHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			f.trusted[h] = struct{}{}
		}
	}
	return f
}

// Evaluate judges one set of referral URLs returned for the operation
// tracked by f. Each call represents one hop of the referral chain.
func (f *Follower) Evaluate(conn ConnInfo, referrals []string) []Decision {
	decisions := make([]Decision, 0, len(referrals))
	if f.depth >= f.maxDepth {
		for _, raw := range referrals {
			decisions = append(decisions, Decision{
				Referral: raw,
				Reason:   fmt.Sprintf("referral depth limit %d exceeded", f.maxDepth),
			})
		}
		return decisions
	}
	if !f.seeded {
		f.seeded = true
		if conn.Host != "" {
			f.visited[visitKey(strings.ToLower(conn.Host), conn.Port, normalizeDN(conn.DN))] = struct{}{}
		}
	}
	f.depth++
	for _, raw := range referrals {
		decisions = append(decisions, f.evaluateOne(conn, raw))
	}
	return decisions
}

func (f *Follower) evaluateOne(conn ConnInfo, raw string) Decision {
	d := Decision{Referral: raw}

	u, err := url.Parse(raw)
	if err != nil {
		d.Reason = "unparseable referral URL"
		return d
	}

	defaultPort := 0
	switch strings.ToLower(u.Scheme) {
	case "ldaps":
		defaultPort = 636
	case "ldap":
		if !conn.StartTLS {
			d.Reason = "plaintext ldap referral requires an encrypted transport"
			return d
		}
		if !requiresStartTLS(u) {
			d.Reason = "refusing downgrade from StartTLS to plaintext ldap"
			return d
		}
		defaultPort = 389
	default:
		d.Reason = "unsupported URL scheme " + strconv.Quote(u.Scheme)
		return d
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		d.Reason = "referral URL has no host"
		return d
	}
	if _, ok := f.trusted[host]; !ok {
		d.Reason = "host not in trusted whitelist"
		return d
	}

	port := defaultPort
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			d.Reason = "invalid port in referral URL"
			return d
		}
		port = n
	}

	dn, err := decodeReferralDN(u)
	if err != nil {
		d.Reason = err.Error()
		return d
	}
	if !dnWithinRoot(dn, f.root) {
		d.Reason = "referral DN escapes the search root"
		return d
	}

	key := visitKey(host, port, dn)
	if _, ok := f.visited[key]; ok {
		d.Reason = "referral loop detected"
		return d
	}
	f.visited[key] = struct{}{}

	d.Follow = true
	d.Host = host
	d.Port = port
	d.DN = dn
	switch {
	case conn.Anonymous:
		// Anonymous binds stay anonymous on referrals.
	case f.rebound:
		// Credentials were already rebound once; never forward them
		// further along the chain.
	default:
		d.Rebind = true
		f.rebound = true
	}
	return d
}

// requiresStartTLS reports whether the referral URL carries a StartTLS
// extension, e.g. ldap://host/dn??sub?(uid=x)?!starttls.
func requiresStartTLS(u *url.URL) bool {
	parts := strings.Split(u.RawQuery, "?")
	if len(parts) < 4 {
		return false
	}
	for _, ext := range strings.Split(parts[3], ",") {
		name := strings.TrimPrefix(strings.TrimSpace(ext), "!")
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if strings.EqualFold(name, "starttls") || strings.EqualFold(name, "x-starttls") {
			return true
		}
	}
	return false
}

// decodeReferralDN extracts and percent-decodes the DN from the referral
// URL path. DNs containing ".." (before or after decoding) are rejected.
func decodeReferralDN(u *url.URL) (string, error) {
	raw := strings.TrimPrefix(u.EscapedPath(), "/")
	if raw == "" {
		return "", nil
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("invalid percent-encoding in referral DN")
	}
	if strings.Contains(decoded, "..") {
		return "", fmt.Errorf("referral DN contains '..'")
	}
	return normalizeDN(decoded), nil
}

// normalizeDN lowercases a DN and strips insignificant whitespace so
// equivalent DNs compare equal.
func normalizeDN(dn string) string {
	rdns := strings.Split(dn, ",")
	out := rdns[:0]
	for _, rdn := range rdns {
		if rdn = strings.TrimSpace(rdn); rdn != "" {
			out = append(out, rdn)
		}
	}
	return strings.ToLower(strings.Join(out, ","))
}

// dnWithinRoot reports whether dn is equal to or below root.
func dnWithinRoot(dn, root string) bool {
	if root == "" {
		return true
	}
	return dn == root || strings.HasSuffix(dn, ","+root)
}

func visitKey(host string, port int, dn string) string {
	return fmt.Sprintf("%s:%d/%s", host, port, dn)
}
