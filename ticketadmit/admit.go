package ticketadmit

import (
	"time"
)

// MaxTicketAge is the maximum permitted ticket age (RFC 8446 ticket lifetime
// ceiling). Older tickets are refused and the server must fall back to a full
// handshake.
const MaxTicketAge = 7 * 24 * time.Hour

// MaxEarlyDataAgeSkew is the maximum tolerated difference between the client
// obfuscated_ticket_age (de-obfuscated with ticket_age_add) and the age the
// server computes itself. A larger skew rejects early data only; the session
// may still be resumed via a normal (1-RTT) PSK handshake.
const MaxEarlyDataAgeSkew = 10 * time.Second

// Decision is the admission outcome for a presented ticket.
type Decision int

const (
	// DecisionReject means the ticket must not be used for resumption; the
	// connection proceeds with a full handshake.
	DecisionReject Decision = iota

	// DecisionResume means the ticket is valid for PSK resumption, but any
	// early data offered alongside it must be rejected.
	DecisionResume

	// DecisionResumeEarlyData means the ticket is valid and the offered early
	// data passes the age-skew check and may be accepted.
	DecisionResumeEarlyData
)

// Reason identifies why an admission decision was taken. The zero value
// (ReasonOK) indicates an accepted ticket.
type Reason int

const (
	ReasonOK Reason = iota
	ReasonUnknownTicket
	ReasonTicketExpired
	ReasonSNIMismatch
	ReasonClientCertRequired
	ReasonClientCertNotExpected
	ReasonALPNMismatch
	ReasonCipherSuiteMismatch
	ReasonEarlyDataAgeSkew
)

// Result is the outcome of an admission evaluation.
type Result struct {
	Decision Decision
	Reason   Reason

	// Ticket is the authenticated ticket, populated when the ticket could be
	// opened even when policy later rejects resumption.
	Ticket *Ticket

	// Err carries the underlying open error (for example ErrUnknownTicket).
	Err error

	// Age is the server-computed ticket age, populated when the ticket was
	// opened.
	Age time.Duration

	// ClientAge is the age reconstructed from obfuscated_ticket_age and
	// ticket_age_add, populated for an early-data request.
	ClientAge time.Duration
}

// VhostPolicy describes the security posture of the target virtual host.
type VhostPolicy struct {
	// RequireClientCert is true when the virtual host demands a client
	// certificate (equivalent to crypto/tls RequireAnyClientCert /
	// RequireAndVerifyClientCert). A ticket without a certificate fingerprint
	// must then be refused.
	RequireClientCert bool

	// ExpectClientCert is true when the virtual host was configured not to
	// accept client certificates at all (equivalent to NoClientCert). A ticket
	// carrying a fingerprint must then be refused, matching crypto/tls.
	ExpectNoClientCert bool

	// AllowedCipherSuites optionally restricts the cipher suites the virtual
	// host accepts. When non-empty, the ticket's suite must be listed.
	AllowedCipherSuites []uint16
}

// AdmitRequest describes the resumption attempt being evaluated.
type AdmitRequest struct {
	// RawTicket is the ticket identity presented by the client.
	RawTicket []byte

	// SNI is the server name in the current ClientHello.
	SNI string

	// ALPN is the ALPN protocol selected for the current connection.
	ALPN string

	// Policy is the target virtual host's posture.
	Policy VhostPolicy

	// EarlyData is true when the client offered 0-RTT data.
	EarlyData bool

	// ObfuscatedTicketAge is the obfuscated_ticket_age from the
	// pre_shared_key extension (RFC 8446 section 4.2.11.2), only meaningful
	// when EarlyData is true.
	ObfuscatedTicketAge uint32

	// Now is the evaluation time. When zero, time.Now is used.
	Now time.Time
}

// Admitter evaluates tickets against a two-generation key ring.
type Admitter struct {
	ring *KeyRing

	// now may be overridden in tests.
	now func() time.Time
}

// NewAdmitter builds an Admitter on the given key ring.
func NewAdmitter(ring *KeyRing) (*Admitter, error) {
	if ring == nil || ring.current == nil {
		return nil, ErrInvalidKey
	}
	return &Admitter{ring: ring, now: time.Now}, nil
}

