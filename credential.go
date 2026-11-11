package netsec

import (
	"context"
	"errors"
	"time"
)

// RejectionReason explains why a candidate credential was not eligible.
type RejectionReason string

const (
	// RejectNotYetValid means NotBefore is in the future.
	RejectNotYetValid RejectionReason = "not_yet_valid"
	// RejectExpired means ExpiresAt is at or before the evaluation time.
	RejectExpired RejectionReason = "expired"
	// RejectTenantMismatch means the candidate binds a different tenant.
	RejectTenantMismatch RejectionReason = "tenant_mismatch"
	// RejectAudienceMismatch means the candidate does not bind the audience.
	RejectAudienceMismatch RejectionReason = "audience_mismatch"
	// RejectSourceError means the credential source could not be read.
	RejectSourceError RejectionReason = "source_error"
	// RejectRevoked means the source explicitly revoked its credential.
	RejectRevoked RejectionReason = "revoked"
)

// Credential is a candidate identity bound to a tenant and an audience.
// Zero NotBefore means immediately valid; zero ExpiresAt means it never
// expires.
type Credential struct {
	Source    SourceID
	Identity  string
	Tenant    string
	Audience  string
	NotBefore time.Time
	ExpiresAt time.Time
	Secret    []byte
}

// Clone returns a deep copy so callers cannot mutate bytes owned by a source.
func (c Credential) Clone() Credential {
	clone := c
	if c.Secret != nil {
		clone.Secret = append([]byte(nil), c.Secret...)
	}
	return clone
}

// Request describes the identity the credential host needs.
type Request struct {
	Tenant   string
	Audience string
	Now      time.Time
}

// Source supplies at most one credential per read. A nil credential with a
// nil error means the source currently has nothing to offer.
type Source interface {
	ID() SourceID
	Credential(ctx context.Context) (*Credential, error)
}

// MutableSource is a concurrency-safe Source whose credential can be
// replaced or revoked while a refresh is in flight.
type MutableSource struct {
	id SourceID

	mu    chan struct{}
	value atomicCredential
}

// NewMutableSource creates an empty source registered as id.
func NewMutableSource(id SourceID) *MutableSource {
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	return &MutableSource{id: id, mu: mu}
}

// ID implements Source.
func (s *MutableSource) ID() SourceID { return s.id }

// Set stores a defensive copy of credential. The caller keeps ownership of
// the original value, including its Secret bytes.
func (s *MutableSource) Set(credential Credential) {
	s.lock()
	defer s.unlock()
	credential.Source = s.id
	s.value.Store(&credential)
}

// Revoke removes any credential previously held by this source.
func (s *MutableSource) Revoke() {
	s.lock()
	defer s.unlock()
	s.value.Store(nil)
}

// Credential implements Source and returns a defensive copy.
func (s *MutableSource) Credential(_ context.Context) (*Credential, error) {
	s.lock()
	defer s.unlock()
	current := s.value.Load()
	if current == nil {
		return nil, nil
	}
	clone := current.Clone()
	return &clone, nil
}

func (s *MutableSource) lock()   { <-s.mu }
func (s *MutableSource) unlock() { s.mu <- struct{}{} }

// Rejection records why a source was excluded from a selection.
type Rejection struct {
	Source SourceID
	Reason RejectionReason
	Err    error
}

// ErrRevoked is returned by sources that explicitly revoke a credential.
var ErrRevoked = errors.New("netsec: credential revoked")

// RevokingSource is a concurrency-safe source that distinguishes an
// explicit revocation (Credential returns ErrRevoked) from merely having no
// credential yet. Revocation models a credential taken away while a job is
// running, rather than a source that has never issued one.
type RevokingSource struct {
	id SourceID

	mu      chan struct{}
	value   atomicCredential
	revoked bool
}

// NewRevokingSource creates an unrevoked, empty source registered as id.
func NewRevokingSource(id SourceID) *RevokingSource {
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	return &RevokingSource{id: id, mu: mu}
}

// ID implements Source.
func (s *RevokingSource) ID() SourceID { return s.id }

// Set stores a defensive copy and clears any prior revocation.
func (s *RevokingSource) Set(credential Credential) {
	s.lock()
	defer s.unlock()
	credential.Source = s.id
	s.value.Store(&credential)
	s.revoked = false
}

// Revoke marks the source as revoked and drops its credential.
func (s *RevokingSource) Revoke() {
	s.lock()
	defer s.unlock()
	s.value.Store(nil)
	s.revoked = true
}

// Credential implements Source.
func (s *RevokingSource) Credential(_ context.Context) (*Credential, error) {
	s.lock()
	defer s.unlock()
	if s.revoked {
		return nil, ErrRevoked
	}
	current := s.value.Load()
	if current == nil {
		return nil, nil
	}
	clone := current.Clone()
	return &clone, nil
}

func (s *RevokingSource) lock()   { <-s.mu }
func (s *RevokingSource) unlock() { s.mu <- struct{}{} }
