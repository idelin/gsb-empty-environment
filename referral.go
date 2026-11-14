package netsec

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Scheme is an LDAP URL scheme.
type Scheme string

const (
	SchemeLDAP  Scheme = "ldap"
	SchemeLDAPS Scheme = "ldaps"
)

// TLSMode describes how the transport for a referral is protected.
type TLSMode string

const (
	// TLSModeNone is a plaintext LDAP transport. It is only used when the
	// configuration explicitly permits cleartext.
	TLSModeNone TLSMode = "none"
	// TLSModeLDAPS is TLS from the first byte (ldaps://).
	TLSModeLDAPS TLSMode = "ldaps"
	// TLSModeStartTLS upgrades a plain LDAP connection with the StartTLS
	// extended operation before any bind is sent.
	TLSModeStartTLS TLSMode = "starttls"
)

// Scope is the LDAP search scope.
type Scope int

const (
	ScopeBase Scope = iota
	ScopeOne
	ScopeSub
)

func (s Scope) String() string {
	switch s {
	case ScopeBase:
		return "base"
	case ScopeOne:
		return "one"
	case ScopeSub:
		return "sub"
	default:
		return "unknown"
	}
}

func parseScope(s string) (Scope, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "base":
		return ScopeBase, true
	case "one":
		return ScopeOne, true
	case "sub":
		return ScopeSub, true
	default:
		return ScopeBase, false
	}
}

// Referral is a parsed LDAP referral URL.
//
// Host is normalized (lower case, no trailing dot, IPv6 without brackets) and
// Port is always populated. DN is percent-decoded. HasScope and HasFilter
// distinguish an omitted LDAP URL field from an explicitly empty one.
type Referral struct {
	Raw        string
	Scheme     Scheme
	Host       string
	Port       int
	DN         string
	Attributes []string
	Scope      Scope
	HasScope   bool
	Filter     string
	HasFilter  bool
	Extensions []string
}

// DefaultPort returns the registered default port for an LDAP scheme.
func DefaultPort(s Scheme) int {
	if s == SchemeLDAPS {
		return 636
	}
	return 389
}

// ParseReferral parses an RFC 4516 LDAP URL.
func ParseReferral(raw string) (Referral, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Referral{}, fmt.Errorf("%w: empty URL", ErrMalformedReferral)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Referral{}, fmt.Errorf("%w: %v", ErrMalformedReferral, err)
	}
	if u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return Referral{}, fmt.Errorf("%w: opaque URLs, userinfo, and fragments are not allowed", ErrMalformedReferral)
	}

	scheme := Scheme(strings.ToLower(u.Scheme))
	if scheme != SchemeLDAP && scheme != SchemeLDAPS {
		return Referral{}, fmt.Errorf("%w: %q", ErrUnsupportedScheme, u.Scheme)
	}

	rawHost := u.Hostname()
	if rawHost == "" {
		return Referral{}, fmt.Errorf("%w: missing referral host", ErrMalformedReferral)
	}
	host, err := NormalizeHost(rawHost)
	if err != nil {
		return Referral{}, fmt.Errorf("%w: %v", ErrMalformedReferral, err)
	}

	port := DefaultPort(scheme)
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return Referral{}, fmt.Errorf("%w: invalid port %q", ErrMalformedReferral, p)
		}
		port = n
	}

	dn := ""
	if path := u.EscapedPath(); path != "" {
		path = strings.TrimPrefix(path, "/")
		dn, err = url.PathUnescape(path)
		if err != nil {
			return Referral{}, fmt.Errorf("%w: invalid DN escaping: %v", ErrMalformedReferral, err)
		}
	}

	ref := Referral{
		Raw:    raw,
		Scheme: scheme,
		Host:   host,
		Port:   port,
		DN:     dn,
	}

	// LDAP URLs use '?' as the separator between DN, attributes, scope,
	// filter, and extensions. net/url treats only the first '?' as the query
	// introducer, so split RawQuery ourselves.
	fields := strings.SplitN(u.RawQuery, "?", 5)
	if len(fields) >= 1 && fields[0] != "" {
		for _, attr := range strings.Split(fields[0], ",") {
			attr = strings.TrimSpace(attr)
			if attr == "" {
				continue
			}
			decoded, err := url.PathUnescape(attr)
			if err != nil {
				return Referral{}, fmt.Errorf("%w: invalid attribute escaping: %v", ErrMalformedReferral, err)
			}
			ref.Attributes = append(ref.Attributes, decoded)
		}
	}
	if len(fields) >= 2 && fields[1] != "" {
		decoded, err := url.PathUnescape(fields[1])
		if err != nil {
			return Referral{}, fmt.Errorf("%w: invalid scope escaping: %v", ErrMalformedReferral, err)
		}
		scope, ok := parseScope(decoded)
		if !ok {
			return Referral{}, fmt.Errorf("%w: invalid scope %q", ErrMalformedReferral, decoded)
		}
		ref.Scope = scope
		ref.HasScope = true
	}
	if len(fields) >= 3 && fields[2] != "" {
		decoded, err := url.PathUnescape(fields[2])
		if err != nil {
			return Referral{}, fmt.Errorf("%w: invalid filter escaping: %v", ErrMalformedReferral, err)
		}
		ref.Filter = decoded
		ref.HasFilter = true
	}
	if len(fields) >= 4 && fields[3] != "" {
		for _, ext := range strings.Split(fields[3], ",") {
			ext = strings.TrimSpace(ext)
			if ext == "" {
				continue
			}
			decoded, err := url.PathUnescape(ext)
			if err != nil {
				return Referral{}, fmt.Errorf("%w: invalid extension escaping: %v", ErrMalformedReferral, err)
			}
			ref.Extensions = append(ref.Extensions, decoded)
		}
	}

	if strings.ContainsRune(dn, 0) {
		return Referral{}, fmt.Errorf("%w: NUL in DN", ErrMalformedReferral)
	}
	return ref, nil
}

