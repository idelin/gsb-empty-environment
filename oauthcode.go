// Package oauthcode implements the authorization-code and PKCE component of
// an OAuth 2.0 authorization server.
//
// Security properties enforced here:
//   - An authorization code is single-use. Redemption is an atomic
//     check-and-consume, so concurrent redemptions cannot both succeed.
//   - A code is bound to the client_id, the exact redirect_uri string, and an
//     S256 PKCE challenge presented at issuance time.
//   - Expired codes and redirect_uri mismatches are always rejected.
//   - If a redeemed code is presented again (replay), every token issued for
//     that code is revoked immediately.
package oauthcode

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidGrant      = errors.New("oauthcode: invalid grant")
	ErrCodeReuse         = errors.New("oauthcode: authorization code already redeemed")
	ErrCodeExpired       = errors.New("oauthcode: authorization code expired")
	ErrClientMismatch    = errors.New("oauthcode: client_id does not match the code")
	ErrRedirectMismatch  = errors.New("oauthcode: redirect_uri does not match the code")
	ErrPKCEMismatch      = errors.New("oauthcode: PKCE code_verifier does not match")
	ErrUnsupportedMethod = errors.New("oauthcode: only S256 code_challenge_method is supported")
	ErrMissingChallenge  = errors.New("oauthcode: code_challenge is required")
	defaultCodeTTL       = 10 * time.Minute
	codeBytes            = 32
	tokenBytes           = 32
)

// Clock abstracts time so tests can control expiry.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManualClock is a Clock controlled by the caller, for tests.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type codeRecord struct {
	clientID    string
	redirectURI string
	challenge   string
	expiresAt   time.Time
	consumed    bool
	tokens      []string
}

type tokenRecord struct {
	code    string
	revoked bool
}

// Server issues and redeems authorization codes and tracks the tokens minted
// from them. It is safe for concurrent use.
type Server struct {
	mu     sync.Mutex
	codes  map[string]*codeRecord
	tokens map[string]*tokenRecord
	clock  Clock
	ttl    time.Duration

	// preRedeem, when set, runs just before the atomic redemption critical
	// section. Tests use it to orchestrate interleavings of concurrent
	// redemptions; it must be nil in production.
	preRedeem func()
}

// NewServer builds a Server. A nil clock uses wall time; a non-positive ttl
// uses a 10 minute default.
func NewServer(clock Clock, ttl time.Duration) *Server {
	if clock == nil {
		clock = realClock{}
	}
	if ttl <= 0 {
		ttl = defaultCodeTTL
	}
	return &Server{
		codes:  make(map[string]*codeRecord),
		tokens: make(map[string]*tokenRecord),
		clock:  clock,
		ttl:    ttl,
	}
}

// IssueCode creates an authorization code bound to clientID, the exact
// redirectURI string, and an S256 PKCE challenge. Any other challenge method
// is rejected.
func (s *Server) IssueCode(clientID, redirectURI, challenge, method string) (string, error) {
	if method != "S256" {
		return "", ErrUnsupportedMethod
	}
	if challenge == "" {
		return "", ErrMissingChallenge
	}
	code, err := randomString(codeBytes)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.codes[code] = &codeRecord{
		clientID:    clientID,
		redirectURI: redirectURI,
		challenge:   challenge,
		expiresAt:   s.clock.Now().Add(s.ttl),
	}
	s.mu.Unlock()
	return code, nil
}

// Redeem exchanges an authorization code for an access token. The check and
// the consume happen in a single critical section, so at most one caller can
// ever succeed for a given code. A replay of an already-consumed code revokes
// every token that code has issued.
func (s *Server) Redeem(code, clientID, redirectURI, verifier string) (string, error) {
	if s.preRedeem != nil {
		s.preRedeem()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.codes[code]
	if !ok {
		return "", ErrInvalidGrant
	}
	if rec.consumed {
		// Replay detected: the code may have been intercepted. Revoke all
		// tokens minted from it.
		for _, tok := range rec.tokens {
			if tr, ok := s.tokens[tok]; ok {
				tr.revoked = true
			}
		}
		return "", ErrCodeReuse
	}
	if !s.clock.Now().Before(rec.expiresAt) {
		return "", ErrCodeExpired
	}
	if subtle.ConstantTimeCompare([]byte(clientID), []byte(rec.clientID)) != 1 {
		return "", ErrClientMismatch
	}
	// redirect_uri must match the issuance-time value byte for byte.
	if subtle.ConstantTimeCompare([]byte(redirectURI), []byte(rec.redirectURI)) != 1 {
		return "", ErrRedirectMismatch
	}
	if !verifyS256(rec.challenge, verifier) {
		return "", ErrPKCEMismatch
	}

	rec.consumed = true
	tok, err := randomString(tokenBytes)
	if err != nil {
		return "", err
	}
	rec.tokens = append(rec.tokens, tok)
	s.tokens[tok] = &tokenRecord{code: code}
	return tok, nil
}

// IsTokenActive reports whether token was issued and has not been revoked.
func (s *Server) IsTokenActive(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.tokens[token]
	return ok && !rec.revoked
}

func verifyS256(challenge, verifier string) bool {
	if verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
