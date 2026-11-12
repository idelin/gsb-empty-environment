package netsec

import (
	"errors"
	"strings"
)

// SchemePrefix is the fixed scheme of every SPIFFE ID, including the
// scheme separator ("spiffe://").
const SchemePrefix = "spiffe://"

// SPIFFEID is a parsed SPIFFE ID as defined by the SPIFFE specification
// (https://github.com/spiffe/spiffe/blob/main/standards/SPIFFE.md).
//
// The zero value is not a valid SPIFFE ID; use ParseSPIFFEID.
type SPIFFEID struct {
	trustDomain string
	path        string
}

// TrustDomain returns the lower-cased trust domain of the SPIFFE ID.
func (id SPIFFEID) TrustDomain() string { return id.trustDomain }

// Path returns the workload path, including the leading slash. It is empty
// when the SPIFFE ID contains no path.
func (id SPIFFEID) Path() string { return id.path }

// String returns the canonical representation of the SPIFFE ID.
func (id SPIFFEID) String() string {
	if id.trustDomain == "" {
		return ""
	}
	return SchemePrefix + id.trustDomain + id.path
}

var (
	errInvalidID      = errors.New("netsec: invalid SPIFFE ID")
	errUpperTrustDom  = errors.New("netsec: trust domain must be lower case")
	errBadTrustDomain = errors.New("netsec: invalid trust domain")
	errBadPath        = errors.New("netsec: invalid SPIFFE ID path")
)

// ParseSPIFFEID parses and validates s as a SPIFFE ID.
//
// The validation rules are stricter than generic URL parsing:
//   - the value must start with the literal "spiffe://" scheme;
//   - the trust domain must be non-empty, lower case, and contain only
//     letters, digits, dots, hyphens and underscores;
//   - user info, ports, query strings and fragments are rejected;
//   - the path, when present, must begin with "/" and its segments must be
//     non-empty, contain only pchar characters, and must not be "." or "..";
//   - a trailing slash is rejected.
func ParseSPIFFEID(s string) (SPIFFEID, error) {
	if s == "" {
		return SPIFFEID{}, errInvalidID
	}
	// Reject anything a URL authority/path must not contain, which also
	// covers query and fragment components up front.
	if strings.ContainsAny(s, " \t\r\n") {
		return SPIFFEID{}, errInvalidID
	}
	if !strings.HasPrefix(s, SchemePrefix) {
		return SPIFFEID{}, errInvalidID
	}
	rest := s[len(SchemePrefix):]
	if rest == "" {
		return SPIFFEID{}, errBadTrustDomain
	}

	authority := rest
	path := ""
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		authority = rest[:slash]
		path = rest[slash:]
	}
	if authority == "" {
		return SPIFFEID{}, errBadTrustDomain
	}
	// User info and port are not permitted in a SPIFFE trust domain.
	if strings.ContainsAny(authority, "@:?#") {
		return SPIFFEID{}, errBadTrustDomain
	}
	if strings.ToLower(authority) != authority {
		return SPIFFEID{}, errUpperTrustDom
	}
	if !validTrustDomain(authority) {
		return SPIFFEID{}, errBadTrustDomain
	}

	if path != "" {
		if err := validatePath(path); err != nil {
			return SPIFFEID{}, err
		}
	}

	return SPIFFEID{trustDomain: authority, path: path}, nil
}

func validTrustDomain(td string) bool {
	if td == "" || strings.HasPrefix(td, ".") || strings.HasSuffix(td, ".") ||
		strings.Contains(td, "..") || strings.HasPrefix(td, "-") || strings.HasSuffix(td, "-") {
		return false
	}
	for i := 0; i < len(td); i++ {
		c := td[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

func validatePath(path string) error {
	if !strings.HasPrefix(path, "/") {
		return errBadPath
	}
	if path == "/" || strings.HasSuffix(path, "/") {
		return errBadPath
	}
	segments := strings.Split(path[1:], "/")
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return errBadPath
		}
		if err := validatePathSegment(seg); err != nil {
			return err
		}
	}
	return nil
}

// validatePathSegment checks an RFC 3986 pchar sequence: unreserved
// characters, sub-delims, ":" and "@", plus valid percent-encoding.
func validatePathSegment(seg string) error {
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if c == '%' {
			if i+2 >= len(seg) || !isHex(seg[i+1]) || !isHex(seg[i+2]) {
				return errBadPath
			}
			// Percent-encoding must not smuggle structural characters
			// (slash, backslash, query/fragment separators) into a segment.
			decoded := unhex(seg[i+1])<<4 | unhex(seg[i+2])
			switch decoded {
			case '/', '\\', '?', '#':
				return errBadPath
			}
			i += 2
			continue
		}
		switch {
		case isUnreserved(c):
		case isSubDelim(c):
		case c == ':' || c == '@':
		default:
			return errBadPath
		}
	}
	return nil
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
	case c >= 'A' && c <= 'Z':
	case c >= '0' && c <= '9':
	case c == '-' || c == '.' || c == '_' || c == '~':
	default:
		return false
	}
	return true
}

func isSubDelim(c byte) bool {
	switch c {
	case '!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=':
		return true
	}
	return false
}
