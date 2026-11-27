package tls13zerortt

import (
	"errors"
	"time"
)

// Decision is the host-facing handshake verdict.
type Decision uint8

const (
	// DecisionFullHandshake rejects the PSK and requires a fresh TLS
	// handshake. Binder failures are fatal to the PSK and must never be
	// silently downgraded with early data.
	DecisionFullHandshake Decision = iota

	// DecisionResume1RTT accepts the PSK but refuses early_data: the
	// connection proceeds as an ordinary TLS 1.3 resumption (the server omits
	// early_data from EncryptedExtensions). Buffered 0-RTT records must be
	// discarded and the request retried after the handshake.
	DecisionResume1RTT

	// DecisionAcceptEarlyData admits the PSK and authorizes 0-RTT, subject to
	// the per-ticket byte budget charged separately via ChargeEarlyData.
	DecisionAcceptEarlyData
)

// String returns the stable lowercase wire/log name of a decision.
func (d Decision) String() string {
	switch d {
	case DecisionFullHandshake:
		return "full-handshake"
	case DecisionResume1RTT:
		return "resume-1-rtt"
	case DecisionAcceptEarlyData:
		return "accept-early-data"
	default:
		return "unknown"
	}
}

// IdempotencyPolicy lets a gateway forbid 0-RTT on non-idempotent request
// paths. Return true when the target (method + route) is safe to replay.
type IdempotencyPolicy func(routeKey string) bool

// Config configures the Validator.
type Config struct {
	// Policy is the ticket acceptance policy (skew window and early_data
	// extension strictness).
	Policy TicketPolicy

	// Replay is the single-use filter. Required; construct one with
	// NewExactReplayFilter or NewBoundedReplayFilter.
	Replay ReplayFilter

	// IsIdempotent optionally forces non-idempotent paths to 1-RTT. A nil
	// policy treats every route as idempotent (replay protection still
	// applies at the ticket layer).
	IsIdempotent IdempotencyPolicy
}

// Validator is the stateless policy engine; all mutable single-use state lives
// behind the ReplayFilter and the SessionTicket byte counters. It is safe for
// concurrent use.
type Validator struct {
	cfg Config
}

// NewValidator constructs a Validator, validating the configuration.
func NewValidator(cfg Config) (*Validator, error) {
	if cfg.Replay == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.Policy.Skew < 0 {
		return nil, ErrInvalidConfig
	}
	return &Validator{cfg: cfg}, nil
}

// AdmissionRequest carries everything needed for one ClientHello decision.
type AdmissionRequest struct {
	// Parsed is the parsed ClientHello containing the binder and early_data.
	Parsed *ParsedClientHello

	// IdentityIndex selects which offered PSK identity matched a server
	// ticket (usually 0).
	IdentityIndex int

	// Ticket is the server-side record referenced by the selected identity.
	Ticket *SessionTicket

	// Now is the injectable server receive time.
	Now time.Time

	// OfferedALPN is the ALPN value from the ClientHello, or nil if absent.
	OfferedALPN []byte

	// RouteKey identifies the request target for the idempotency policy.
	RouteKey string

	// EarlyDataBytes is the amount of 0-RTT data the client is expected to
	// send, used for an upfront budget check. Zero skips the upfront check;
	// the host still calls SessionTicket.ChargeEarlyData as data arrives.
	EarlyDataBytes uint32
}

// Result reports the verdict and the cause of any early_data refusal.
type Result struct {
	Decision Decision

	// EarlyData is true only when Decision == DecisionAcceptEarlyData.
	EarlyData bool

	// Reason is nil on full acceptance; otherwise it is one of the exported
	// sentinel errors and always safe to log (it carries no key material).
	Reason error

	// Replay is populated when the replay filter produced a verdict.
	Replay ReplayVerdict
}

