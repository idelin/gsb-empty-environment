// Package ticketkeys implements a TLS session ticket issuer that plugs into
// tls.Config.WrapSession and tls.Config.UnwrapSession.
//
// Tickets are encrypted with a rotatable AES-256-GCM key; decryption accepts
// the current and the previous key generation so tickets survive one
// rotation. Each ticket is bound to the SNI, the client certificate
// fingerprint and the policy generation at issuance time. Before a session
// is resumed the issuer re-checks revocation, expiry and policy generation,
// and falls back to a full handshake when any check fails.
package ticketkeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"sync"
	"time"
)

const (
	ticketVersion = 1
	keySize       = 32 // AES-256
	nonceSize     = 12 // AES-GCM standard nonce
	headerSize    = 1 + 4 + nonceSize

	defaultLifetime = 2 * time.Hour
)

// keySlot is one generation of ticket encryption key.
type keySlot struct {
	id    uint32
	key   [keySize]byte
	valid bool
}

// Issuer wraps and unwraps TLS session tickets. It is safe for concurrent
// use and can be shared by multiple tls.Config values.
type Issuer struct {
	mu       sync.RWMutex
	current  keySlot
	previous keySlot

	policy   *Policy
	lifetime time.Duration
	now      func() time.Time
}

// Option configures an Issuer.
type Option func(*Issuer)

// WithLifetime sets how long a ticket stays valid after issuance.
func WithLifetime(d time.Duration) Option {
	return func(i *Issuer) { i.lifetime = d }
}

// WithClock overrides the time source, mainly for testing.
func WithClock(now func() time.Time) Option {
	return func(i *Issuer) { i.now = now }
}

// NewIssuer returns an Issuer with a freshly generated key. If policy is
// nil a new empty Policy is used.
func NewIssuer(policy *Policy, opts ...Option) (*Issuer, error) {
	if policy == nil {
		policy = NewPolicy()
	}
	i := &Issuer{
		policy:   policy,
		lifetime: defaultLifetime,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(i)
	}
	if i.lifetime <= 0 {
		i.lifetime = defaultLifetime
	}
	if err := i.Rotate(); err != nil {
		return nil, err
	}
	// The first rotation only fills the current slot; drop the empty
	// previous slot so no invalid key is ever accepted.
	i.mu.Lock()
	i.previous = keySlot{}
	i.mu.Unlock()
	return i, nil
}

// Policy returns the revocation/generation policy the issuer consults.
func (i *Issuer) Policy() *Policy { return i.policy }

// Rotate generates a new encryption key. The old current key becomes the
// previous key and is still accepted for decryption; the key before that is
// forgotten and its tickets can no longer be resumed.
func (i *Issuer) Rotate() error {
	var key [keySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.previous = i.current
	i.current = keySlot{id: i.current.id + 1, key: key, valid: true}
	return nil
}

// Install hooks the issuer into cfg as WrapSession/UnwrapSession.
func (i *Issuer) Install(cfg *tls.Config) {
	cfg.WrapSession = i.WrapSession
	cfg.UnwrapSession = i.UnwrapSession
}

// WrapSession implements tls.Config.WrapSession.
func (i *Issuer) WrapSession(cs tls.ConnectionState, ss *tls.SessionState) ([]byte, error) {
	if ss == nil {
		return nil, nil
	}
	state, err := ss.Bytes()
	if err != nil {
		return nil, err
	}

	now := i.now()
	p := plaintext{
		issuedAt:   now,
		expiresAt:  now.Add(i.lifetime),
		generation: i.policy.Generation(),
		sni:        cs.ServerName,
	}
	if len(cs.PeerCertificates) > 0 {
		p.fingerprint = Fingerprint(cs.PeerCertificates[0])
		p.hasFingerprint = true
	}
	body := p.encode(state)

	i.mu.RLock()
	slot := i.current
	i.mu.RUnlock()

	aead, err := newAEAD(&slot.key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, headerSize+len(body)+aead.Overhead())
	out = append(out, ticketVersion)
	out = binary.BigEndian.AppendUint32(out, slot.id)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, body, nil)
	return out, nil
}

