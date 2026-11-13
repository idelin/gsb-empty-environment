package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrSecretDestroyed is returned when a destroyed secret handle is used.
	ErrSecretDestroyed = errors.New("netsec: secret handle destroyed")
)

// SecretHandle is the opaque handle to secret material returned by an STS.
//
// Clone must return an independent handle. The cache owns the original handle
// and destroys it on eviction or result rejection. Every lease receives a
// clone and destroys that clone when the lease is released.
type SecretHandle interface {
	Clone() (SecretHandle, error)
	Destroy() error
}

// SecretState is an observable snapshot intended for tests and audit logging.
type SecretState struct {
	ID        string
	ParentID  string
	Destroyed bool
	Zeroed    bool
	Length    int
}

var memorySecretCopySequence atomic.Uint64

// MemorySecret is an in-memory SecretHandle with observable zeroization.
// It is useful for tests and for hosts whose STS adapter materializes secrets
// in process memory.
type MemorySecret struct {
	mu        sync.Mutex
	id        string
	parentID  string
	data      []byte
	destroyed bool
}

// NewMemorySecret creates an in-memory secret handle. The input is copied.
func NewMemorySecret(id string, secret []byte) *MemorySecret {
	return &MemorySecret{
		id:   id,
		data: append([]byte(nil), secret...),
	}
}

// ID returns the handle identifier.
func (s *MemorySecret) ID() string {
	return s.id
}

// Clone returns an independent handle owned by one lease.
func (s *MemorySecret) Clone() (SecretHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return nil, ErrSecretDestroyed
	}
	seq := memorySecretCopySequence.Add(1)
	return &MemorySecret{
		id:       fmt.Sprintf("%s.lease.%d", s.id, seq),
		parentID: s.id,
		data:     append([]byte(nil), s.data...),
	}, nil
}

// Destroy zeroes the secret bytes and makes the handle unusable. It is safe to
// call more than once.
func (s *MemorySecret) Destroy() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return nil
	}
	for i := range s.data {
		s.data[i] = 0
	}
	s.destroyed = true
	return nil
}

// Reveal returns a copy of the secret material. It fails after Destroy.
func (s *MemorySecret) Reveal() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return nil, ErrSecretDestroyed
	}
	return append([]byte(nil), s.data...), nil
}

// State returns an observable snapshot.
func (s *MemorySecret) State() SecretState {
	s.mu.Lock()
	defer s.mu.Unlock()
	zeroed := s.destroyed
	for _, b := range s.data {
		if b != 0 {
			zeroed = false
			break
		}
	}
	return SecretState{
		ID:        s.id,
		ParentID:  s.parentID,
		Destroyed: s.destroyed,
		Zeroed:    zeroed,
		Length:    len(s.data),
	}
}

// Lease is an immutable, independently owned borrow of one cached STS result.
//
// A lease cannot be rebound to another identity. Release destroys the clone of
// the secret handle owned by this lease.
type Lease struct {
	identity     Identity
	accessKeyID  string
	sessionToken string
	issuedAt     time.Time
	expiresAt    time.Time
	secret       SecretHandle

	released    atomic.Bool
	releaseOnce sync.Once
	releaseErr  error
}

// Identity returns the identity bound to the lease.
func (l *Lease) Identity() Identity { return l.identity }

// AccessKeyID returns the access key ID bound to the lease.
func (l *Lease) AccessKeyID() string { return l.accessKeyID }

// SessionToken returns the session token bound to the lease.
func (l *Lease) SessionToken() string { return l.sessionToken }

// IssuedAt returns the STS issue time bound to the lease.
func (l *Lease) IssuedAt() time.Time { return l.issuedAt }

// ExpiresAt returns the STS expiry time bound to the lease.
func (l *Lease) ExpiresAt() time.Time { return l.expiresAt }

// Secret returns the lease-owned secret handle. After Release the handle is
// destroyed and further use fails.
func (l *Lease) Secret() SecretHandle { return l.secret }

// Released reports whether Release has been called.
func (l *Lease) Released() bool { return l.released.Load() }

// Release destroys the lease-owned secret clone. Repeated calls return the
// first result.
func (l *Lease) Release() error {
	l.releaseOnce.Do(func() {
		if l.secret != nil {
			l.releaseErr = l.secret.Destroy()
		}
		l.released.Store(true)
	})
	return l.releaseErr
}

// Close is an alias for Release.
func (l *Lease) Close() error { return l.Release() }

// Return is an alias for Release.
func (l *Lease) Return() error { return l.Release() }
