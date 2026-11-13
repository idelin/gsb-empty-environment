// Package referral decides whether LDAP referrals returned by a directory
// server may be followed, and which credentials (if any) may be presented to
// the referred-to host.
//
// A directory client that blindly follows a referral reuses the bound
// credentials against an attacker-chosen host, leaking the user's cleartext
// password. Policy centralizes every check that must pass before a referral
// is followed:
//
//   - Transport must never be downgraded from encryption to cleartext. Only
//     ldaps, or ldap-with-StartTLS where the original connection also used
//     StartTLS, are followed.
//   - The referred host must be in the caller-supplied whitelist (compared
//     case-insensitively, default ldaps port 636 / ldap port 389).
//   - The referral DN must stay under the caller's search root; percent
//     encoding and ".." traversal are decoded/resolved before comparison.
//   - Credentials are presented at most once (the first followed hop) and
//     are never forwarded further down the referral chain. An anonymous
//     original bind stays anonymous.
//   - Chain depth is capped at three referrals and any repeated
//     host/port/DN triple (a loop) stops the chain.
package referral

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// Security describes how the original connection was (or, for a referral,
// must be) protected.
type Security int

const (
	// SecurityCleartext is an ldap:// connection without StartTLS.
	SecurityCleartext Security = iota
	// SecurityStartTLS is an ldap:// connection upgraded with StartTLS.
	SecurityStartTLS
	// SecurityLDAPS is a native TLS ldaps:// connection.
	SecurityLDAPS
)

const (
	defaultLDAPPort  = 389
	defaultLDAPSPort = 636

	// MaxDepth is the maximum number of referrals that may be followed.
	MaxDepth = 3
)

// Identity is the bound identity of the original connection. A zero value
// (or one with an empty BindDN) represents an anonymous bind.
type Identity struct {
	// BindDN is the distinguished name used to bind; empty means anonymous.
	BindDN string
	// Password is the cleartext bind secret. It never leaves the package's
	// decision values and is only returned for the single allowed rebind.
	Password string
}

// Anonymous reports whether the identity is an anonymous bind.
func (i Identity) Anonymous() bool {
	return strings.TrimSpace(i.BindDN) == ""
}

// Origin describes the original directory request and its transport.
type Origin struct {
	// Addr is the original directory request address, either "host:port" or
	// an ldap(s):// URL. A referral back to the same host, port and DN is a
	// loop. It may be empty when the caller seeds the chain explicitly.
	Addr string
	// Transport is the protection of the original connection.
	Transport Security
	// Bind is the identity used on the original connection.
	Bind Identity
	// SearchBase is the caller's trusted search root. Referral DNs must be
	// equal to or subordinate to it.
	SearchBase string
}

// Referral is a parsed LDAP referral URL (RFC 4516).
type Referral struct {
	raw      string
	Scheme   string
	Host     string // lower-cased host without port
	Port     int    // normalized default port filled in
	DN       string // percent-decoded base DN
	StartTLS bool   // the startTLS extension was requested
}

// String returns the original referral URL.
func (r Referral) String() string { return r.raw }

// Reason explains why a referral was rejected (or the special Follow value).
type Reason string

const (
	ReasonFollow             Reason = "follow"
	ReasonMalformedReferral  Reason = "malformed_referral"
	ReasonDepthExceeded      Reason = "depth_exceeded"
	ReasonReferralLoop       Reason = "referral_loop"
	ReasonHostNotWhitelisted Reason = "host_not_whitelisted"
	ReasonDowngrade          Reason = "transport_downgrade"
	ReasonDNOutOfScope       Reason = "dn_out_of_scope"
)

// Policy is the reusable, concurrency-safe referral decision engine.
type Policy struct {
	// AllowedHosts is the trusted directory host whitelist. Entries are
	// compared case-insensitively and may be either bare hosts or host:port.
	AllowedHosts []string
}

// Node is one visited entry in a referral chain.
type Node struct {
	Host string
	Port int
	DN   string
}

func (n Node) equal(other Node) bool {
	return n.Host == other.Host && n.Port == other.Port &&
		strings.EqualFold(n.DN, other.DN)
}

