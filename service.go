package netsec

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is the lifecycle state of a one-time secret.
type State string

const (
	// StateAvailable: registered, never claimed.
	StateAvailable State = "available"
	// StateLeased: an unexpired, session-bound read lease exists.
	StateLeased State = "leased"
	// StateDelivered: terminal, client proof verified and payload destroyed.
	StateDelivered State = "delivered"
	// StateUnknown: terminal, outcome unknowable; payload must never be served.
	StateUnknown State = "unknown"
)

// Reason explains why a record entered the unknown terminal state.
type Reason string

const (
	ReasonLeaseExpired  Reason = "lease_expired"
	ReasonDestroyFailed Reason = "destroy_failed"
)

// EventType enumerates auditable protocol transitions.
type EventType string

const (
	EventClaimed       EventType = "claimed"
	EventRetransmitted EventType = "retransmitted"
	EventAckRejected   EventType = "ack_rejected"
	EventDelivered     EventType = "delivered"
	EventExpired       EventType = "lease_expired"
	EventDestroyFailed EventType = "destroy_failed"
)

// Event is one audit record. It deliberately carries no payload, nonce or key
// material, so the audit trail itself cannot leak secret content.
type Event struct {
	Time      time.Time
	Type      EventType
	SecretID  string
	SessionID string
	State     State
	Reason    Reason
	Detail    string
}

// Lease is the session-bound, short-lived read lease returned by Claim and
// Retransmit. All byte fields are independent copies owned by the caller.
type Lease struct {
	SecretID  string
	SessionID string
	Nonce     []byte
	Payload   []byte
	ProofKey  []byte
	ExpiresAt time.Time
	Attempt   int
}

// Outcome is the observable status of a secret record.
type Outcome struct {
	State     State
	Reason    Reason
	SessionID string
	Attempts  int
	UpdatedAt time.Time
}

// Clock abstracts time so tests can drive lease expiry deterministically.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Config configures a Service.
type Config struct {
	Store Store
	// TTL is the lifetime of every read lease, measured from Claim.
	TTL time.Duration
	// Clock defaults to wall-clock time when nil.
	Clock Clock
}

// Service implements the one-time-secret delivery protocol. It is safe for
// concurrent use.
type Service struct {
	store Store
	ttl   time.Duration
	clock Clock

	mu      sync.Mutex
	secrets map[string]*record
	audit   []Event
}

type record struct {
	id        string
	state     State
	reason    Reason
	detail    string
	sessionID string
	nonce     []byte
	proofKey  []byte
	expiresAt time.Time
	attempts  int
	updatedAt time.Time
}

// New constructs a Service.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("netsec: store is required")
	}
	if cfg.TTL <= 0 {
		return nil, errors.New("netsec: ttl must be positive")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{
		store:   cfg.Store,
		ttl:     cfg.TTL,
		clock:   clock,
		secrets: make(map[string]*record),
	}, nil
}

