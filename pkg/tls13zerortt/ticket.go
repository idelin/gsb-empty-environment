package tls13zerortt

import (
	"bytes"
	"time"
)

// mod232 computes x mod 2^32 for an unsigned 32-bit subtraction, matching the
// modular arithmetic mandated by RFC 8446 Section 4.2.11.1:
//
//	ticket_age = (obfuscated_ticket_age - ticket_age_add) mod 2^32
func mod232(a, b uint32) uint32 {
	return a - b
}

// RecoverTicketAge reverses the ticket_age_add obfuscation. All values are in
// milliseconds. The subtraction wraps modulo 2^32 automatically in uint32.
func RecoverTicketAge(obfuscatedTicketAge, ticketAgeAdd uint32) uint32 {
	return mod232(obfuscatedTicketAge, ticketAgeAdd)
}

// SessionTicket is the server-side record created when issuing a
// NewSessionTicket (RFC 8446 Section 4.6.1). It represents the negotiated
// parameters the resumption/0-RTT handshake must match exactly.
type SessionTicket struct {
	// PSK is the resumption_master_secret-derived resumption PSK.
	PSK []byte

	// Mode records whether this is a resumption or external PSK; it selects
	// the "res binder" / "ext binder" derivation label.
	Mode PSKMode

	// TicketAgeAdd is the per-ticket random obfuscation value.
	TicketAgeAdd uint32

	// Lifetime is the ticket_lifetime from NewSessionTicket.
	Lifetime time.Duration

	// IssuedAt is the server time at which the ticket was issued.
	IssuedAt time.Time

	// CipherSuite is the single cipher suite bound to this ticket.
	CipherSuite uint16

	// ALPN is the application-layer protocol negotiated with the ticket.
	// An empty slice means ALPN was not negotiated; an offered non-empty
	// ALPN list then fails the equality check.
	ALPN []byte

	// MaxEarlyDataSize is the max_early_data_size from the ticket's
	// early_data extension. Zero means the ticket does not permit 0-RTT.
	MaxEarlyDataSize uint32

	// EarlyDataUsed tracks cumulative accepted early-data bytes for this
	// ticket across the Validator's accounting (in addition to the single-use
	// replay filter). A single ticket identity is consumed by one winning
	// 0-RTT handshake at a time, but hosts sharing ticket records across
	// goroutines must still guard ChargeEarlyData with their own lock.
	EarlyDataUsed uint32
}

// AllowsEarlyData reports whether the ticket was issued with an early_data
// extension (non-zero max_early_data_size).
func (t *SessionTicket) AllowsEarlyData() bool {
	return t.MaxEarlyDataSize > 0
}

// TicketPolicy configures the strict acceptance window for resumption.
type TicketPolicy struct {
	// Skew is the maximum absolute difference between the client-recovered
	// ticket age and the server-measured age: both bounds [-Skew,+Skew] are
	// enforced.
	Skew time.Duration
}

// TicketObservation bundles the wire inputs needed to validate a ticket
// against its server-side record.
type TicketObservation struct {
	Ticket *SessionTicket

	// ObfuscatedTicketAge is taken from the selected PskIdentity.
	ObfuscatedTicketAge uint32

	// Now is the server receive time (injectable clock for testing).
	Now time.Time

	// OfferedCipherSuite is the single cipher suite the client selected for
	// the binder (the first entry of ClientHello.cipher_suites).
	OfferedCipherSuite uint16

	// OfferedALPN is the protocol offered in the "application_layer_protocol_
	// negotiation" extension, or nil when the extension is absent.
	OfferedALPN []byte

	// ClientSentEarlyData mirrors ParsedClientHello.HasEarlyData.
	ClientSentEarlyData bool
}

// ValidateTicket enforces, in order:
//
//  1. ticket not expired relative to ticket_lifetime;
//  2. recovered client age vs. server-measured age within [-Skew,+Skew];
//  3. cipher suite equality with the ticket;
//  4. ALPN equality with the ticket;
//  5. early_data extension presence and ticket early_data allowance;
//
// It does NOT perform replay registration or byte-budget accounting; see
// Validator for the full state machine. A nil error means the PSK is usable
// for (at least) 1-RTT resumption.
func ValidateTicket(obs TicketObservation, policy TicketPolicy) error {
	t := obs.Ticket
	if t == nil {
		return ErrMalformedClientHello
	}

	measuredAge := obs.Now.Sub(t.IssuedAt)
	if measuredAge < 0 {
		// Clock moved backwards relative to issuance; treat as skew failure
		// rather than accepting a negative age.
		return ErrTicketAgeSkew
	}
	if measuredAge > t.Lifetime {
		return ErrTicketExpired
	}

	recovered := time.Duration(RecoverTicketAge(obs.ObfuscatedTicketAge, t.TicketAgeAdd)) * time.Millisecond
	diff := recovered - measuredAge
	if diff < -policy.Skew || diff > policy.Skew {
		return ErrTicketAgeSkew
	}

	if obs.OfferedCipherSuite != t.CipherSuite {
		return ErrCipherSuiteMismatch
	}

	if !bytes.Equal(obs.OfferedALPN, t.ALPN) {
		return ErrALPNMismatch
	}

	if obs.ClientSentEarlyData {
		// A ticket without an early_data extension (zero
		// max_early_data_size) can never carry 0-RTT, regardless of the
		// strictness flag.
		if !t.AllowsEarlyData() {
			return ErrEarlyDataNotNegotiated
		}
	}

	return nil
}

// ChargeEarlyData accounts n accepted early-data bytes against the ticket's
// max_early_data_size. It returns ErrEarlyDataOverflow without mutating the
// counter when the cumulative total would exceed the budget; otherwise it
// updates EarlyDataUsed.
func (t *SessionTicket) ChargeEarlyData(n uint32) error {
	if n == 0 {
		return nil
	}
	if uint64(t.EarlyDataUsed)+uint64(n) > uint64(t.MaxEarlyDataSize) {
		return ErrEarlyDataOverflow
	}
	t.EarlyDataUsed += n
	return nil
}
