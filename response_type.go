package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// OAuth 2.0 / OpenID Connect response type names.
const (
	ResponseTypeCode    = "code"
	ResponseTypeToken   = "token"
	ResponseTypeIDToken = "id_token"
)

// Reason explains why a response type was accepted or rejected.
type Reason string

const (
	ReasonNone                           Reason = ""
	ReasonEmptyResponseType              Reason = "empty_response_type"
	ReasonMalformedResponseType          Reason = "malformed_response_type"
	ReasonUnsupportedResponseType        Reason = "unsupported_response_type"
	ReasonAccessTokenInFrontend          Reason = "access_token_in_frontend"
	ReasonUnregisteredHybridResponseType Reason = "unregistered_hybrid_response_type"
)

// Sentinel errors suitable for use with errors.Is.
var (
	ErrEmptyResponseType              = errors.New("netsec: empty response_type")
	ErrMalformedResponseType          = errors.New("netsec: malformed response_type")
	ErrUnsupportedResponseType        = errors.New("netsec: unsupported response_type")
	ErrAccessTokenInFrontend          = errors.New("netsec: access token must not be returned to the frontend")
	ErrUnregisteredHybridResponseType = errors.New("netsec: id_token requires a registered hybrid response type")
)

// PolicyError is returned when a response type is not allowed by the policy.
type PolicyError struct {
	Reason       Reason
	ResponseType string
	Detail       string
}

func (e *PolicyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return fmt.Sprintf("netsec: %s: %s (%s)", e.Reason, e.Detail, e.ResponseType)
	}
	return fmt.Sprintf("netsec: %s (%s)", e.Reason, e.ResponseType)
}

// Unwrap lets callers use errors.Is to match a policy failure.
func (e *PolicyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return sentinelForReason(e.Reason)
}

func sentinelForReason(reason Reason) error {
	switch reason {
	case ReasonEmptyResponseType:
		return ErrEmptyResponseType
	case ReasonMalformedResponseType:
		return ErrMalformedResponseType
	case ReasonUnsupportedResponseType:
		return ErrUnsupportedResponseType
	case ReasonAccessTokenInFrontend:
		return ErrAccessTokenInFrontend
	case ReasonUnregisteredHybridResponseType:
		return ErrUnregisteredHybridResponseType
	default:
		return nil
	}
}

// Flow identifies the authorization flow selected by a response type.
type Flow string

const (
	FlowAuthorizationCode Flow = "authorization_code"
	FlowHybrid            Flow = "hybrid"
)

// ResponseMode identifies where the authorization response is delivered.
type ResponseMode string

const (
	ResponseModeQuery    ResponseMode = "query"
	ResponseModeFragment ResponseMode = "fragment"
)

// Decision is the result of evaluating a response_type value. A successful
// decision never grants permission to return an access token to the frontend.
type Decision struct {
	Allowed      bool
	ResponseType string
	Components   []string
	Flow         Flow
	ResponseMode ResponseMode
	Reason       Reason
	Err          error
}

func (d Decision) allowed(responseType string, components []string, flow Flow, mode ResponseMode) Decision {
	return Decision{
		Allowed:      true,
		ResponseType: responseType,
		Components:   append([]string(nil), components...),
		Flow:         flow,
		ResponseMode: mode,
	}
}

func (d Decision) denied(raw string, reason Reason, detail string) Decision {
	return Decision{
		Allowed:      false,
		ResponseType: raw,
		Reason:       reason,
		Err: &PolicyError{
			Reason:       reason,
			ResponseType: raw,
			Detail:       detail,
		},
	}
}

type parsedResponseType struct {
	raw        string
	components []string
	set        map[string]struct{}
}

func parseResponseType(raw string) (parsedResponseType, *PolicyError) {
	if strings.TrimSpace(raw) == "" {
		return parsedResponseType{}, &PolicyError{
			Reason:       ReasonEmptyResponseType,
			ResponseType: raw,
		}
	}

	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return parsedResponseType{}, &PolicyError{
				Reason:       ReasonMalformedResponseType,
				ResponseType: raw,
				Detail:       "control characters are not permitted",
			}
		}
	}

	components := strings.Fields(raw)
	set := make(map[string]struct{}, len(components))
	for _, component := range components {
		switch component {
		case ResponseTypeCode, ResponseTypeToken, ResponseTypeIDToken:
		default:
			return parsedResponseType{}, &PolicyError{
				Reason:       ReasonUnsupportedResponseType,
				ResponseType: raw,
				Detail:       "unknown response type " + component,
			}
		}
		if _, duplicate := set[component]; duplicate {
			return parsedResponseType{}, &PolicyError{
				Reason:       ReasonMalformedResponseType,
				ResponseType: raw,
				Detail:       "duplicate response type " + component,
			}
		}
		set[component] = struct{}{}
	}

	return parsedResponseType{
		raw:        raw,
		components: components,
		set:        set,
	}, nil
}

