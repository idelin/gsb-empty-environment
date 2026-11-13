package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrCookieInvalid is returned when a cookie fails validation
	// (wrong length, version, or authentication tag).
	ErrCookieInvalid = errors.New("netsec: cookie failed validation")
	// ErrCookieExpired is returned when a cookie is too old or
	// carries a timestamp from the future.
	ErrCookieExpired = errors.New("netsec: cookie expired")
)

const (
	cookieVersion = 1
	timestampLen  = 8
	tagLen        = sha256.Size
	// CookieLen is the exact length of cookies produced by Manager.Issue.
	CookieLen = 1 + timestampLen + tagLen
)

// Manager issues and validates stateless cookies that a TLS stack
// places into HelloRetryRequest and expects echoed back in the
// second ClientHello.
type Manager struct {
	secret   []byte
	lifetime time.Duration
	now      func() time.Time
}

// NewManager creates a Manager. secret must be at least 16 bytes and
// is copied. lifetime bounds how long an issued cookie stays valid;
// non-positive values default to 60 seconds.
func NewManager(secret []byte, lifetime time.Duration) (*Manager, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("netsec: secret too short (%d bytes), need at least 16", len(secret))
	}
	if lifetime <= 0 {
		lifetime = 60 * time.Second
	}
	m := &Manager{
		secret:   make([]byte, len(secret)),
		lifetime: lifetime,
		now:      time.Now,
	}
	copy(m.secret, secret)
	return m, nil
}

// Issue builds a stateless cookie bound to the ClientHello bytes and
// the client address. The TLS stack sends it in HelloRetryRequest.
func (m *Manager) Issue(clientHello []byte, clientAddr string) []byte {
	cookie := make([]byte, 0, CookieLen)
	cookie = append(cookie, cookieVersion)
	var ts [timestampLen]byte
	binary.BigEndian.PutUint64(ts[:], uint64(m.now().Unix()))
	cookie = append(cookie, ts[:]...)
	cookie = append(cookie, m.tag(ts[:], clientHello, clientAddr)...)
	return cookie
}

func (m *Manager) tag(ts, clientHello []byte, clientAddr string) []byte {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte{cookieVersion})
	mac.Write(ts)
	mac.Write([]byte(clientAddr))
	mac.Write([]byte{0})
	mac.Write(clientHello)
	return mac.Sum(nil)
}

// Validate checks that cookie was issued by this Manager for the
// given ClientHello and client address, and has not expired.
func (m *Manager) Validate(cookie, clientHello []byte, clientAddr string) error {
	if len(cookie) != CookieLen || cookie[0] != cookieVersion {
		return ErrCookieInvalid
	}
	ts := cookie[1 : 1+timestampLen]
	if subtle.ConstantTimeCompare(cookie[1+timestampLen:], m.tag(ts, clientHello, clientAddr)) != 1 {
		return ErrCookieInvalid
	}
	age := m.now().Sub(time.Unix(int64(binary.BigEndian.Uint64(ts)), 0))
	if age < -5*time.Second || age > m.lifetime {
		return ErrCookieExpired
	}
	return nil
}
