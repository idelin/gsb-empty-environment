package netsec

import (
	"errors"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
)

// StatusEarlyHints is the informational status code used for early hints.
const StatusEarlyHints = http.StatusEarlyHints

// ErrSensitiveHintField is returned when a caller attempts to add an
// authentication or credential-bearing field to a hint whitelist.
var ErrSensitiveHintField = errors.New("netsec: sensitive field cannot be allowed in early hints")

// sensitiveHintFields are credential-bearing fields that must never be
// forwarded in an early hint, even if a caller tries to widen the policy.
var sensitiveHintFields = map[string]struct{}{
	"Authorization":             {},
	"Proxy-Authorization":       {},
	"Cookie":                    {},
	"Set-Cookie":                {},
	"Www-Authenticate":          {},
	"Proxy-Authenticate":        {},
	"Authentication-Info":       {},
	"Proxy-Authentication-Info": {},
	"X-Api-Key":                 {},
	"X-Auth-Token":              {},
	"X-Amz-Security-Token":      {},
}

// sensitiveHintMarkers catch non-standard credential-bearing names that are
// not enumerated above. They intentionally err on the side of not forwarding
// an unknown authentication field.
var sensitiveHintMarkers = []string{
	"auth",
	"apikey",
	"api-key",
	"cookie",
	"credential",
	"secret",
	"session",
	"signature",
	"token",
}

// defaultHintFieldNames is the conservative set of fields that may appear in
// a forwarded 103 response by default. Link is the resource-hint field
// defined by RFC 8288; Alt-Svc is safe to advertise early as well.
var defaultHintFieldNames = []string{
	"Link",
	"Alt-Svc",
}

// DefaultHintFields returns a copy of the default hint whitelist.
func DefaultHintFields() []string {
	return append([]string(nil), defaultHintFieldNames...)
}

// HintPolicy decides which response fields are allowed in an early hint. A
// policy is immutable after construction.
type HintPolicy struct {
	allowed map[string]struct{}
}

// DefaultHintPolicy returns a policy containing the default hint fields.
func DefaultHintPolicy() *HintPolicy {
	p, err := NewHintPolicy(defaultHintFieldNames...)
	if err != nil {
		panic(err)
	}
	return p
}

// NewHintPolicy builds an explicit whitelist. Authentication-related fields
// are rejected even when explicitly requested.
func NewHintPolicy(fields ...string) (*HintPolicy, error) {
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(field))
		if name == "" || !validHeaderName(name) {
			return nil, ErrInvalidHeader
		}
		if IsSensitiveHintField(name) {
			return nil, ErrSensitiveHintField
		}
		allowed[name] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, errors.New("netsec: empty hint whitelist")
	}
	return &HintPolicy{allowed: allowed}, nil
}

// Allows reports whether name is in the whitelist.
func (p *HintPolicy) Allows(name string) bool {
	if p == nil {
		return false
	}
	canonical := textproto.CanonicalMIMEHeaderKey(name)
	if IsSensitiveHintField(canonical) {
		return false
	}
	_, ok := p.allowed[canonical]
	return ok
}

// Filter returns a copy of h containing only whitelisted fields. The second
// result lists the rejected field names in sorted order. Authentication
// fields are always rejected.
//
// Existing canonical and non-canonical spellings of the same field are
// combined under the canonical name. Values are copied, so the caller may
// reuse or mutate h as soon as Filter returns.
func (p *HintPolicy) Filter(h http.Header) (http.Header, []string) {
	out := make(http.Header)
	if p == nil {
		return out, headerNames(h)
	}

	droppedSet := make(map[string]struct{})
	for key, values := range h {
		name := textproto.CanonicalMIMEHeaderKey(key)
		if !p.Allows(name) {
			if name == "" {
				name = key
			}
			droppedSet[name] = struct{}{}
			continue
		}
		for _, value := range values {
			if !validHeaderValue(value) {
				droppedSet[name] = struct{}{}
				continue
			}
			out[name] = append(out[name], value)
		}
	}

	dropped := make([]string, 0, len(droppedSet))
	for name := range droppedSet {
		dropped = append(dropped, name)
	}
	sort.Strings(dropped)
	return out, dropped
}

// IsSensitiveHintField reports whether name is a known authentication or
// credential-bearing response field.
func IsSensitiveHintField(name string) bool {
	canonical := textproto.CanonicalMIMEHeaderKey(name)
	if _, ok := sensitiveHintFields[canonical]; ok {
		return true
	}
	lower := strings.ToLower(canonical)
	for _, marker := range sensitiveHintMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func cloneHeader(h http.Header) (http.Header, error) {
	out := make(http.Header, len(h))
	for key, values := range h {
		name := textproto.CanonicalMIMEHeaderKey(key)
		if name == "" || !validHeaderName(name) {
			return nil, ErrInvalidHeader
		}
		for _, value := range values {
			if !validHeaderValue(value) {
				return nil, ErrInvalidHeader
			}
			out[name] = append(out[name], value)
		}
		if len(values) == 0 {
			out[name] = nil
		}
	}
	return out, nil
}

func headerNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for key := range h {
		name := textproto.CanonicalMIMEHeaderKey(key)
		if name == "" {
			name = key
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
