// Package qrtoken provides a one-time, session-bound token component for
// QR-code login flows.
//
// A token is issued for a specific browser session and device, bound to a
// purpose, and valid only for a short time. The mobile app drives a two-step
// handshake (Scan then Confirm), and the browser finally redeems the token.
// Redemption succeeds only from the session and device the token was issued
// to, so a token intercepted from a URL is useless in another session.
// Every step is one-time: replayed scans, confirms, or redemptions are
// rejected, as are expired tokens.
package qrtoken

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Purpose identifies what a token may be used for. Redemption must present
// the same purpose the token was issued with.
type Purpose string

// PurposeLogin is the purpose for QR-code login tokens.
const PurposeLogin Purpose = "login"

// Sentinel errors returned by the Manager. Callers should match them with
// errors.Is.
var (
	ErrTokenNotFound    = errors.New("qrtoken: token not found")
	ErrTokenExpired     = errors.New("qrtoken: token expired")
	ErrSessionMismatch  = errors.New("qrtoken: redemption attempted from a different session")
	ErrDeviceMismatch   = errors.New("qrtoken: redemption attempted from a different device")
	ErrPurposeMismatch  = errors.New("qrtoken: redemption attempted for a different purpose")
	ErrAlreadyScanned   = errors.New("qrtoken: token already scanned")
	ErrAlreadyConfirmed = errors.New("qrtoken: token already confirmed")
	ErrAlreadyRedeemed  = errors.New("qrtoken: token already redeemed")
	ErrNotScanned       = errors.New("qrtoken: token not scanned yet")
	ErrNotConfirmed     = errors.New("qrtoken: token not confirmed yet")
)

// DefaultTTL is the default token lifetime. QR login tokens should be
// short-lived so an intercepted token expires quickly.
const DefaultTTL = 2 * time.Minute

// tokenBytes is the entropy of the opaque token: 256 bits from crypto/rand.
const tokenBytes = 32

type state int

const (
	statePending state = iota
	stateScanned
	stateConfirmed
	stateRedeemed
)

type token struct {
	sessionID string
	deviceID  string
	purpose   Purpose
	userID    string // identity confirmed by the mobile app
	state     state
	expiresAt time.Time
}

// Manager issues and verifies QR login tokens. It is safe for concurrent
// use. Construct one with NewManager.
type Manager struct {
	mu     sync.Mutex
	tokens map[string]*token
	ttl    time.Duration
	now    func() time.Time
}

// Option customizes a Manager.
type Option func(*Manager)

// WithTTL sets the token lifetime. Values <= 0 are ignored.
func WithTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.ttl = ttl
		}
	}
}

// WithClock overrides the time source, primarily for tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// NewManager returns a Manager with an in-memory token store.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		tokens: make(map[string]*token),
		ttl:    DefaultTTL,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Issue creates a token bound to the given browser session, device, and
// purpose. It returns the opaque token string (safe to embed in a QR URL)
// and its expiry time. sessionID, deviceID, and purpose must be non-empty.
func (m *Manager) Issue(sessionID, deviceID string, purpose Purpose) (string, time.Time, error) {
	if sessionID == "" {
		return "", time.Time{}, fmt.Errorf("qrtoken: session ID must not be empty")
	}
	if deviceID == "" {
		return "", time.Time{}, fmt.Errorf("qrtoken: device ID must not be empty")
	}
	if purpose == "" {
		return "", time.Time{}, fmt.Errorf("qrtoken: purpose must not be empty")
	}

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("qrtoken: generate token: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(raw)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked()
	expiresAt := m.now().Add(m.ttl)
	m.tokens[id] = &token{
		sessionID: sessionID,
		deviceID:  deviceID,
		purpose:   purpose,
		state:     statePending,
		expiresAt: expiresAt,
	}
	return id, expiresAt, nil
}

// Scan records that the mobile app has scanned the QR code. It is the first
// step of the mobile handshake and may happen exactly once per token.
func (m *Manager) Scan(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(id)
	if err != nil {
		return err
	}
	switch t.state {
	case statePending:
		t.state = stateScanned
		return nil
	case stateScanned:
		return ErrAlreadyScanned
	case stateConfirmed:
		return ErrAlreadyConfirmed
	default:
		return ErrAlreadyRedeemed
	}
}

// Confirm records that the mobile user approved the login, binding the
// token to userID. It requires a prior Scan and may happen exactly once.
func (m *Manager) Confirm(id, userID string) error {
	if userID == "" {
		return fmt.Errorf("qrtoken: user ID must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(id)
	if err != nil {
		return err
	}
	switch t.state {
	case statePending:
		return ErrNotScanned
	case stateScanned:
		t.state = stateConfirmed
		t.userID = userID
		return nil
	case stateConfirmed:
		return ErrAlreadyConfirmed
	default:
		return ErrAlreadyRedeemed
	}
}

// Redeem exchanges a confirmed token for the confirmed user identity. It
// succeeds only when called from the same session and device the token was
// issued to, with the same purpose, before expiry, and exactly once: a
// successful redemption consumes the token.
func (m *Manager) Redeem(id, sessionID, deviceID string, purpose Purpose) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(id)
	if err != nil {
		return "", err
	}
	if t.sessionID != sessionID {
		return "", ErrSessionMismatch
	}
	if t.deviceID != deviceID {
		return "", ErrDeviceMismatch
	}
	if t.purpose != purpose {
		return "", ErrPurposeMismatch
	}
	switch t.state {
	case stateRedeemed:
		return "", ErrAlreadyRedeemed
	case stateConfirmed:
		t.state = stateRedeemed
		return t.userID, nil
	default:
		return "", ErrNotConfirmed
	}
}

// lookupLocked returns the token, rejecting unknown and expired IDs.
// Expired tokens are removed so they can never be used again.
func (m *Manager) lookupLocked(id string) (*token, error) {
	t, ok := m.tokens[id]
	if !ok {
		return nil, ErrTokenNotFound
	}
	if !m.now().Before(t.expiresAt) {
		delete(m.tokens, id)
		return nil, ErrTokenExpired
	}
	return t, nil
}

// purgeExpiredLocked drops expired tokens to bound memory usage.
func (m *Manager) purgeExpiredLocked() {
	now := m.now()
	for id, t := range m.tokens {
		if !now.Before(t.expiresAt) {
			delete(m.tokens, id)
		}
	}
}
