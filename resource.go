package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	// InvalidTarget is the OAuth error code for a resource that is malformed
	// or is not part of the resource set approved by the authorization server.
	InvalidTarget = "invalid_target"
	// InvalidScope is returned when a token request asks for scopes outside
	// the intersection approved for its resources.
	InvalidScope = "invalid_scope"
)

var (
	// ErrInvalidTarget is a sentinel suitable for errors.Is checks.
	ErrInvalidTarget = errors.New(InvalidTarget)
	// ErrInvalidScope is a sentinel suitable for errors.Is checks.
	ErrInvalidScope = errors.New(InvalidScope)
)

// Error is an OAuth-style protocol error produced while resolving resources
// or issuing a token.
type Error struct {
	Code        string
	Description string
	Resource    string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Resource != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Description, e.Resource)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Description)
}

// Is lets errors.Is(err, ErrInvalidTarget) and
// errors.Is(err, ErrInvalidScope) match protocol errors by code.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrInvalidTarget:
		return e.Code == InvalidTarget
	case ErrInvalidScope:
		return e.Code == InvalidScope
	default:
		return false
	}
}

// IsInvalidTarget reports whether err is an RFC 8707 invalid_target error.
func IsInvalidTarget(err error) bool {
	var resourceErr *Error
	return errors.As(err, &resourceErr) && resourceErr.Code == InvalidTarget
}

// ValidateResource checks the RFC 8707 syntax constraints for one resource
// parameter value. The value must be an absolute URI and must not contain a
// fragment component. Validation deliberately does not normalize the value:
// resource identifiers are compared byte-for-byte later in the flow.
func ValidateResource(value string) error {
	if value == "" {
		return &Error{Code: InvalidTarget, Description: "resource must not be empty"}
	}
	if strings.TrimSpace(value) != value || hasURIWhitespace(value) {
		return &Error{Code: InvalidTarget, Description: "resource must not contain whitespace", Resource: value}
	}
	// url.Parse retains an empty fragment as Fragment == "", so check the
	// delimiter itself. This rejects both "https://host/#fragment" and
	// "https://host/#".
	if strings.Contains(value, "#") {
		return &Error{Code: InvalidTarget, Description: "resource must not contain a fragment", Resource: value}
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return &Error{Code: InvalidTarget, Description: "resource must be an absolute URI", Resource: value}
	}
	if !parsed.IsAbs() {
		return &Error{Code: InvalidTarget, Description: "resource must be an absolute URI", Resource: value}
	}
	return nil
}

func hasURIWhitespace(value string) bool {
	for _, r := range value {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return true
		}
	}
	return false
}

// IsAbsoluteResourceURI reports whether value is an absolute URI without a
// fragment component. It is a convenience predicate for ValidateResource.
func IsAbsoluteResourceURI(value string) bool {
	return ValidateResource(value) == nil
}

// ParseResources validates all repeated resource parameter values and
// canonicalizes them as a set. The first occurrence of each exact string is
// preserved; no URI normalization is performed.
func ParseResources(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(values))
	resources := make([]string, 0, len(values))
	for _, value := range values {
		if err := ValidateResource(value); err != nil {
			return nil, err
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		resources = append(resources, value)
	}
	return resources, nil
}

// ValidateResources is an alias for ParseResources.
func ValidateResources(values []string) ([]string, error) {
	return ParseResources(values)
}

// ValidateResourceURI is an alias for ValidateResource.
func ValidateResourceURI(value string) error {
	return ValidateResource(value)
}

// ParseResource is an alias for ParseResources for callers documenting the
// singular OAuth parameter name.
func ParseResource(values []string) ([]string, error) {
	return ParseResources(values)
}

// ParseResourceValues is an alias for ParseResources.
func ParseResourceValues(values []string) ([]string, error) {
	return ParseResources(values)
}

// ParseResourceParameter extracts all repeated resource values from url.Values
// and validates them using ParseResources.
func ParseResourceParameter(values url.Values) ([]string, error) {
	return ParseResources(values["resource"])
}

// Audience is the audience (aud) of an issued token. It marshals as a JSON
// string for one resource and as a JSON array for zero or multiple resources.
// It is also the only type a resource server should use for an exact aud
// comparison.
type Audience []string

// NewAudience returns an audience containing the supplied resource values.
// Callers normally obtain an Audience from Approval.Resolve.
func NewAudience(resources []string) Audience {
	return Audience(append([]string(nil), resources...))
}

// Values returns a copy of the audience members.
func (a Audience) Values() []string {
	return append([]string(nil), a...)
}

// Contains reports whether resource is exactly one of the token's audience
// values. This is intentionally an equality check, not a prefix or host
// suffix check.
func (a Audience) Contains(resource string) bool {
	for _, member := range a {
		if member == resource {
			return true
		}
	}
	return false
}

// MarshalJSON encodes a one-element audience as a string and any other
// audience as an array, matching common OAuth JWT aud representations.
func (a Audience) MarshalJSON() ([]byte, error) {
	if len(a) == 1 {
		return json.Marshal(a[0])
	}
	if a == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(a))
}

// UnmarshalJSON accepts either a JSON string or a JSON array of strings.
func (a *Audience) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return errors.New("aud must be a string or an array of strings")
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = Audience{single}
		return nil
	}

	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return errors.New("aud must be a string or an array of strings")
	}
	*a = Audience(multiple)
	return nil
}

// AudienceMatches reports whether aud contains the resource server's own
// identifier as an exact byte-for-byte member.
func AudienceMatches(aud Audience, resourceServerID string) bool {
	return aud.Contains(resourceServerID)
}

// ResourceServerAccepts reports whether the resource server identified by
// resourceServerID should accept a token with aud. No prefix matching,
// host suffix matching, case folding, or URI normalization is performed.
func ResourceServerAccepts(resourceServerID string, aud Audience) bool {
	return aud.Contains(resourceServerID)
}

// AudienceContains is an alias for Audience.Contains with package-level
// argument order.
func AudienceContains(aud Audience, resource string) bool {
	return aud.Contains(resource)
}

// ExactAudienceMatch is an alias for AudienceMatches.
func ExactAudienceMatch(aud Audience, resourceServerID string) bool {
	return AudienceMatches(aud, resourceServerID)
}

// ScopeIntersection returns the intersection of scope sets, preserving the
// order of the first set. Scope tokens are compared exactly.
func ScopeIntersection(sets ...[]string) []string {
	if len(sets) == 0 {
		return nil
	}

	first := canonicalStrings(sets[0])
	if len(first) == 0 {
		return nil
	}
	if len(sets) == 1 {
		return first
	}

	others := make([]map[string]struct{}, 0, len(sets)-1)
	for _, set := range sets[1:] {
		values := canonicalStrings(set)
		scopeSet := make(map[string]struct{}, len(values))
		for _, scope := range values {
			scopeSet[scope] = struct{}{}
		}
		others = append(others, scopeSet)
	}

	result := make([]string, 0, len(first))
	for _, scope := range first {
		present := true
		for _, set := range others {
			if _, ok := set[scope]; !ok {
				present = false
				break
			}
		}
		if present {
			result = append(result, scope)
		}
	}
	return result
}

// ParseScope splits a space-delimited OAuth scope value into exact scope
// tokens. Surrounding and repeated whitespace is ignored.
func ParseScope(value string) []string {
	return canonicalStrings(strings.Fields(value))
}

// FormatScope renders scopes as a space-delimited OAuth scope value.
func FormatScope(scopes []string) string {
	return strings.Join(scopes, " ")
}

func canonicalStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