// NormalizeHost canonicalizes a host for trust comparisons.
//
// It deliberately does not perform DNS resolution. Trust is attached to the
// literal host name, so a DNS alias outside the trust domain cannot borrow the
// trust of the name it resolves to.
func NormalizeHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if strings.ContainsAny(host, " \t\r\n/\\@?#%") {
		return "", fmt.Errorf("invalid character in host %q", host)
	}
	host = strings.TrimSuffix(host, ".")
	host = strings.ToLower(host)
	if host == "" {
		return "", fmt.Errorf("empty host")
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}

	if strings.Contains(host, ":") {
		return "", fmt.Errorf("invalid host %q: port must be in the URL port field", host)
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", fmt.Errorf("invalid host %q", host)
	}
	if len(host) > 253 {
		return "", fmt.Errorf("host is too long")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("invalid host label in %q", host)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid host label in %q", host)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			// Non-ASCII names require IDNA processing. Reject them rather
			// than making a trust decision on an ambiguous spelling.
			return "", fmt.Errorf("invalid character in host %q", host)
		}
	}
	return host, nil
}

// Target is the concrete endpoint and security mode selected for a referral.
type Target struct {
	Scheme Scheme
	Host   string
	Port   int
	DN     string
	Mode   TLSMode
}

// Address returns the dialable host:port pair.
func (t Target) Address() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// String returns a normalized URL-like representation without credentials.
func (t Target) String() string {
	return fmt.Sprintf("%s://%s/%s", t.Scheme, t.Address(), url.PathEscape(t.DN))
}

// Key is the identity used for loop detection. It intentionally covers the
// endpoint and base DN but ignores search parameters: a chain that revisits
// the same endpoint and base is considered a loop even if the filter changes.
func (t Target) Key() string {
	return fmt.Sprintf("%s|%s|%d|%s", t.Scheme, t.Host, t.Port, canonicalDN(t.DN))
}

func canonicalDN(dn string) string {
	var b strings.Builder
	b.Grow(len(dn))
	space := false
	for i := 0; i < len(dn); i++ {
		c := dn[i]
		switch c {
		case '\\':
			if i+1 < len(dn) {
				i++
				b.WriteByte(lowerASCII(dn[i]))
			}
		case ' ', '\t', '\r', '\n':
			space = true
			continue
		case ',', '+', '=', ';':
			s := strings.TrimRight(b.String(), " ")
			b.Reset()
			b.WriteString(s)
			b.WriteByte(c)
			space = false
			continue
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteByte(lowerASCII(c))
		}
	}
	return strings.TrimSpace(b.String())
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// redactReferralForLog avoids copying URL userinfo into audit records or
// errors. Userinfo is rejected during parsing, but malformed URLs can still
// contain credential-looking text.
func redactReferralForLog(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-referral>"
	}
	if u.User != nil {
		u.User = url.User("<redacted>")
	}
	return u.String()
}
