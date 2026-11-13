package netsec

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const tokenBytes = 32

type codeRecord struct {
	state      State
	tokens     map[Token]struct{}
	createdAt  time.Time
	changedAt  time.Time
	redeemedAt time.Time
}

type tokenRecord struct {
	code      string
	revoked   bool
	revokedAt time.Time
}

// Machine is a concurrency-safe authorization-code state machine.
//
// All transitions are serialized by one mutex. In particular, the check of
// whether a code is still redeemable, the issuance of its token, and the
// revocation of earlier tokens on replay happen atomically. This prevents two
// concurrent redemptions from both receiving valid tokens.
type Machine struct {
	mu     sync.Mutex
	codes  map[string]*codeRecord
	tokens map[Token]*tokenRecord
}

// NewMachine creates an empty state machine. Optional codes are issued
// immediately; invalid or duplicate codes are ignored. Use Issue when the
// caller needs to observe issuance errors.
func NewMachine(codes ...string) *Machine {
	m := &Machine{
		codes:  make(map[string]*codeRecord),
		tokens: make(map[Token]*tokenRecord),
	}
	for _, code := range codes {
		_ = m.Issue(code)
	}
	return m
}

// New is shorthand for NewMachine.
func New(codes ...string) *Machine {
	return NewMachine(codes...)
}

// Issue registers an authorization code as available for one redemption.
// Re-issuing an existing code is rejected and never resets its state.
func (m *Machine) Issue(code string) error {
	if code == "" {
		return ErrInvalidCode
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	if _, exists := m.codes[code]; exists {
		return ErrCodeAlreadyIssued
	}

	now := time.Now().UTC()
	m.codes[code] = &codeRecord{
		state:     StateIssued,
		tokens:    make(map[Token]struct{}),
		createdAt: now,
		changedAt: now,
	}
	return nil
}

// Redeem atomically exchanges a code for a token.
//
// The first call for an issued code succeeds and returns a valid token. A
// replay fails with ErrCodeAlreadyUsed, revokes every token previously issued
// from that code, and moves the code to StateRevoked. Subsequent calls fail
// with ErrCodeRevoked. The failed calls never receive a token.
func (m *Machine) Redeem(code string) (Token, error) {
	if code == "" {
		return "", ErrInvalidCode
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	rec, exists := m.codes[code]
	if !exists {
		return "", ErrCodeNotFound
	}

	switch rec.state {
	case StateIssued:
		token, err := newToken()
		if err != nil {
			return "", fmt.Errorf("netsec: generate token: %w", err)
		}
		now := time.Now().UTC()
		rec.state = StateRedeemed
		rec.changedAt = now
		rec.redeemedAt = now
		rec.tokens[token] = struct{}{}
		m.tokens[token] = &tokenRecord{code: code}
		return token, nil
	case StateRedeemed:
		m.revokeCodeTokensLocked(rec)
		rec.state = StateRevoked
		rec.changedAt = time.Now().UTC()
		return "", ErrCodeAlreadyUsed
	case StateRevoked:
		return "", ErrCodeRevoked
	default:
		return "", fmt.Errorf("netsec: invalid code state %d", rec.state)
	}
}

// Validate reports whether a token is currently valid. Revoked, unknown, and
// empty tokens are invalid.
func (m *Machine) Validate(token Token) bool {
	if token == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.tokens[token]
	return exists && !rec.revoked
}

// Revoke invalidates one token. Revoking an already revoked token is
// idempotent.
func (m *Machine) Revoke(token Token) error {
	if token == "" {
		return ErrInvalidToken
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.tokens[token]
	if !exists {
		return ErrTokenNotFound
	}
	if !rec.revoked {
		rec.revoked = true
		rec.revokedAt = time.Now().UTC()
	}
	return nil
}

// RevokeCode revokes a code and every token issued from it. It is useful when
// the authorization service withdraws a code out of band.
func (m *Machine) RevokeCode(code string) error {
	if code == "" {
		return ErrInvalidCode
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	rec, exists := m.codes[code]
	if !exists {
		return ErrCodeNotFound
	}
	m.revokeCodeTokensLocked(rec)
	rec.state = StateRevoked
	rec.changedAt = time.Now().UTC()
	return nil
}

// StateOf returns the current lifecycle state of a code.
func (m *Machine) StateOf(code string) (State, bool) {
	if code == "" {
		return StateUnknown, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.codes[code]
	if !exists {
		return StateUnknown, false
	}
	return rec.state, true
}

// Apply consumes an event sent by the authorization service. It is a
// convenience wrapper around the typed methods and is safe to call
// concurrently with them.
func (m *Machine) Apply(event Event) Result {
	result := Result{}

	switch event.Action {
	case ActionIssue:
		result.Err = m.Issue(event.Code)
	case ActionRedeem:
		result.Token, result.Err = m.Redeem(event.Code)
	case ActionRevokeToken:
		result.Err = m.Revoke(event.Token)
	case ActionRevokeCode:
		result.Err = m.RevokeCode(event.Code)
	default:
		result.Err = fmt.Errorf("%w: %q", ErrUnknownAction, event.Action)
	}

	result.OK = result.Err == nil
	if state, exists := m.StateOf(event.Code); exists {
		result.State = state
	}
	return result
}

func (m *Machine) revokeCodeTokensLocked(rec *codeRecord) {
	now := time.Now().UTC()
	for token := range rec.tokens {
		tokenRec, exists := m.tokens[token]
		if !exists || tokenRec.revoked {
			continue
		}
		tokenRec.revoked = true
		tokenRec.revokedAt = now
	}
}

func (m *Machine) initLocked() {
	if m.codes == nil {
		m.codes = make(map[string]*codeRecord)
	}
	if m.tokens == nil {
		m.tokens = make(map[Token]*tokenRecord)
	}
}

func newToken() (Token, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
