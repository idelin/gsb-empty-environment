package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrTicketMalformed  = errors.New("netsec: malformed ticket")
	ErrTicketSignature  = errors.New("netsec: ticket signature invalid")
	ErrTicketExpired    = errors.New("netsec: ticket expired")
	ErrTicketRevoked    = errors.New("netsec: ticket revoked")
	ErrTicketGeneration = errors.New("netsec: ticket from stale generation")
)

// Ticket binds an early-data resumption attempt to a specific session,
// client identity, SNI, ALPN and issuer generation. Tickets are sealed
// (HMAC-SHA256) by the Issuer and never trusted from the wire unverified.
type Ticket struct {
	SessionID     string    `json:"sid"`
	ClientID      string    `json:"cid"`
	SNI           string    `json:"sni"`
	ALPN          string    `json:"alpn"`
	Generation    uint64    `json:"gen"`
	PolicyVersion uint64    `json:"pv"`
	IssuedAt      time.Time `json:"iat"`
	ExpiresAt     time.Time `json:"exp"`
	Nonce         string    `json:"nonce"`
}

// Issuer seals and verifies tickets. It owns a per-generation HMAC
// keyring: rotating drops every older key, so tickets from previous
// generations immediately fail verification. Revocation is tracked by
// client and session identity.
type Issuer struct {
	mu              sync.Mutex
	now             func() time.Time
	ttl             time.Duration
	generation      uint64
	keys            map[uint64][]byte
	revokedClients  map[string]struct{}
	revokedSessions map[string]struct{}
}

// NewIssuer creates an Issuer at generation 1 with a fresh random key.
// ttl bounds how long any issued ticket stays valid.
func NewIssuer(ttl time.Duration) *Issuer {
	i := &Issuer{
		now:             time.Now,
		ttl:             ttl,
		keys:            make(map[uint64][]byte),
		revokedClients:  make(map[string]struct{}),
		revokedSessions: make(map[string]struct{}),
	}
	i.rotateLocked()
	return i
}

// Issue seals a new ticket for the given client identity, SNI and ALPN
// under the current generation and policy version.
func (i *Issuer) Issue(clientID, sni, alpn string, policyVersion uint64) (string, *Ticket, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, fmt.Errorf("netsec: nonce: %w", err)
	}
	sid := make([]byte, 16)
	if _, err := rand.Read(sid); err != nil {
		return "", nil, fmt.Errorf("netsec: session id: %w", err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	now := i.now()
	t := &Ticket{
		SessionID:     hex.EncodeToString(sid),
		ClientID:      clientID,
		SNI:           sni,
		ALPN:          alpn,
		Generation:    i.generation,
		PolicyVersion: policyVersion,
		IssuedAt:      now,
		ExpiresAt:     now.Add(i.ttl),
		Nonce:         hex.EncodeToString(nonce),
	}
	token, err := seal(t, i.keys[i.generation])
	if err != nil {
		return "", nil, err
	}
	return token, t, nil
}

// Rotate advances the generation and discards all older keys. Every
// ticket issued before the rotation becomes unverifiable.
func (i *Issuer) Rotate() uint64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.rotateLocked()
}

func (i *Issuer) rotateLocked() uint64 {
	i.generation++
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Errorf("netsec: key generation: %w", err))
	}
	i.keys = map[uint64][]byte{i.generation: key}
	return i.generation
}

// RevokeClient invalidates every outstanding and future-presented ticket
// belonging to the client identity.
func (i *Issuer) RevokeClient(clientID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.revokedClients[clientID] = struct{}{}
}

// RevokeSession invalidates a single session ticket.
func (i *Issuer) RevokeSession(sessionID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.revokedSessions[sessionID] = struct{}{}
}

// Generation reports the current ticket generation.
func (i *Issuer) Generation() uint64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.generation
}

// Unseal verifies the token's signature, generation, expiry and
// revocation status, returning the bound ticket.
func (i *Issuer) Unseal(token string) (*Ticket, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	t, err := unseal(token, i.keys)
	if err != nil {
		return nil, err
	}
	if t.Generation != i.generation {
		return nil, ErrTicketGeneration
	}
	if !i.now().Before(t.ExpiresAt) {
		return nil, ErrTicketExpired
	}
	if _, ok := i.revokedClients[t.ClientID]; ok {
		return nil, ErrTicketRevoked
	}
	if _, ok := i.revokedSessions[t.SessionID]; ok {
		return nil, ErrTicketRevoked
	}
	return t, nil
}

func seal(t *Ticket, key []byte) (string, error) {
	payload, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("netsec: seal: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func unseal(token string, keys map[uint64][]byte) (*Ticket, error) {
	var payloadPart, sigPart string
	for idx := 0; idx < len(token); idx++ {
		if token[idx] == '.' {
			payloadPart, sigPart = token[:idx], token[idx+1:]
			break
		}
	}
	if payloadPart == "" || sigPart == "" {
		return nil, ErrTicketMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, ErrTicketMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return nil, ErrTicketMalformed
	}
	var t Ticket
	if err := json.Unmarshal(payload, &t); err != nil {
		return nil, ErrTicketMalformed
	}
	key, ok := keys[t.Generation]
	if !ok {
		return nil, ErrTicketGeneration
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, ErrTicketSignature
	}
	return &t, nil
}