// UnwrapSession implements tls.Config.UnwrapSession. Any validation failure
// returns (nil, nil) so the handshake falls back to a full handshake.
func (i *Issuer) UnwrapSession(identity []byte, cs tls.ConnectionState) (*tls.SessionState, error) {
	p, state, ok := i.decrypt(identity)
	if !ok {
		return nil, nil
	}
	if !i.now().Before(p.expiresAt) {
		return nil, nil // expired
	}
	if p.sni != cs.ServerName {
		return nil, nil // ticket bound to a different SNI
	}
	if p.generation != i.policy.Generation() {
		return nil, nil // policy changed since issuance
	}
	if p.hasFingerprint && i.policy.IsRevoked(p.fingerprint) {
		return nil, nil // client certificate revoked since issuance
	}
	ss, err := tls.ParseSessionState(state)
	if err != nil {
		return nil, nil
	}
	return ss, nil
}

// decrypt authenticates and decodes a ticket with the current or previous
// key generation.
func (i *Issuer) decrypt(ticket []byte) (plaintext, []byte, bool) {
	var zero plaintext
	if len(ticket) < headerSize || ticket[0] != ticketVersion {
		return zero, nil, false
	}
	keyID := binary.BigEndian.Uint32(ticket[1:5])
	nonce := ticket[5:headerSize]
	ciphertext := ticket[headerSize:]

	i.mu.RLock()
	slots := [2]keySlot{i.current, i.previous}
	i.mu.RUnlock()

	for _, slot := range slots {
		if !slot.valid || slot.id != keyID {
			continue
		}
		aead, err := newAEAD(&slot.key)
		if err != nil {
			return zero, nil, false
		}
		body, err := aead.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return zero, nil, false
		}
		return decodePlaintext(body)
	}
	return zero, nil, false
}

func newAEAD(key *[keySize]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// plaintext is the decrypted ticket payload.
type plaintext struct {
	issuedAt       time.Time
	expiresAt      time.Time
	generation     uint64
	sni            string
	fingerprint    [32]byte
	hasFingerprint bool
}

func (p *plaintext) encode(state []byte) []byte {
	buf := make([]byte, 0, 8+8+8+2+len(p.sni)+1+len(p.fingerprint)+len(state))
	buf = binary.BigEndian.AppendUint64(buf, uint64(p.issuedAt.Unix()))
	buf = binary.BigEndian.AppendUint64(buf, uint64(p.expiresAt.Unix()))
	buf = binary.BigEndian.AppendUint64(buf, p.generation)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(p.sni)))
	buf = append(buf, p.sni...)
	if p.hasFingerprint {
		buf = append(buf, byte(len(p.fingerprint)))
		buf = append(buf, p.fingerprint[:]...)
	} else {
		buf = append(buf, 0)
	}
	return append(buf, state...)
}

func decodePlaintext(b []byte) (plaintext, []byte, bool) {
	var p plaintext
	if len(b) < 24 {
		return p, nil, false
	}
	p.issuedAt = time.Unix(int64(binary.BigEndian.Uint64(b[0:8])), 0)
	p.expiresAt = time.Unix(int64(binary.BigEndian.Uint64(b[8:16])), 0)
	p.generation = binary.BigEndian.Uint64(b[16:24])
	b = b[24:]
	if len(b) < 2 {
		return p, nil, false
	}
	sniLen := int(binary.BigEndian.Uint16(b[:2]))
	b = b[2:]
	if len(b) < sniLen {
		return p, nil, false
	}
	p.sni = string(b[:sniLen])
	b = b[sniLen:]
	if len(b) < 1 {
		return p, nil, false
	}
	fpLen := int(b[0])
	b = b[1:]
	if fpLen != 0 && fpLen != len(p.fingerprint) {
		return p, nil, false
	}
	if len(b) < fpLen {
		return p, nil, false
	}
	if fpLen == len(p.fingerprint) {
		copy(p.fingerprint[:], b[:fpLen])
		p.hasFingerprint = true
	}
	b = b[fpLen:]
	if len(b) == 0 {
		return p, nil, false
	}
	return p, b, true
}
