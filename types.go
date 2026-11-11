package netsec

import (
	"context"
	"errors"
	"time"
)

// State is the lifecycle state of an immutable label.
type State int

const (
	// StateAbsent means no record exists for the label.
	StateAbsent State = iota
	// StateReserved means a request holds the label until it publishes,
	// releases the reservation, or the reservation expires.
	StateReserved
	// StatePublished means the label is immutably bound to one digest.
	StatePublished
	// StateRevoked means a reservation was released or expired and may be
	// re-acquired by a new request ID.
	StateRevoked
)

// String returns the stable wire name of the state.
func (s State) String() string {
	switch s {
	case StateReserved:
		return "reserved"
	case StatePublished:
		return "published"
	case StateRevoked:
		return "revoked"
	default:
		return "absent"
	}
}

var (
	// ErrConflict is returned when a request cannot own the label because a
	// different request ID or digest already bound it.
	ErrConflict = errors.New("netsec: label conflicts with an existing request or digest")
	// ErrReservationExpired is returned when a reservation outlived its TTL.
	ErrReservationExpired = errors.New("netsec: reservation expired")
	// ErrContentNotFound is returned when the uploaded content has not
	// arrived yet (the classic content-arrives-after-reservation case).
	ErrContentNotFound = errors.New("netsec: uploaded content not found")
	// ErrContentMismatch is returned when the stored upload digest differs
	// from the digest bound to the reservation.
	ErrContentMismatch = errors.New("netsec: uploaded content digest mismatch")
	// ErrIllegalTransition is returned by a Store when a state transition
	// violates the label state machine.
	ErrIllegalTransition = errors.New("netsec: illegal label state transition")
	// ErrInvalidArgument is returned for malformed requests.
	ErrInvalidArgument = errors.New("netsec: invalid argument")
)

// LabelRecord is the persisted state of one label.
type LabelRecord struct {
	// Label is the immutable tag name.
	Label string
	// State is the current lifecycle state.
	State State
	// Digest is the upload digest bound to the reservation/publish.
	Digest string
	// RequestID identifies the owning request; it drives idempotency.
	RequestID string
	// ExpiresAt is zero unless State is StateReserved.
	ExpiresAt time.Time
	// Version is incremented by the Store on every committed mutation.
	Version int64
}

// Store is a transactional label store.
//
// Update runs fn against the current record inside a single serializable
// transaction. A nil record means StateAbsent. Returning the record unchanged
// (same pointer and Version) is a read-only transaction. Any error returned by
// fn or the Store aborts the transaction: callers must observe no partial
// effect. A successful commit makes the returned record durable.
type Store interface {
	Update(ctx context.Context, label string, fn func(rec *LabelRecord) (*LabelRecord, error)) (*LabelRecord, error)
	// Label returns a snapshot of the record, or (nil, nil) when absent.
	Label(ctx context.Context, label string) (*LabelRecord, error)
}

// Verifier re-validates uploaded content before a label is published.
//
// Implementations return ErrContentNotFound when the upload is still in
// transit and ErrContentMismatch when the stored content does not hash to
// digest. Any other error aborts the publish without mutating the label.
type Verifier interface {
	Verify(ctx context.Context, label, digest string) error
}

// VerifierFunc adapts a function to the Verifier interface.
type VerifierFunc func(ctx context.Context, label, digest string) error

// Verify implements Verifier.
func (f VerifierFunc) Verify(ctx context.Context, label, digest string) error {
	return f(ctx, label, digest)
}

// Result is the outcome of a service call. Replayed is true when an
// idempotent retry replayed an already recorded outcome instead of creating a
// new one.
type Result struct {
	Record   *LabelRecord
	Replayed bool
}

// Clock abstracts time so expiry behavior is deterministically testable.
type Clock interface {
	Now() time.Time
}

func zeroTime() time.Time { return time.Time{} }

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the service clock.
func WithClock(c Clock) Option {
	return func(s *Service) { s.now = c.Now }
}

// WithTTL overrides the default reservation TTL.
func WithTTL(ttl time.Duration) Option {
	return func(s *Service) { s.ttl = ttl }
}

// Service is the host-facing publish transaction service.
type Service struct {
	store    Store
	verifier Verifier
	now      func() time.Time
	ttl      time.Duration
}

// NewService builds a Service with a 15 minute default reservation TTL.
func NewService(store Store, verifier Verifier, opts ...Option) *Service {
	s := &Service{
		store:    store,
		verifier: verifier,
		now:      time.Now,
		ttl:      15 * time.Minute,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
