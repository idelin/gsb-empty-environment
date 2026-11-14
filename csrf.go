package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrTokenMalformed  = errors.New("csrf: token malformed")
	ErrTokenSignature  = errors.New("csrf: token signature invalid")
	ErrTokenExpired    = errors.New("csrf: token expired")
	ErrSessionMismatch = errors.New("csrf: token not bound to this session")
	ErrUserMismatch    = errors.New("csrf: token not bound to this user")
	ErrActionMismatch  = errors.New("csrf: token not bound to this action")
	ErrTokenConsumed   = errors.New("csrf: token already consumed or unknown form instance")
)

// TokenStore remembers issued form instances so each token can be
// consumed exactly once. Consume must be atomic.
type TokenStore interface {
	// Add records a form instance id with its expiry.
	Add(formID string, expiresAt time.Time)
	// Consume atomically removes formID and reports whether it was
	// present and unexpired. A false result means the token was
	// already used, never issued, or expired.
	Consume(formID string, now time.Time) bool
}

// MemoryTokenStore is a goroutine-safe in-memory TokenStore.
type MemoryTokenStore struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{entries: make(map[string]time.Time)}
}

func (s *MemoryTokenStore) Add(formID string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[formID] = expiresAt
}

func (s *MemoryTokenStore) Consume(formID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.entries[formID]
	if !ok {
		return false
	}
	delete(s.entries, formID)
	return now.Before(exp)
}

// Claims are the bindings every token is checked against, one by one,
// at submission time.
type Claims struct {
	SessionID string `json:"sid"`
	UserID    string `json:"uid"`
	Action    string `json:"act"`
	FormID    string `json:"fid"`
	ExpiresAt int64  `json:"exp"`
}

// TokenManager issues and validates HMAC-signed CSRF tokens bound to a
// session, user, action and single-use form instance with a short TTL.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
	store  TokenStore
	now    func() time.Time
}

func NewTokenManager(secret []byte, ttl time.Duration, store TokenStore) (*TokenManager, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("csrf: secret too short, need at least 16 bytes")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("csrf: ttl must be positive")
	}
	if store == nil {
		return nil, fmt.Errorf("csrf: store must not be nil")
	}
	return &TokenManager{
		secret: append([]byte(nil), secret...),
		ttl:    ttl,
		store:  store,
		now:    time.Now,
	}, nil
}

// Issue mints a token for one form instance of action in the given
// session. Every call produces a fresh, unique form id.
func (m *TokenManager) Issue(sessionID, userID, action string) (string, error) {
	if sessionID == "" || userID == "" || action == "" {
		return "", fmt.Errorf("csrf: session, user and action are required")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	expiresAt := m.now().Add(m.ttl)
	claims := Claims{
		SessionID: sessionID,
		UserID:    userID,
		Action:    action,
		FormID:    hex.EncodeToString(raw),
		ExpiresAt: expiresAt.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	m.store.Add(claims.FormID, expiresAt)
	return encode(payload, m.sign(payload)), nil
}

// Validate checks a submitted token against every binding in turn and
// consumes it exactly once. A token that passes validation can never
// be used again, and only works within the session it was issued for.
func (m *TokenManager) Validate(token, sessionID, userID, action string) error {
	payload, sig, err := decode(token)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(sig, m.sign(payload)) != 1 {
		return ErrTokenSignature
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ErrTokenMalformed
	}
	if !m.now().Before(time.Unix(claims.ExpiresAt, 0)) {
		return ErrTokenExpired
	}
	if subtle.ConstantTimeCompare([]byte(claims.SessionID), []byte(sessionID)) != 1 {
		return ErrSessionMismatch
	}
	if subtle.ConstantTimeCompare([]byte(claims.UserID), []byte(userID)) != 1 {
		return ErrUserMismatch
	}
	if subtle.ConstantTimeCompare([]byte(claims.Action), []byte(action)) != 1 {
		return ErrActionMismatch
	}
	if !m.store.Consume(claims.FormID, m.now()) {
		return ErrTokenConsumed
	}
	return nil
}

func (m *TokenManager) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

func encode(payload, sig []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

func decode(token string) (payload, sig []byte, err error) {
	dot := -1
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			if dot >= 0 {
				return nil, nil, ErrTokenMalformed
			}
			dot = i
		}
	}
	if dot <= 0 || dot == len(token)-1 {
		return nil, nil, ErrTokenMalformed
	}
	payload, err = base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return nil, nil, ErrTokenMalformed
	}
	sig, err = base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return nil, nil, ErrTokenMalformed
	}
	return payload, sig, nil
}
