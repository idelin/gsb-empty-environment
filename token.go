package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// DefaultTokenRotation is used when no token rotation interval is
	// configured.
	DefaultTokenRotation = time.Minute

	// TokenNonceSize is the per-issuance nonce carried by a token.
	TokenNonceSize = 16
	// TokenMACSize is the HMAC-SHA256 tag size.
	TokenMACSize = sha256.Size
	// TokenSize is the complete wire size of an anti-clogging token.
	TokenSize = TokenNonceSize + TokenMACSize
)

var tokenKeyLabel = []byte("netsec/sae-anti-clogging-key/v1")

type tokenOptions struct {
	now     func() time.Time
	overlap time.Duration
	random  io.Reader
}

// TokenOption configures a TokenManager.
type TokenOption func(*tokenOptions) error

// WithClock installs a clock, primarily for deterministic rotation tests.
func WithClock(now func() time.Time) TokenOption {
	return func(o *tokenOptions) error {
		if now == nil {
			return fmt.Errorf("netsec: nil token clock")
		}
		o.now = now
		return nil
	}
}

// WithOverlap allows tokens from the preceding key epoch to remain valid for
// the given grace period after rotation. The default is zero.
func WithOverlap(overlap time.Duration) TokenOption {
	return func(o *tokenOptions) error {
		if overlap < 0 {
			return fmt.Errorf("netsec: negative token overlap")
		}
		o.overlap = overlap
		return nil
	}
}

// WithRand replaces the nonce source. It is intended for tests.
func WithRand(r io.Reader) TokenOption {
	return func(o *tokenOptions) error {
		if r == nil {
			return fmt.Errorf("netsec: nil token random source")
		}
		o.random = r
		return nil
	}
}

// TokenManager issues and verifies stateless anti-clogging tokens. The HMAC
// key changes every rotation interval. Each token carries a nonce that is
// covered by the HMAC, so a filter can also enforce single use.
type TokenManager struct {
	secret   []byte
	rotation time.Duration
	overlap  time.Duration
	now      func() time.Time
	random   io.Reader

	mu     sync.Mutex
	randMu sync.Mutex
	keys   map[uint64][]byte
}

// NewTokenManager creates a token manager. An empty secret is replaced with a
// random 32-byte secret. A zero rotation uses DefaultTokenRotation.
func NewTokenManager(secret []byte, rotation time.Duration, opts ...TokenOption) (*TokenManager, error) {
	if rotation < 0 {
		return nil, fmt.Errorf("netsec: negative token rotation")
	}
	if rotation == 0 {
		rotation = DefaultTokenRotation
	}

	opt := tokenOptions{
		now:    time.Now,
		random: rand.Reader,
	}
	for _, fn := range opts {
		if err := fn(&opt); err != nil {
			return nil, err
		}
	}

	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, secret); err != nil {
			return nil, fmt.Errorf("netsec: generate token secret: %w", err)
		}
	}
	secret = append([]byte(nil), secret...)

	return &TokenManager{
		secret:   secret,
		rotation: rotation,
		overlap:  opt.overlap,
		now:      opt.now,
		random:   opt.random,
		keys:     make(map[uint64][]byte),
	}, nil
}

// NewTokenManagerWithClock is a convenience constructor for callers that only
// need to override the clock.
func NewTokenManagerWithClock(secret []byte, rotation time.Duration, now func() time.Time) (*TokenManager, error) {
	return NewTokenManager(secret, rotation, WithClock(now))
}

// Rotation returns the configured key rotation interval.
func (m *TokenManager) Rotation() time.Duration { return m.rotation }

// Overlap returns the configured previous-epoch grace period.
func (m *TokenManager) Overlap() time.Duration { return m.overlap }

// Token returns a fresh anti-clogging token bound to mac and group. The token
// is nonce || HMAC-SHA256(epoch key, nonce || mac || group).
func (m *TokenManager) Token(mac net.HardwareAddr, group GroupID) []byte {
	if len(mac) != 6 {
		return nil
	}

	nonce := make([]byte, TokenNonceSize)
	m.randMu.Lock()
	if _, err := io.ReadFull(m.random, nonce); err != nil {
		m.randMu.Unlock()
		return nil
	}
	m.randMu.Unlock()

	now := m.now()
	epoch := m.epochAt(now)
	key := m.keyForEpoch(epoch)
	if key == nil {
		return nil
	}

	tag := tokenHMAC(key, mac, group, nonce)
	out := make([]byte, 0, TokenSize)
	out = append(out, nonce...)
	out = append(out, tag...)
	return out
}

// Issue is an alias for Token.
func (m *TokenManager) Issue(mac net.HardwareAddr, group GroupID) []byte {
	return m.Token(mac, group)
}

// Verify checks that token is well formed, uses a current key epoch, and is
// bound to mac and group.
func (m *TokenManager) Verify(mac net.HardwareAddr, group GroupID, token []byte) bool {
	if len(mac) != 6 || len(token) != TokenSize {
		return false
	}
	nonce := token[:TokenNonceSize]
	tag := token[TokenNonceSize:]

	now := m.now()
	epoch := m.epochAt(now)
	epochs := []uint64{epoch}
	if epoch > 0 && m.overlap > 0 && now.Before(m.epochStart(epoch).Add(m.overlap)) {
		epochs = append(epochs, epoch-1)
	}

	for _, e := range epochs {
		key := m.keyForEpoch(e)
		if key == nil {
			continue
		}
		expected := tokenHMAC(key, mac, group, nonce)
		if subtle.ConstantTimeCompare(expected, tag) == 1 {
			return true
		}
	}
	return false
}

// Check is an alias for Verify.
func (m *TokenManager) Check(mac net.HardwareAddr, group GroupID, token []byte) bool {
	return m.Verify(mac, group, token)
}

// CurrentEpoch returns the key epoch for the manager's clock.
func (m *TokenManager) CurrentEpoch() uint64 {
	return m.epochAt(m.now())
}

// Expiry returns an upper bound on the time a currently verified token remains
// valid. It is used to garbage-collect replay records.
func (m *TokenManager) Expiry(now time.Time) time.Time {
	epoch := m.epochAt(now)
	return m.epochStart(epoch).Add(m.rotation + m.overlap)
}

func (m *TokenManager) epochAt(now time.Time) uint64 {
	ns := now.UnixNano()
	if ns <= 0 {
		return 0
	}
	return uint64(ns / int64(m.rotation))
}

func (m *TokenManager) epochStart(epoch uint64) time.Time {
	ns := int64(epoch) * int64(m.rotation)
	return time.Unix(0, ns)
}

func (m *TokenManager) keyForEpoch(epoch uint64) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	if key, ok := m.keys[epoch]; ok {
		return key
	}

	h := hmac.New(sha256.New, m.secret)
	h.Write(tokenKeyLabel)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], epoch)
	h.Write(buf[:])
	key := h.Sum(nil)
	m.keys[epoch] = key

	if epoch > 4 {
		for e := range m.keys {
			if e < epoch-3 {
				delete(m.keys, e)
			}
		}
	}
	return key
}

func tokenHMAC(key []byte, mac net.HardwareAddr, group GroupID, nonce []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(nonce)
	h.Write(mac)
	g := groupBytes(group)
	h.Write(g[:])
	return h.Sum(nil)
}
