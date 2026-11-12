package netsec

import (
	"net"
)

// CookieStatus is the result of inspecting a DNS request's Cookie option.
type CookieStatus uint8

const (
	// CookieValid means a 16-byte Server Cookie matched the key, client IP,
	// version, and timestamp window.
	CookieValid CookieStatus = iota

	// CookieMissingServer means a valid 8-byte Client Cookie was present but
	// the request had no Server Cookie. The caller must send a TC challenge.
	CookieMissingServer

	// CookieInvalid means the Cookie option was absent, malformed, or failed
	// validation. The caller must send BADCOOKIE.
	CookieInvalid
)

// Compatibility aliases with more explicit names.
const (
	StatusValid               = CookieValid
	StatusMissingServer       = CookieMissingServer
	StatusInvalid             = CookieInvalid
	StatusMissingServerCookie = CookieMissingServer
)

// Decision describes a Cookie gate decision.
type Decision struct {
	Status CookieStatus

	// ClientCookie is the 8-byte Client Cookie found in the request, if any.
	ClientCookie []byte
	// ServerCookie is the Server Cookie presented by the client, if any. It
	// has not necessarily been validated when Status is CookieInvalid.
	ServerCookie []byte
	// FreshServerCookie is a replacement Server Cookie generated for a
	// BADCOOKIE response. It is nil when no valid Client Cookie was available.
	FreshServerCookie []byte
	// Reason is the validation error for CookieInvalid.
	Reason error
}

// Gate is an alias for CookieGate.
type Gate = CookieGate

// NewGate is an alias for NewCookieGate.
func NewGate(key []byte, options ...CookieGateOption) (*CookieGate, error) {
	return NewCookieGate(key, options...)
}

// Inspect parses a DNS request, validates its Cookie option against clientIP,
// and returns the policy decision without constructing a response.
func (g *CookieGate) Inspect(message []byte, clientIP net.IP) (Decision, error) {
	decision, _, err := g.inspect(message, clientIP)
	return decision, err
}

// Check parses and validates a DNS request and returns a challenge response
// when the request must not reach the application:
//
//   - CookieValid: the response is nil and the request may be resolved.
//   - CookieMissingServer: the response has TC set, no additional section, and
//     is never larger than the request (UDP amplification guard).
//   - CookieInvalid: the response has extended RCODE BADCOOKIE and, when
//     possible, a fresh Server Cookie bound to clientIP.
//
// A malformed DNS message returns ErrMalformedDNS and no response.
func (g *CookieGate) Check(message []byte, clientIP net.IP) (Decision, []byte, error) {
	decision, parsed, err := g.inspect(message, clientIP)
	if err != nil {
		return decision, nil, err
	}

	switch decision.Status {
	case CookieValid:
		return decision, nil, nil
	case CookieMissingServer:
		return decision, buildTCResponse(message, parsed, len(message)), nil
	default:
		return decision, buildBadCookieResponse(
			message,
			parsed,
			decision.ClientCookie,
			decision.FreshServerCookie,
		), nil
	}
}

// CheckRequest is an alias for Check.
func (g *CookieGate) CheckRequest(message []byte, clientIP net.IP) (Decision, []byte, error) {
	return g.Check(message, clientIP)
}

func (g *CookieGate) inspect(message []byte, clientIP net.IP) (Decision, parsedMessage, error) {
	decision := Decision{Status: CookieInvalid}
	parsed, err := parseMessage(message)
	if err != nil {
		decision.Reason = err
		return decision, parsed, err
	}
	decision.ClientCookie = append([]byte(nil), parsed.client...)
	decision.ServerCookie = append([]byte(nil), parsed.server...)

	if _, err := ipBytes(clientIP); err != nil {
		decision.Reason = err
		return decision, parsed, err
	}
	if !parsed.hasCookie || len(parsed.client) != ClientCookieLen {
		decision.Status = CookieInvalid
		decision.Reason = ErrClientCookieLength
		return decision, parsed, nil
	}
	if len(parsed.server) == 0 {
		decision.Status = CookieMissingServer
		return decision, parsed, nil
	}
	if len(parsed.server) != ServerCookieLen {
		decision.Status = CookieInvalid
		decision.Reason = ErrServerCookieLength
		decision.FreshServerCookie, _ = g.GenerateServerCookie(parsed.client, clientIP)
		return decision, parsed, nil
	}
	if err := g.ValidateServerCookie(parsed.client, parsed.server, clientIP); err != nil {
		decision.Status = CookieInvalid
		decision.Reason = err
		decision.FreshServerCookie, _ = g.GenerateServerCookie(parsed.client, clientIP)
		return decision, parsed, nil
	}

	decision.Status = CookieValid
	return decision, parsed, nil
}