// Admit runs the complete RFC 8446 0-RTT admission state machine.
//
// Order of checks (failures map to the documented Decision):
//
//  1. parse-derived invariants and PSK binder HMAC (constant time) over the
//     truncated ClientHello;
//  2. ticket lifetime and recovered-age skew window;
//  3. cipher suite / ALPN / early_data extension exact match;
//  4. per-ticket early-data byte budget;
//  5. idempotency policy;
//  6. single-use replay registration (fail closed on saturation).
func (v *Validator) Admit(req AdmissionRequest) Result {
	if req.Parsed == nil || req.Ticket == nil {
		return Result{Decision: DecisionFullHandshake, Reason: ErrMalformedClientHello}
	}

	// (1) Binder verification over truncated ClientHello bytes.
	ks, err := NewKeySchedule(req.Ticket.PSK, req.Ticket.Mode)
	if err != nil {
		return Result{Decision: DecisionFullHandshake, Reason: err}
	}
	if err := req.Parsed.VerifyBinder(ks, req.IdentityIndex); err != nil {
		return Result{Decision: DecisionFullHandshake, Reason: err}
	}

	age, ok := req.Parsed.ObfuscatedTicketAge(req.IdentityIndex)
	if !ok {
		return Result{Decision: DecisionFullHandshake, Reason: ErrBinderIndex}
	}

	// (2)+(3) Ticket age, lifetime and exact parameter matching.
	obs := TicketObservation{
		Ticket:              req.Ticket,
		ObfuscatedTicketAge: age,
		Now:                 req.Now,
		OfferedCipherSuite:  req.Parsed.CipherSuite,
		OfferedALPN:         req.OfferedALPN,
		ClientSentEarlyData: req.Parsed.HasEarlyData(),
	}
	if err := ValidateTicket(obs, v.cfg.Policy); err != nil {
		if errors.Is(err, ErrTicketExpired) {
			// An expired ticket cannot resume at all.
			return Result{Decision: DecisionFullHandshake, Reason: err}
		}
		// Skew / parameter mismatch invalidates 0-RTT but, per the task's
		// safe-fallback requirement, the verified PSK still resumes over
		// 1-RTT. Binder correctness was already established above.
		return downgrade(err)
	}

	// Without an early_data offer the handshake is simply a 1-RTT resume.
	if !req.Parsed.HasEarlyData() {
		return Result{Decision: DecisionResume1RTT, Reason: nil}
	}

	// (4) Upfront byte-budget check against max_early_data_size.
	if req.EarlyDataBytes > 0 {
		if uint64(req.Ticket.EarlyDataUsed)+uint64(req.EarlyDataBytes) >
			uint64(req.Ticket.MaxEarlyDataSize) {
			return downgrade(ErrEarlyDataOverflow)
		}
	}

	// (5) Non-idempotent paths are forced off 0-RTT.
	if v.cfg.IsIdempotent != nil && !v.cfg.IsIdempotent(req.RouteKey) {
		return downgrade(ErrNonIdempotent)
	}

	// (6) Single-use registration. The identity key is the ticket's opaque
	// identity bytes; the host may namespace them per node.
	identity, ok := req.Parsed.Identity(req.IdentityIndex)
	if !ok {
		return downgrade(ErrBinderIndex)
	}
	verdict := v.cfg.Replay.CheckAndRegister(string(identity), req.Now)
	switch verdict {
	case ReplayFirstUse:
		return Result{Decision: DecisionAcceptEarlyData, EarlyData: true, Replay: verdict}
	case ReplayDuplicate:
		return downgradeWithReplay(ErrReplayed, verdict)
	default:
		return downgradeWithReplay(ErrRegistryUnavailable, verdict)
	}
}

func downgrade(reason error) Result {
	return Result{Decision: DecisionResume1RTT, Reason: reason}
}

func downgradeWithReplay(reason error, verdict ReplayVerdict) Result {
	return Result{Decision: DecisionResume1RTT, Reason: reason, Replay: verdict}
}