// Issue seals a fresh ticket binding the given claims to the current key
// generation.
func (a *Admitter) Issue(claims Claims) (*Ticket, error) {
	if claims.IssuedAt.IsZero() {
		claims.IssuedAt = a.now()
	}
	return a.ring.Seal(claims)
}

// Admit evaluates a resumption request. Tickets that cannot be opened by
// either key generation are reported with ReasonUnknownTicket and a reject
// decision rather than an error that could terminate the handshake.
func (a *Admitter) Admit(req AdmitRequest) Result {
	now := req.Now
	if now.IsZero() {
		now = a.now()
	}

	ticket, err := a.ring.Open(req.RawTicket)
	if err != nil {
		// Both ErrUnknownTicket and a malformed-but-decryptable ticket are
		// non-fatal: the caller ignores the session and does a full handshake.
		return Result{Decision: DecisionReject, Reason: ReasonUnknownTicket, Err: err}
	}

	age := now.Sub(ticket.Claims.IssuedAt)
	result := Result{Ticket: ticket, Age: age}

	// 1. Freshness: the ticket must be no older than the 7-day ceiling.
	if age > MaxTicketAge || age < -MaxTicketAge {
		result.Decision = DecisionReject
		result.Reason = ReasonTicketExpired
		return result
	}

	// 2. SNI binding: the ticket may only be resumed on the virtual host that
	// issued it. This is the core defense against ticket migration.
	if ticket.Claims.SNI != req.SNI {
		result.Decision = DecisionReject
		result.Reason = ReasonSNIMismatch
		return result
	}

	// 3. Client certificate posture.
	hasFingerprint := len(ticket.Claims.ClientCertFingerprint) != 0
	switch {
	case req.Policy.RequireClientCert && !hasFingerprint:
		// Ticket issued on a lenient host, replayed onto a host that demands
		// client authentication: refuse resumption and force a full handshake.
		result.Decision = DecisionReject
		result.Reason = ReasonClientCertRequired
		return result
	case req.Policy.ExpectNoClientCert && hasFingerprint:
		result.Decision = DecisionReject
		result.Reason = ReasonClientCertNotExpected
		return result
	}

	// 4. ALPN binding. An empty ticket ALPN only matches an empty negotiated
	// ALPN.
	if ticket.Claims.ALPN != req.ALPN {
		result.Decision = DecisionReject
		result.Reason = ReasonALPNMismatch
		return result
	}

	// 5. Cipher suite binding, when the virtual host restricts suites.
	if len(req.Policy.AllowedCipherSuites) != 0 {
		allowed := false
		for _, id := range req.Policy.AllowedCipherSuites {
			if id == ticket.Claims.CipherSuite {
				allowed = true
				break
			}
		}
		if !allowed {
			result.Decision = DecisionReject
			result.Reason = ReasonCipherSuiteMismatch
			return result
		}
	}

	// All binding checks passed: the session can at least be resumed.
	result.Decision = DecisionResume

	// 6. Early data is conditional on the age-skew check. Failure here must
	// only downgrade 0-RTT to 1-RTT resumption, not reject the ticket.
	if req.EarlyData {
		clientAge := DeobfuscateTicketAge(req.ObfuscatedTicketAge, ticket.AgeAdd)
		result.ClientAge = clientAge
		if ageSkew(age, clientAge) > MaxEarlyDataAgeSkew {
			result.Decision = DecisionResume
			result.Reason = ReasonEarlyDataAgeSkew
			return result
		}
		result.Decision = DecisionResumeEarlyData
	}

	return result
}

// DeobfuscateTicketAge reconstructs the client-observed ticket age from the
// obfuscated_ticket_age field and the ticket_age_add (RFC 8446 section
// 4.2.11.2):
//
//	obfuscated_age = (ticket_age + ticket_age_add) mod 2^32
func DeobfuscateTicketAge(obfuscated, ageAdd uint32) time.Duration {
	ageMillis := obfuscated - ageAdd
	return time.Duration(ageMillis) * time.Millisecond
}

// ageSkew returns the absolute difference between the server-computed and the
// client-reconstructed ticket ages.
func ageSkew(serverAge, clientAge time.Duration) time.Duration {
	d := serverAge - clientAge
	if d < 0 {
		d = -d
	}
	return d
}