// Put registers a new secret in the available state. A registered id,
// including one in a terminal state, can never be reused.
func (s *Service) Put(ctx context.Context, id string, payload []byte) error {
	if id == "" {
		return errors.New("netsec: secret id is required")
	}
	if len(payload) == 0 {
		return errors.New("netsec: payload is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.secrets[id]; exists {
		return ErrExists
	}
	if err := s.store.Put(ctx, id, cloneBytes(payload)); err != nil {
		return err
	}
	s.secrets[id] = &record{id: id, state: StateAvailable, updatedAt: s.clock.Now()}
	return nil
}

// Claim issues the first, session-bound read lease for a secret. A secret that
// is already leased can only be accessed via Retransmit by the lease holder.
func (s *Service) Claim(ctx context.Context, id, sessionID string) (*Lease, error) {
	if err := validateLeaseRef(id, sessionID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.secrets[id]
	if !ok {
		return nil, ErrNotFound
	}
	s.expireLocked(ctx, rec)

	switch rec.state {
	case StateAvailable:
		payload, found, err := s.store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		nonce, err := randomBytes(32)
		if err != nil {
			return nil, err
		}
		proofKey, err := randomBytes(32)
		if err != nil {
			return nil, err
		}
		now := s.clock.Now()
		rec.state = StateLeased
		rec.sessionID = sessionID
		rec.nonce = nonce
		rec.proofKey = proofKey
		rec.expiresAt = now.Add(s.ttl)
		rec.attempts = 1
		rec.updatedAt = now
		s.appendEvent(Event{
			Time: now, Type: EventClaimed, SecretID: id,
			SessionID: sessionID, State: StateLeased,
		})
		return s.leaseDTO(rec, payload), nil
	case StateLeased:
		return nil, fmt.Errorf("%w: use Retransmit with the existing lease", ErrLeaseHeld)
	case StateDelivered:
		return nil, ErrAlreadyDelivered
	default:
		return nil, s.unknownError(rec)
	}
}

// Retransmit re-serves the payload under the same lease. It requires the exact
// session id and nonce bound at Claim time, never extends the lease deadline,
// and is the only operation allowed before Acknowledge.
func (s *Service) Retransmit(ctx context.Context, id, sessionID string, nonce []byte) (*Lease, error) {
	if err := validateLeaseRef(id, sessionID); err != nil {
		return nil, err
	}
	if len(nonce) == 0 {
		return nil, ErrLeaseMismatch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.secrets[id]
	if !ok {
		return nil, ErrNotFound
	}
	s.expireLocked(ctx, rec)

	switch rec.state {
	case StateAvailable:
		return nil, ErrNoLease
	case StateLeased:
		if !leaseMatches(rec, sessionID, nonce) {
			return nil, ErrLeaseMismatch
		}
		payload, found, err := s.store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		now := s.clock.Now()
		rec.attempts++
		rec.updatedAt = now
		s.appendEvent(Event{
			Time: now, Type: EventRetransmitted, SecretID: id,
			SessionID: sessionID, State: StateLeased,
		})
		return s.leaseDTO(rec, payload), nil
	case StateDelivered:
		return nil, ErrAlreadyDelivered
	default:
		return nil, s.unknownError(rec)
	}
}

// Acknowledge verifies the client proof delivered over ComputeProof. Only a
// valid proof transitions the record to delivered and destroys the payload;
// network loss of this call never produces a false delivery claim because the
// lease remains untouched on any rejection.
func (s *Service) Acknowledge(ctx context.Context, id, sessionID string, nonce, proof []byte) error {
	if err := validateLeaseRef(id, sessionID); err != nil {
		return err
	}
	if len(nonce) == 0 || len(proof) == 0 {
		return ErrLeaseMismatch
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.secrets[id]
	if !ok {
		return ErrNotFound
	}
	s.expireLocked(ctx, rec)

	switch rec.state {
	case StateAvailable:
		return ErrNoLease
	case StateLeased:
		// Fall through to verification below.
	case StateDelivered:
		return ErrAlreadyDelivered
	default:
		return s.unknownError(rec)
	}

	if !leaseMatches(rec, sessionID, nonce) {
		s.recordAckRejection(rec, "lease mismatch")
		return ErrLeaseMismatch
	}
	payload, found, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		// Backend lost the blob without a protocol transition: the delivery
		// outcome is unknowable, so go terminal instead of claiming success.
		return s.forceUnknownLocked(rec, ReasonDestroyFailed, "payload missing at confirmation")
	}
	expected := ComputeProof(rec.proofKey, rec.nonce, payload)
	if subtle.ConstantTimeCompare(expected, proof) != 1 {
		s.recordAckRejection(rec, "invalid proof")
		return ErrInvalidProof
	}

	if err := s.store.Delete(ctx, id); err != nil {
		// The proof was valid but destruction failed. The client did receive
		// the data, yet we cannot certify destruction: record unknown and stop.
		now := s.clock.Now()
		rec.state = StateUnknown
		rec.reason = ReasonDestroyFailed
		rec.detail = err.Error()
		rec.updatedAt = now
		rec.nonce = nil
		rec.proofKey = nil
		s.appendEvent(Event{
			Time: now, Type: EventDestroyFailed, SecretID: id,
			SessionID: sessionID, State: StateUnknown,
			Reason: ReasonDestroyFailed, Detail: err.Error(),
		})
		return fmt.Errorf("%w: %w", ErrDestroyFailed, err)
	}

	now := s.clock.Now()
	rec.state = StateDelivered
	rec.updatedAt = now
	rec.nonce = nil
	rec.proofKey = nil
	s.appendEvent(Event{
		Time: now, Type: EventDelivered, SecretID: id,
		SessionID: sessionID, State: StateDelivered,
	})
	return nil
}

// Sweep expires all leases whose deadline has passed. It returns the number of
// records newly moved to the unknown terminal state.
func (s *Service) Sweep(ctx context.Context) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, rec := range s.secrets {
		if rec.state == StateLeased && !rec.expiresAt.After(s.clock.Now()) {
			s.expireLocked(ctx, rec)
			count++
		}
	}
	return count
}

// expireLocked destroys an overdue lease's payload and moves the record to the
// unknown terminal state. A backend deletion failure keeps the same terminal
// state with reason still lease_expired; the detail records the failure.
func (s *Service) expireLocked(ctx context.Context, rec *record) {
	if rec.state != StateLeased || rec.expiresAt.After(s.clock.Now()) {
		return
	}
	now := s.clock.Now()
	err := s.store.Delete(ctx, rec.id)
	rec.state = StateUnknown
	rec.reason = ReasonLeaseExpired
	rec.updatedAt = now
	rec.nonce = nil
	rec.proofKey = nil
	if err != nil {
		rec.detail = err.Error()
	}
	s.appendEvent(Event{
		Time: now, Type: EventExpired, SecretID: rec.id,
		SessionID: rec.sessionID, State: StateUnknown,
		Reason: ReasonLeaseExpired, Detail: rec.detail,
	})
}

// forceUnknownLocked moves a record to the unknown terminal state without
// attempting a normal state transition.
func (s *Service) forceUnknownLocked(rec *record, reason Reason, detail string) error {
	now := s.clock.Now()
	rec.state = StateUnknown
	rec.reason = reason
	rec.detail = detail
	rec.updatedAt = now
	rec.nonce = nil
	rec.proofKey = nil
	s.appendEvent(Event{
		Time: now, Type: EventDestroyFailed, SecretID: rec.id,
		SessionID: rec.sessionID, State: StateUnknown,
		Reason: reason, Detail: detail,
	})
	return fmt.Errorf("%w: %s", ErrDestroyFailed, detail)
}

func (s *Service) recordAckRejection(rec *record, detail string) {
	s.appendEvent(Event{
		Time: s.clock.Now(), Type: EventAckRejected, SecretID: rec.id,
		SessionID: rec.sessionID, State: rec.state, Detail: detail,
	})
}

// Outcome reports the current state of a secret record.
func (s *Service) Outcome(id string) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.secrets[id]
	if !ok {
		return Outcome{}, ErrNotFound
	}
	return Outcome{
		State:     rec.state,
		Reason:    rec.reason,
		SessionID: rec.sessionID,
		Attempts:  rec.attempts,
		UpdatedAt: rec.updatedAt,
	}, nil
}

// Events returns a copy of the audit trail, oldest first.
func (s *Service) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.audit))
	copy(out, s.audit)
	return out
}