// canonicalOrder puts components in the OpenID Connect order and returns a
// stable key suitable for comparing response type sets.
func canonicalOrder(components []string) []string {
	order := map[string]int{
		ResponseTypeCode:    0,
		ResponseTypeIDToken: 1,
		ResponseTypeToken:   2,
	}
	ordered := append([]string(nil), components...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return order[ordered[i]] < order[ordered[j]]
	})
	return ordered
}

func canonicalResponseType(components []string) string {
	return strings.Join(canonicalOrder(components), " ")
}

// Policy contains the response types registered for hybrid authorization.
// Response types containing "token" are never registered: those values are
// rejected unconditionally so an access token can never reach the frontend.
type Policy struct {
	mu         sync.RWMutex
	registered map[string]struct{}
}

// NewPolicy creates a policy from zero or more registered response types.
// Entries that would place an access token in the frontend, or that are not
// valid hybrid response types, are ignored.
func NewPolicy(registeredHybrids ...string) *Policy {
	p := &Policy{registered: make(map[string]struct{})}
	p.Register(registeredHybrids...)
	return p
}

// NewPolicyFrom creates a policy from a slice of registered response types.
func NewPolicyFrom(registeredHybrids []string) *Policy {
	return NewPolicy(registeredHybrids...)
}

// Register adds response types to the set of registered hybrid modes. Only
// "code id_token" is registrable: "code" stands alone, and anything containing
// "token" must never be returned to the frontend.
func (p *Policy) Register(registeredHybrids ...string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.registered == nil {
		p.registered = make(map[string]struct{})
	}
	for _, raw := range registeredHybrids {
		parsed, err := parseResponseType(raw)
		if err != nil {
			continue
		}
		if _, hasToken := parsed.set[ResponseTypeToken]; hasToken {
			continue
		}
		if _, hasCode := parsed.set[ResponseTypeCode]; !hasCode {
			continue
		}
		if _, hasIDToken := parsed.set[ResponseTypeIDToken]; !hasIDToken {
			continue
		}
		p.registered[canonicalResponseType(parsed.components)] = struct{}{}
	}
}

// Registered reports whether a canonical hybrid response type is registered.
func (p *Policy) Registered(responseType string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.registered[responseType]
	return ok
}

// Evaluate applies the policy to a response_type value.
func (p *Policy) Evaluate(raw string) Decision {
	parsed, parseErr := parseResponseType(raw)
	if parseErr != nil {
		return Decision{}.denied(raw, parseErr.Reason, parseErr.Detail)
	}

	if _, hasToken := parsed.set[ResponseTypeToken]; hasToken {
		return Decision{}.denied(raw, ReasonAccessTokenInFrontend,
			"response_type contains token; access tokens must not be delivered in a redirect")
	}

	_, hasCode := parsed.set[ResponseTypeCode]
	_, hasIDToken := parsed.set[ResponseTypeIDToken]

	switch {
	case hasCode && !hasIDToken:
		return Decision{}.allowed(ResponseTypeCode, []string{ResponseTypeCode},
			FlowAuthorizationCode, ResponseModeQuery)

	case hasCode && hasIDToken:
		canonical := canonicalResponseType(parsed.components)
		if !p.Registered(canonical) {
			return Decision{}.denied(raw, ReasonUnregisteredHybridResponseType,
				"hybrid response type is not registered for this client")
		}
		return Decision{}.allowed(canonical, canonicalOrder(parsed.components),
			FlowHybrid, ResponseModeFragment)

	case hasIDToken:
		return Decision{}.denied(raw, ReasonUnregisteredHybridResponseType,
			"id_token is only permitted in a registered code id_token hybrid response")
	}

	return Decision{}.denied(raw, ReasonUnsupportedResponseType, "no usable response type")
}

// Evaluate applies a fresh policy to a response_type value.
func Evaluate(raw string, registeredHybrids ...string) Decision {
	return NewPolicy(registeredHybrids...).Evaluate(raw)
}

// ValidateResponseType applies a fresh policy to a response_type value using a
// slice of registered hybrid modes.
func ValidateResponseType(raw string, registeredHybrids []string) Decision {
	return NewPolicyFrom(registeredHybrids).Evaluate(raw)
}

// IsAllowed reports whether the response type passes the policy.
func IsAllowed(raw string, registeredHybrids ...string) bool {
	return Evaluate(raw, registeredHybrids...).Allowed
}
