package dpopcheck

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// ecdsaCurve returns the P-256 curve used by ES256.
func ecdsaCurve() elliptic.Curve {
	return elliptic.P256()
}

// jtiStore remembers proof identifiers for one validity window so that a
// captured proof cannot be presented twice. Expired entries are removed
// lazily on every lookup, which keeps the map bounded without a janitor.
type jtiStore struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	window  time.Duration
	nowFunc func() time.Time
}

func newJTIStore(window time.Duration, nowFunc func() time.Time) *jtiStore {
	return &jtiStore{
		seen:    make(map[string]time.Time),
		window:  window,
		nowFunc: nowFunc,
	}
}

// useOnce records jti at the current time. It returns false when the same
// jti was already recorded within the validity window, i.e. a replay.
func (s *jtiStore) useOnce(jti string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFunc()
	for identifier, seenAt := range s.seen {
		if now.Sub(seenAt) > s.window {
			delete(s.seen, identifier)
		}
	}
	if _, exists := s.seen[jti]; exists {
		return false
	}
	s.seen[jti] = now
	return true
}

// NonceManager mints DPoP server nonces and tracks which ones are currently
// valid. Once any nonce has been issued, subsequent requests must present a
// still-valid nonce (RFC 9449 section 8).
type NonceManager struct {
	mu      sync.Mutex
	issued  map[string]time.Time
	ttl     time.Duration
	nowFunc func() time.Time
}

// NewNonceManager creates a manager whose nonces stay valid for ttl.
func NewNonceManager(ttl time.Duration) *NonceManager {
	return newNonceManager(ttl, time.Now)
}

func newNonceManager(ttl time.Duration, nowFunc func() time.Time) *NonceManager {
	return &NonceManager{
		issued:  make(map[string]time.Time),
		ttl:     ttl,
		nowFunc: nowFunc,
	}
}

// Issue returns a fresh random nonce and marks it as valid.
func (m *NonceManager) Issue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	m.issued[nonce] = m.nowFunc()
	return nonce, nil
}

// IsRequired reports whether the server has started using nonces.
func (m *NonceManager) IsRequired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	return len(m.issued) > 0
}

// Validate reports whether nonce is currently accepted.
func (m *NonceManager) Validate(nonce string) bool {
	if nonce == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	_, ok := m.issued[nonce]
	return ok
}

func (m *NonceManager) pruneLocked() {
	now := m.nowFunc()
	for nonce, issuedAt := range m.issued {
		if now.Sub(issuedAt) > m.ttl {
			delete(m.issued, nonce)
		}
	}
}
