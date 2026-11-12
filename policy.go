package netsec

import (
	"errors"
	"fmt"
	"time"
)

// Mode selects how the resolver treats plaintext DNS when a secure upstream
// fails.
type Mode int

const (
	// Strict only dispatches to authenticated DoH and DoT upstreams. A secure
	// upstream failure is returned to the caller and never falls back to
	// plaintext DNS.
	Strict Mode = iota

	// Opportunistic tries authenticated DoH and DoT upstreams first and may
	// fall back to plaintext DNS. Every fallback is recorded in the result and
	// written to the configured logger.
	Opportunistic
)

const (
	StrictMode        = Strict
	OpportunisticMode = Opportunistic
)

// Policy is an alias for Mode.
type Policy = Mode

const (
	StrictPolicy        = Strict
	OpportunisticPolicy = Opportunistic
)

func (m Mode) String() string {
	switch m {
	case Strict:
		return "strict"
	case Opportunistic:
		return "opportunistic"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// Protocol identifies an upstream transport.
type Protocol string

const (
	DoH      Protocol = "doh"
	DoT      Protocol = "dot"
	PlainDNS Protocol = "plain"
)

const (
	TransportDoH      = DoH
	TransportDoT      = DoT
	TransportPlainDNS = PlainDNS
)

// Transport is an alias for Protocol.
type Transport = Protocol

func (p Protocol) secure() bool {
	return p == DoH || p == DoT
}

var (
	// ErrNoUpstreams is returned when a selector is created without upstreams.
	ErrNoUpstreams = errors.New("netsec: no upstreams configured")
	// ErrNoSecureUpstream is returned when strict mode has no DoH or DoT
	// upstream available.
	ErrNoSecureUpstream = errors.New("netsec: no secure upstream configured")
	// ErrNoPlainFallback is returned when opportunistic mode needs a plaintext
	// fallback but none is configured.
	ErrNoPlainFallback = errors.New("netsec: no plaintext fallback configured")
	// ErrStrictNoFallback marks a strict-mode failure. Strict mode never
	// dispatches the query to a plaintext upstream.
	ErrStrictNoFallback = errors.New("netsec: strict policy forbids plaintext fallback")
	// ErrInsecureDoHURL is returned for a DoH URL that does not use HTTPS.
	ErrInsecureDoHURL = errors.New("netsec: DoH URL must use https")
	// ErrMissingAuthName is returned when a secure upstream has no certificate
	// authentication name.
	ErrMissingAuthName = errors.New("netsec: secure upstream requires an authentication name")
	// ErrMissingSPKIPin is returned when strict mode has no caller-supplied
	// SPKI pin for a secure upstream.
	ErrMissingSPKIPin = errors.New("netsec: strict secure upstream requires an SPKI pin")
	// ErrInvalidSPKIPin is returned for a malformed SPKI pin.
	ErrInvalidSPKIPin = errors.New("netsec: invalid SPKI pin")
	// ErrSPKIPinMismatch is returned when no presented certificate matches a
	// configured SPKI pin.
	ErrSPKIPinMismatch = errors.New("netsec: TLS SPKI pin mismatch")
	// ErrContentType is returned when a DoH response is not
	// application/dns-message.
	ErrContentType = errors.New("netsec: DoH response content type is not application/dns-message")
	// ErrQuestionMismatch is returned when a response question section differs
	// from the query.
	ErrQuestionMismatch = errors.New("netsec: response question section does not match request")
	// ErrResponseIDMismatch is returned when a response DNS transaction ID
	// differs from the query.
	ErrResponseIDMismatch = errors.New("netsec: response transaction ID does not match request")
	// ErrMalformedMessage is returned for an unparseable DNS message.
	ErrMalformedMessage = errors.New("netsec: malformed DNS message")
	// ErrNotResponse is returned when a reply does not have the QR bit set.
	ErrNotResponse = errors.New("netsec: DNS message is not a response")
)

// Attempt records one upstream exchange.
type Attempt struct {
	Upstream string
	Protocol Protocol
	Duration time.Duration
	Err      error
}

// Degradation records an opportunistic plaintext fallback.
type Degradation struct {
	From     string
	To       string
	Protocol Protocol
	Reason   error
	At       time.Time
	Attempts []Attempt
}

// Result is the successful outcome of a query, including whether the answer
// came from a plaintext fallback.
type Result struct {
	Message     []byte
	Upstream    Upstream
	Protocol    Protocol
	Degraded    bool
	Degradation *Degradation
	Attempts    []Attempt
}

// ExchangeError reports that no upstream produced an acceptable response.
type ExchangeError struct {
	Mode     Mode
	Attempts []Attempt
}

func (e *ExchangeError) Error() string {
	if len(e.Attempts) == 0 {
		return fmt.Sprintf("netsec: %s resolver failed", e.Mode)
	}
	return fmt.Sprintf("netsec: %s resolver failed after %d attempt(s): %v",
		e.Mode, len(e.Attempts), e.Attempts[len(e.Attempts)-1].Err)
}

// Unwrap exposes the policy sentinel and all attempt errors to errors.Is and
// errors.As.
func (e *ExchangeError) Unwrap() []error {
	errs := make([]error, 0, len(e.Attempts)+2)
	switch e.Mode {
	case Strict:
		errs = append(errs, ErrStrictNoFallback)
	case Opportunistic:
		hasPlain := false
		for _, attempt := range e.Attempts {
			if attempt.Protocol == PlainDNS {
				hasPlain = true
				break
			}
		}
		if !hasPlain {
			errs = append(errs, ErrNoPlainFallback)
		}
	}
	for _, attempt := range e.Attempts {
		if attempt.Err != nil {
			errs = append(errs, attempt.Err)
		}
	}
	return errs
}