// node returns the original request as a chain node so referrals pointing
// straight back at the origin are detected as loops.
func (o Origin) node() (Node, bool) {
	addr := strings.TrimSpace(o.Addr)
	if addr == "" {
		return Node{}, false
	}
	host, portText := addr, ""
	if u, err := url.Parse(addr); err == nil && (u.Scheme == "ldap" || u.Scheme == "ldaps") {
		host = u.Hostname()
		portText = u.Port()
		if portText == "" {
			if u.Scheme == "ldaps" {
				portText = "636"
			} else {
				portText = "389"
			}
		}
	} else if h, p, err := net.SplitHostPort(addr); err == nil {
		host, portText = h, p
	}
	port, err := parsePort(portText)
	if err != nil {
		return Node{}, false
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	return Node{Host: host, Port: port, DN: o.SearchBase}, true
}

// Chain tracks the referrals followed so far for depth and loop detection.
// The zero value is ready to use.
type Chain struct {
	visited        []Node
	credentialUsed bool
}

// NewChain returns an empty chain.
func NewChain() *Chain { return &Chain{} }

// Len returns the number of referrals already followed.
func (c *Chain) Len() int { return len(c.visited) }

// Visited reports whether node has already been visited on this chain.
func (c *Chain) Visited(node Node) bool {
	for _, n := range c.visited {
		if n.equal(node) {
			return true
		}
	}
	return false
}

// Seed pre-populates the chain with already-visited nodes, for callers that
// maintain their own chain state.
func (c *Chain) Seed(nodes ...Node) *Chain {
	c.visited = append(c.visited, nodes...)
	return c
}

// Decision is the verdict for a single referral.
type Decision struct {
	// Follow is true only when every policy check passed.
	Follow bool
	// Reason is ReasonFollow when followed, otherwise the rejection reason.
	Reason Reason
	// Node is the normalized referred-to node.
	Node Node
	// Rebind is the identity to present. It is zero for anonymous hops and
	// for every hop after the single allowed credential rebind.
	Rebind Identity
}

// Parse decodes an RFC 4516 LDAP URL into a Referral.
func Parse(raw string) (Referral, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Referral{}, err
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "ldap" && scheme != "ldaps" {
		return Referral{}, errors.New("referral: unsupported scheme " + u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return Referral{}, errors.New("referral: missing host")
	}

	port := u.Port()
	if port == "" {
		if scheme == "ldaps" {
			port = "636"
		} else {
			port = "389"
		}
	}
	portNum, err := parsePort(port)
	if err != nil {
		return Referral{}, err
	}

	dn := strings.TrimPrefix(u.EscapedPath(), "/")
	decoded, err := url.PathUnescape(dn)
	if err != nil {
		return Referral{}, err
	}

	return Referral{
		raw:      raw,
		Scheme:   scheme,
		Host:     host,
		Port:     portNum,
		DN:       decoded,
		StartTLS: hasStartTLSExtension(u.RawQuery),
	}, nil
}

func parsePort(s string) (int, error) {
	var n int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("referral: invalid port")
		}
		n = n*10 + int(ch-'0')
		if n > 65535 {
			return 0, errors.New("referral: invalid port")
		}
	}
	if n == 0 {
		return 0, errors.New("referral: invalid port")
	}
	return n, nil
}

// hasStartTLSExtension inspects the URL query for the startTLS extension
// described in RFC 4515. Per RFC 4516 the DN lives in the URL path, so the
// query is "attributes?scope?filter?extensions" and extensions is the fourth
// component (index 3).
func hasStartTLSExtension(rawQuery string) bool {
	parts := strings.Split(rawQuery, "?")
	if len(parts) < 4 {
		return false
	}
	for _, ext := range strings.Split(parts[3], ",") {
		// An extension is "extype[=exvalue]"; criticality (the leading "!")
		// does not change the requirement.
		token := strings.TrimPrefix(ext, "!")
		if i := strings.IndexByte(token, '='); i >= 0 {
			token = token[:i]
		}
		if strings.EqualFold(token, "1.3.6.1.4.1.1466.20037") {
			return true
		}
	}
	return false
}

