// Package wrapstore stores secrets inside single-use wrapping tokens.
//
// A wrapping token lets a credential distribution service hand a secret to a
// recipient while keeping the delivery auditable. Every token can be unwrapped
// at most once: Unwrap atomically consumes the token, and concurrent unfold
// attempts leave exactly one winner.
//
// # Security model
//
// An attacker that unfolds a token before the legitimate recipient steals the
// secret. When the recipient later uses the same token it is not told "not
// found" (indistinguishable from a typo or a missing token) but receives
// ErrSuspectedInterception. The same error covers tokens that were revoked
// (for example by Rewrap) or that have reached their expiry second. Callers
// must distinguish ErrNotFound ("the store has never held this token") from
// ErrSuspectedInterception ("the token existed but can no longer be used, the
// delivery may have been intercepted").
//
// Recommended recipient protocol:
//
//  1. Call Lookup and compare the returned creation path with the path the
//     recipient expects. A mismatch means the token or its wrapping was
//     replaced; do not call Unwrap.
//  2. Call Unwrap. A nil error delivers the secret. An error satisfying
//     errors.Is(err, ErrSuspectedInterception) must raise an interception
//     alert instead of being reported as an ordinary missing token.
//
// Lookup never consumes a token and never extends its lifetime.
package wrapstore

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotFound is returned when the store has never held the supplied token.
var ErrNotFound = errors.New("wrapstore: token not found")

// ErrSuspectedInterception is returned when a token is (or was) known to the
// store but can no longer be unwrapped: it was already consumed, revoked by a
// Rewrap, or its expiry second has been reached. Recipients should treat this
// error as a possible interception of the credential delivery.
var ErrSuspectedInterception = errors.New("wrapstore: token unusable, suspected interception")

const (
	defaultTTL = 5 * time.Minute
	tokenBytes = 32
)

// Clock returns the current time. Injecting a Clock makes expiry decisions
// deterministic in tests. The default source is time.Now.
type Clock func() time.Time

// Option customizes a Store at construction.
type Option func(*Store)

// WithClock overrides the time source used for expiry decisions.
func WithClock(clock Clock) Option {
	return func(s *Store) {
		if clock != nil {
			s.now = clock
		}
	}
}

// WithTTL sets the lifetime granted to tokens created by Wrap and Rewrap.
// Values below one second are rounded up to one second; the TTL is applied at
// whole-second granularity.
func WithTTL(ttl time.Duration) Option {
	return func(s *Store) {
		if ttl < time.Second {
			ttl = time.Second
		}
		s.ttl = ttl.Truncate(time.Second)
	}
}

type status int

const (
	statusActive status = iota
	statusConsumed
	statusRevoked
	statusExpired
)

func (st status) reason() string {
	switch st {
	case statusConsumed:
		return "token was already consumed"
	case statusRevoked:
		return "token was revoked"
	case statusExpired:
		return "token has reached its expiry second"
	default:
		return "token is no longer active"
	}
}

type entry struct {
	secret       []byte
	creationPath string
	expiresAt    time.Time
	status       status
}

// Store is a concurrency-safe in-memory store of single-use wrapping tokens.
// The zero value is not usable; create one with New.
type Store struct {
	mu      sync.Mutex
	entries map[string]*entry
	now     Clock
	ttl     time.Duration
}

// New creates an empty Store.
func New(options ...Option) *Store {
	s := &Store{
		entries: make(map[string]*entry),
		now:     time.Now,
		ttl:     defaultTTL,
	}
	for _, option := range options {
		option(s)
	}
	return s
}

// currentSecond returns the clock time truncated to whole seconds, matching
// the "invalid from the expiry second on" semantics.
func (s *Store) currentSecond() time.Time {
	return s.now().UTC().Truncate(time.Second)
}

func newToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("wrapstore: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Wrap stores secret behind a freshly generated single-use token and returns
// the token together with creationPath, recorded verbatim as the token's
// creation path. The token becomes unusable starting at its expiry second
// (creation second plus the configured TTL).
func (s *Store) Wrap(secret []byte, creationPath string) (token string, creationPathOut string, err error) {
	stored := append([]byte(nil), secret...)

	s.mu.Lock()
	defer s.mu.Unlock()

	token, err = s.uniqueTokenLocked()
	if err != nil {
		return "", "", err
	}
	s.entries[token] = &entry{
		secret:       stored,
		creationPath: creationPath,
		expiresAt:    s.currentSecond().Add(s.ttl),
		status:       statusActive,
	}
	return token, creationPath, nil
}

func (s *Store) uniqueTokenLocked() (string, error) {
	for {
		token, err := newToken()
		if err != nil {
			return "", err
		}
		if _, exists := s.entries[token]; !exists {
			return token, nil
		}
	}
}

// Lookup returns the creation path recorded for an active token without
// consuming it: repeated Lookup calls leave a subsequent Unwrap available. A
// token that was consumed, revoked, or has reached its expiry second yields an
// error wrapping ErrSuspectedInterception; a token the store has never held
// yields ErrNotFound.
func (s *Store) Lookup(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[token]
	if !ok {
		return "", ErrNotFound
	}
	s.expireIfDueLocked(e)
	if e.status != statusActive {
		return "", interceptionError(e)
	}
	return e.creationPath, nil
}

// Unwrap atomically consumes an active token and returns its secret and
// creation path. Exactly one of any number of concurrent Unwrap calls for the
// same token succeeds; every other caller gets an error wrapping
// ErrSuspectedInterception, as do callers using a revoked or expired token.
// Unknown tokens return ErrNotFound. The returned slice is a copy owned by the
// caller.
func (s *Store) Unwrap(token string) (secret []byte, creationPath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[token]
	if !ok {
		return nil, "", ErrNotFound
	}
	s.expireIfDueLocked(e)
	if e.status != statusActive {
		return nil, "", interceptionError(e)
	}
	secret = append([]byte(nil), e.secret...)
	creationPath = e.creationPath
	e.status = statusConsumed
	e.secret = nil
	return secret, creationPath, nil
}

// Rewrap creates a new token wrapping the same secret and creation path as the
// supplied token and immediately revokes the old token: subsequent Lookup and
// Unwrap calls on it fail with ErrSuspectedInterception. The new token receives
// a full fresh TTL. An unknown token returns ErrNotFound; a token that is no
// longer active returns an error wrapping ErrSuspectedInterception.
func (s *Store) Rewrap(token string) (newToken string, creationPath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[token]
	if !ok {
		return "", "", ErrNotFound
	}
	s.expireIfDueLocked(e)
	if e.status != statusActive {
		return "", "", interceptionError(e)
	}

	newToken, err = s.uniqueTokenLocked()
	if err != nil {
		return "", "", err
	}
	s.entries[newToken] = &entry{
		secret:       append([]byte(nil), e.secret...),
		creationPath: e.creationPath,
		expiresAt:    s.currentSecond().Add(s.ttl),
		status:       statusActive,
	}
	e.status = statusRevoked
	e.secret = nil
	return newToken, e.creationPath, nil
}

// expireIfDueLocked retires an active entry once the current second reaches or
// passes its expiry second. Entries that were already consumed or revoked keep
// their original status.
func (s *Store) expireIfDueLocked(e *entry) {
	if e.status == statusActive && !s.currentSecond().Before(e.expiresAt) {
		e.status = statusExpired
		e.secret = nil
	}
}

func interceptionError(e *entry) error {
	return fmt.Errorf("%w: %s", ErrSuspectedInterception, e.status.reason())
}
