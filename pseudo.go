package netsec

import (
	"errors"
	"strings"
)

// ErrInvalidPseudoHeader is returned when a promised header block fails the
// RFC 7540 section 8.1.2/8.2 checks. Any rejection with this error happens
// before any per-stream state (including the stream identifier itself) is
// retained, so a malformed promise can never reserve or consume resources.
var ErrInvalidPseudoHeader = errors.New("netsec: invalid pseudo-header block")

// HeaderField is one entry of an HTTP/2 header block. Names must be lower
// case (pseudo-header names begin with ':'); values are taken verbatim and
// validated by the controller rather than normalized away.
type HeaderField struct {
	Name  string
	Value string
}

// ValidatedRequest is the sanitized view of a promised request. It is only
// constructed after every pseudo-header and connection-specific header check
// has passed, and its Origin is canonicalized with the same routine used for
// authorization origins.
type ValidatedRequest struct {
	Method    string
	Scheme    string
	Authority string
	Path      string
	Origin    Origin
	Headers   []HeaderField
}

// connectionSpecificHeaders are hop-by-hop headers that RFC 7540 section
// 8.1.2.2 forbids from appearing in an HTTP/2 message.
var connectionSpecificHeaders = map[string]bool{
	"connection":        true,
	"keep-alive":        true,
	"proxy-connection":  true,
	"transfer-encoding": true,
	"upgrade":           true,
	"te":                true,
}

var defaultSafeMethods = map[string]bool{
	"GET":     true,
	"HEAD":    true,
	"OPTIONS": true,
	"TRACE":   true,
}

// ValidatePseudoHeader applies the default admission rules to a promised
// header block: only safe methods and the http/https schemes are accepted.
func ValidatePseudoHeader(block []HeaderField) (ValidatedRequest, error) {
	return validateHeaderBlock(block, defaultSafeMethods, nil)
}

func validateHeaderBlock(block []HeaderField, safeMethods, schemes map[string]bool) (ValidatedRequest, error) {
	if len(block) == 0 {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}

	var (
		method     string
		scheme     string
		authority  string
		path       string
		seen       = map[string]bool{}
		pseudoDone bool
		hostValue  string
		hostSeen   bool
		regular    []HeaderField
	)

	for _, field := range block {
		if !validFieldValue(field.Value) {
			return ValidatedRequest{}, ErrInvalidPseudoHeader
		}

		if strings.HasPrefix(field.Name, ":") {
			// Pseudo-header names are a fixed closed set checked in the
			// switch below; the generic token rule applies to regular names.
			if pseudoDone {
				return ValidatedRequest{}, ErrInvalidPseudoHeader
			}
			if seen[field.Name] {
				return ValidatedRequest{}, ErrInvalidPseudoHeader
			}
			seen[field.Name] = true
			switch field.Name {
			case ":method":
				if field.Value == "" || !isToken(field.Value) {
					return ValidatedRequest{}, ErrInvalidPseudoHeader
				}
				method = field.Value
			case ":scheme":
				if field.Value == "" || !isScheme(field.Value) {
					return ValidatedRequest{}, ErrInvalidPseudoHeader
				}
				scheme = strings.ToLower(field.Value)
			case ":authority":
				authority = field.Value
			case ":path":
				path = field.Value
			default:
				// Unknown pseudo-headers are not part of the request target.
				return ValidatedRequest{}, ErrInvalidPseudoHeader
			}
			continue
		}

		pseudoDone = true
		lowerName := strings.ToLower(field.Name)
		if lowerName == "" || !isToken(lowerName) {
			return ValidatedRequest{}, ErrInvalidPseudoHeader
		}
		if lowerName != field.Name {
			// Upper-case field names are illegal in HTTP/2.
			return ValidatedRequest{}, ErrInvalidPseudoHeader
		}
		if connectionSpecificHeaders[lowerName] {
			return ValidatedRequest{}, ErrInvalidPseudoHeader
		}
		if lowerName == "host" {
			if hostSeen {
				return ValidatedRequest{}, ErrInvalidPseudoHeader
			}
			hostSeen = true
			hostValue = field.Value
		}
		regular = append(regular, field)
	}

	// All four request pseudo-headers are mandatory for a pushed request.
	if !seen[":method"] || !seen[":scheme"] || !seen[":authority"] || !seen[":path"] {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}
	if !safeMethods[method] {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}
	if len(schemes) > 0 && !schemes[scheme] {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}
	if authority == "" {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}
	if !validAuthority(authority) {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}
	if hostSeen && !strings.EqualFold(hostValue, authority) {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}
	if !validPromisedPath(method, path) {
		return ValidatedRequest{}, ErrInvalidPseudoHeader
	}

	target, err := canonicalOrigin(scheme, authority)
	if err != nil {
		return ValidatedRequest{}, errors.Join(ErrInvalidPseudoHeader, err)
	}

	return ValidatedRequest{
		Method:    method,
		Scheme:    scheme,
		Authority: authority,
		Path:      path,
		Origin:    target,
		Headers:   regular,
	}, nil
}

func validPromisedPath(method, path string) bool {
	if path == "" {
		return false
	}
	if strings.ContainsAny(path, " \t\r\n#") {
		return false
	}
	if method == "OPTIONS" && path == "*" {
		return true
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	if strings.HasPrefix(path, "//") {
		return false
	}
	return true
}

func validAuthority(authority string) bool {
	_, _, err := splitAuthority(authority)
	return err == nil
}

func validFieldValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == 0 || c == '\r' || c == '\n' {
			return false
		}
	}
	return true
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7f {
			return false
		}
		switch c {
		case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"',
			'/', '[', ']', '?', '=', '{', '}', ' ':
			return false
		}
	}
	return true
}

// isScheme validates an RFC 3986 scheme: ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ).
func isScheme(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '+' || c == '-' || c == '.':
		default:
			return false
		}
	}
	return true
}