// Decide evaluates one referral against the policy for the given origin and
// current chain state. The chain is not mutated; use Chain.Advance to record
// a followed referral.
func (p Policy) Decide(origin Origin, chain *Chain, raw string) Decision {
	if chain == nil {
		chain = NewChain()
	}

	ref, err := Parse(raw)
	if err != nil {
		return Decision{Follow: false, Reason: ReasonMalformedReferral}
	}
	node := Node{Host: ref.Host, Port: ref.Port, DN: ref.DN}

	if chain.Len() >= MaxDepth {
		return Decision{Node: node, Reason: ReasonDepthExceeded}
	}
	if originNode, ok := origin.node(); ok && originNode.equal(node) {
		return Decision{Node: node, Reason: ReasonReferralLoop}
	}
	if chain.Visited(node) {
		return Decision{Node: node, Reason: ReasonReferralLoop}
	}
	if !p.hostAllowed(ref.Host, ref.Port) {
		return Decision{Node: node, Reason: ReasonHostNotWhitelisted}
	}
	if !transportAllowed(origin.Transport, ref) {
		return Decision{Node: node, Reason: ReasonDowngrade}
	}
	if !dnWithin(ref.DN, origin.SearchBase) {
		return Decision{Node: node, Reason: ReasonDNOutOfScope}
	}

	// Credentials: anonymous stays anonymous; otherwise the bound identity
	// may be presented for exactly one rebind (the first followed hop). Every
	// later hop is still followed on its own merits but must rebind
	// anonymously — the password must never be forwarded down the chain.
	rebind := Identity{}
	if !origin.Bind.Anonymous() {
		if !chain.credentialUsed {
			rebind = origin.Bind
		}
	}

	return Decision{Follow: true, Reason: ReasonFollow, Node: node, Rebind: rebind}
}

// DecideAll evaluates a referral list in order. It returns the first
// followable decision, or all rejection decisions if none can be followed.
func (p Policy) DecideAll(origin Origin, chain *Chain, referrals []string) []Decision {
	if chain == nil {
		chain = NewChain()
	}
	out := make([]Decision, 0, len(referrals))
	for _, raw := range referrals {
		d := p.Decide(origin, chain, raw)
		out = append(out, d)
		if d.Follow {
			return out
		}
	}
	return out
}

// Advance records a followed referral so subsequent decisions enforce depth,
// loop detection and the single-rebind limit. It does nothing for a decision
// that was not followed.
func (c *Chain) Advance(d Decision) *Chain {
	if !d.Follow {
		return c
	}
	c.visited = append(c.visited, d.Node)
	if !d.Rebind.Anonymous() {
		c.credentialUsed = true
	}
	return c
}

// hostAllowed checks host/port against the whitelist. A bare whitelist host
// matches any port; a host:port entry must match both. Comparison is
// case-insensitive with default ports normalized.
func (p Policy) hostAllowed(host string, port int) bool {
	for _, entry := range p.AllowedHosts {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		allowedHost, allowedPort := entry, 0
		if h, prt, err := splitHostPort(entry); err == nil {
			allowedHost, allowedPort = h, prt
		}
		if allowedHost != host {
			continue
		}
		if allowedPort == 0 || allowedPort == port {
			return true
		}
	}
	return false
}

func splitHostPort(entry string) (string, int, error) {
	idx := strings.LastIndex(entry, ":")
	if idx <= 0 || idx == len(entry)-1 {
		return "", 0, errors.New("no port")
	}
	prt, err := parsePort(entry[idx+1:])
	if err != nil {
		return "", 0, err
	}
	return entry[:idx], prt, nil
}

// transportAllowed enforces the no-downgrade rule: ldaps is always accepted;
// ldap+StartTLS only when the original connection also used StartTLS;
// cleartext ldap is never followed from an encrypted connection.
func transportAllowed(origin Security, ref Referral) bool {
	switch {
	case ref.Scheme == "ldaps":
		return true
	case ref.Scheme == "ldap" && ref.StartTLS:
		return origin == SecurityStartTLS
	default:
		return false
	}
}
