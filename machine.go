package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ReferenceStatus is the lifecycle state of a pushed request URI.
type ReferenceStatus string

const (
	// StatusActive means the reference has not expired or been used.
	StatusActive ReferenceStatus = "active"
	// StatusUsed means the reference has already been consumed once.
	StatusUsed ReferenceStatus = "used"
	// StatusExpired means the reference's validity period has elapsed.
	StatusExpired ReferenceStatus = "expired"
)

var (
	// ErrInvalidReference indicates that a request URI or client identifier is missing.
	ErrInvalidReference = errors.New("netsec: request_uri and client_id are required")
	// ErrReferenceExists indicates an attempt to issue an existing request URI again.
	ErrReferenceExists = errors.New("netsec: request_uri already exists")
	// ErrReferenceNotFound indicates that the authorization service has no such reference.
	ErrReferenceNotFound = errors.New("netsec: request_uri not found")
	// ErrReferenceExpired indicates that the reference is no longer within its validity period.
	ErrReferenceExpired = errors.New("netsec: request_uri expired")
	// ErrReferenceUsed indicates that the reference has already been consumed.
	ErrReferenceUsed = errors.New("netsec: request_uri already used")
	// ErrWrongClient indicates that the request URI belongs to a different client.
	ErrWrongClient = errors.New("netsec: request_uri belongs to a different client")

	// ErrAlreadyUsed is an alias for ErrReferenceUsed.
	ErrAlreadyUsed = ErrReferenceUsed
	// ErrClientMismatch is an alias for ErrWrongClient.
	ErrClientMismatch = ErrWrongClient
)

// ReferenceSnapshot is the externally visible state of a request URI.
type ReferenceSnapshot struct {
	RequestURI string          `json:"request_uri"`
	ClientID   string          `json:"client_id"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Status     ReferenceStatus `json:"status"`
}

type reference struct {
	clientID  string
	expiresAt time.Time
	status    ReferenceStatus
}

// Option configures a StateMachine.
type Option func(*StateMachine)

// WithClock replaces the machine's time source. It is useful for deterministic
// expiry tests and for services that use a shared clock.
func WithClock(now func() time.Time) Option {
	return func(m *StateMachine) {
		if now != nil {
			m.now = now
		}
	}
}

// StateMachine tracks pushed request URIs supplied by an authorization service.
//
// A reference is bound to the client that created it. The creator may consume
// it once before it expires. Consuming it again, or attempting to consume it
// as another client, is always rejected.
type StateMachine struct {
	mu         sync.Mutex
	now        func() time.Time
	references map[string]*reference
}

// NewStateMachine creates an empty, concurrency-safe state machine.
func NewStateMachine(options ...Option) *StateMachine {
	m := &StateMachine{
		now:        time.Now,
		references: make(map[string]*reference),
	}
	for _, option := range options {
		if option != nil {
			option(m)
		}
	}
	return m
}

// NewStateMachineWithClock creates an empty state machine using now as its clock.
func NewStateMachineWithClock(now func() time.Time) *StateMachine {
	return NewStateMachine(WithClock(now))
}

// Issue records a new request URI issued by the authorization service.
//
// The URI is bound to clientID and remains active until expiresAt. An already
// known request URI is never replaced, even when the previous entry has expired
// or was used.
func (m *StateMachine) Issue(requestURI, clientID string, expiresAt time.Time) error {
	if requestURI == "" || clientID == "" {
		return ErrInvalidReference
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.references[requestURI]; exists {
		return ErrReferenceExists
	}
	m.references[requestURI] = &reference{
		clientID:  clientID,
		expiresAt: expiresAt,
		status:    StatusActive,
	}
	return nil
}

// Use consumes a request URI for its creator.
//
// The first use by the creator within the validity period succeeds. A second
// use returns ErrReferenceUsed. Use by any other client returns ErrWrongClient
// without consuming or otherwise changing the reference. An expired reference
// returns ErrReferenceExpired.
func (m *StateMachine) Use(requestURI, clientID string) error {
	if requestURI == "" || clientID == "" {
		return ErrInvalidReference
	}

	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	ref, exists := m.references[requestURI]
	if !exists {
		return ErrReferenceNotFound
	}
	if ref.clientID != clientID {
		return ErrWrongClient
	}

	switch ref.status {
	case StatusUsed:
		return ErrReferenceUsed
	case StatusExpired:
		return ErrReferenceExpired
	case StatusActive:
		if !now.Before(ref.expiresAt) {
			ref.status = StatusExpired
			return ErrReferenceExpired
		}
		ref.status = StatusUsed
		return nil
	default:
		return fmt.Errorf("netsec: invalid reference state %q", ref.status)
	}
}

// Consume is an alias for Use.
func (m *StateMachine) Consume(requestURI, clientID string) error {
	return m.Use(requestURI, clientID)
}

// Redeem is an alias for Use.
func (m *StateMachine) Redeem(requestURI, clientID string) error {
	return m.Use(requestURI, clientID)
}

// Inspect returns a snapshot of a reference and whether it is known.
// Inspecting an active reference whose expiry has passed transitions it to
// StatusExpired.
func (m *StateMachine) Inspect(requestURI string) (ReferenceSnapshot, bool) {
	if requestURI == "" {
		return ReferenceSnapshot{}, false
	}

	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	ref, exists := m.references[requestURI]
	if !exists {
		return ReferenceSnapshot{}, false
	}
	if ref.status == StatusActive && !now.Before(ref.expiresAt) {
		ref.status = StatusExpired
	}
	return ReferenceSnapshot{
		RequestURI: requestURI,
		ClientID:   ref.clientID,
		ExpiresAt:  ref.expiresAt,
		Status:     ref.status,
	}, true
}

// Status returns a reference's current lifecycle state and whether it is known.
func (m *StateMachine) Status(requestURI string) (ReferenceStatus, bool) {
	snapshot, ok := m.Inspect(requestURI)
	return snapshot.Status, ok
}
