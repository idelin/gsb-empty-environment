package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// KeyID is the hex-encoded SHA-256 digest of an anchor's raw Ed25519
// public key.
type KeyID string

// Status is the lifecycle state of an anchor key as seen by the machine.
type Status string

const (
	StatusUnknown   Status = "unknown"
	StatusCandidate Status = "candidate"
	StatusTrusted   Status = "trusted"
	StatusRevoked   Status = "revoked"
)

var (
	// ErrClockRollback is returned when the trusted clock reports a time
	// earlier than the persisted watermark.
	ErrClockRollback = errors.New("netsec: trusted clock moved backwards")
	// ErrInvalidKey is returned for a public key that is not a raw
	// Ed25519 key of ed25519.PublicKeySize bytes.
	ErrInvalidKey = errors.New("netsec: invalid anchor public key")
	// ErrUnknownAnchor is returned when a referenced key is neither
	// trusted nor a candidate.
	ErrUnknownAnchor = errors.New("netsec: unknown anchor")
	// ErrRevoked is returned when an already revoked anchor is targeted
	// by another revocation.
	ErrRevoked = errors.New("netsec: anchor already revoked")
	// ErrInvalidNotice is returned when a revocation notice is malformed
	// or its signature does not verify.
	ErrInvalidNotice = errors.New("netsec: invalid revocation notice")
	// ErrUntrustedSigner is returned when the revocation signer is not
	// currently a trusted anchor.
	ErrUntrustedSigner = errors.New("netsec: revocation signer is not a trusted anchor")
	// ErrInvalidConfig is returned for an invalid machine configuration.
	ErrInvalidConfig = errors.New("netsec: invalid machine configuration")
)

// RevocationNotice carries a revocation flag. The signature must verify
// under an anchor that is trusted at the time the notice is processed.
type RevocationNotice struct {
	Target    KeyID
	Signer    KeyID
	Signature []byte
}

// RevocationMessage returns the canonical payload that a trusted anchor
// signs in order to revoke the anchor identified by target.
func RevocationMessage(target KeyID) []byte {
	return []byte("netsec-anchor-revoke:v1:" + string(target))
}

// KeyIDFromPublic derives the KeyID of a raw Ed25519 public key.
func KeyIDFromPublic(public []byte) KeyID {
	sum := sha256.Sum256(public)
	return KeyID(hex.EncodeToString(sum[:]))
}

// Clock supplies the trusted time base. Implementations must be monotonic:
// a later call must never return an earlier instant. The machine persists
// a high-water mark so that rollback across restarts is detected as well.
type Clock interface {
	Now() time.Time
}

// SystemClock is the default time source. The persisted high-water mark
// additionally guards against wall-clock rollback and restarts.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config configures a Machine opened with Open.
type Config struct {
	// Path is the file in which the anchor state is persisted.
	Path string
	// Clock is the trusted time source; defaults to SystemClock.
	Clock Clock
	// ObservationPeriod is the continuous observation time a new key must
	// accumulate before it is promoted to a trusted anchor.
	ObservationPeriod time.Duration
	// BootstrapKeys are the initially trusted anchors; used only when no
	// state file exists at Path.
	BootstrapKeys [][]byte
}

type anchor struct {
	ID            KeyID     `json:"id"`
	PublicKey     []byte    `json:"public_key"`
	ObservedSince time.Time `json:"observed_since"`
}

type state struct {
	Version    int               `json:"version"`
	LastTime   time.Time         `json:"last_time"`
	Trusted    map[KeyID][]byte  `json:"trusted"`
	Candidates map[KeyID]*anchor `json:"candidates"`
	Revoked    map[KeyID]bool    `json:"revoked"`
}

const currentVersion = 1

// Machine is the offline trust-anchor state machine. It is safe for
// concurrent use by the resolver host.
type Machine struct {
	mu                sync.Mutex
	path              string
	clock             Clock
	observationPeriod time.Duration
	st                *state
}

// Open loads the persisted state at cfg.Path, or bootstraps a new state
// file from cfg.BootstrapKeys when none exists yet.
func Open(cfg Config) (*Machine, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("%w: empty state path", ErrInvalidConfig)
	}
	if cfg.ObservationPeriod <= 0 {
		return nil, fmt.Errorf("%w: observation period must be positive", ErrInvalidConfig)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}

	st, err := loadState(cfg.Path)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = &state{
			Version:    currentVersion,
			Trusted:    map[KeyID][]byte{},
			Candidates: map[KeyID]*anchor{},
			Revoked:    map[KeyID]bool{},
		}
		for _, public := range cfg.BootstrapKeys {
			if len(public) != ed25519.PublicKeySize {
				return nil, ErrInvalidKey
			}
			id := KeyIDFromPublic(public)
			st.Trusted[id] = cloneBytes(public)
		}
		if err := saveState(cfg.Path, st); err != nil {
			return nil, err
		}
	}

	return &Machine{
		path:              cfg.Path,
		clock:             clock,
		observationPeriod: cfg.ObservationPeriod,
		st:                st,
	}, nil
}

// Observe processes one observation tick: present is the set of raw
// Ed25519 public keys visible to the resolver at this instant.
//
// Candidates present at this tick accumulate observation time and are
// promoted once they have been continuously present for the configured
// observation period. Candidates missing at this tick lose all
// accumulated observation time. Revoked keys are ignored.
func (m *Machine) Observe(present [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now, err := m.trustedNow()
	if err != nil {
		return err
	}

	seen := make(map[KeyID]bool, len(present))
	for _, public := range present {
		if len(public) != ed25519.PublicKeySize {
			return ErrInvalidKey
		}
		seen[KeyIDFromPublic(public)] = true
	}

	for id, candidate := range m.st.Candidates {
		if !seen[id] {
			// A missing tick resets the observation window.
			delete(m.st.Candidates, id)
			continue
		}
		if now.Sub(candidate.ObservedSince) >= m.observationPeriod {
			m.st.Trusted[id] = cloneBytes(candidate.PublicKey)
			delete(m.st.Candidates, id)
		}
	}

	for _, public := range present {
		id := KeyIDFromPublic(public)
		if m.st.Revoked[id] {
			continue
		}
		if _, ok := m.st.Trusted[id]; ok {
			continue
		}
		if _, ok := m.st.Candidates[id]; ok {
			continue
		}
		m.st.Candidates[id] = &anchor{
			ID:            id,
			PublicKey:     cloneBytes(public),
			ObservedSince: now,
		}
	}

	// The watermark advances on every successful tick, even when the
	// anchor sets are unchanged, so a later clock rollback is detectable.
	m.st.LastTime = now
	return m.persist()
}

// Revoke processes a revocation notice. The notice signature must verify
// under a currently trusted anchor (the original trusted key); candidate
// keys cannot authorize revocations. A revoked anchor is removed from the
// trusted set and tombstoned so that it can never be re-admitted.
func (m *Machine) Revoke(notice RevocationNotice) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now, err := m.trustedNow()
	if err != nil {
		return err
	}
	if notice.Target == "" || len(notice.Signature) == 0 {
		return ErrInvalidNotice
	}

	signerPublic, ok := m.st.Trusted[notice.Signer]
	if !ok {
		return ErrUntrustedSigner
	}
	if !ed25519.Verify(ed25519.PublicKey(signerPublic), RevocationMessage(notice.Target), notice.Signature) {
		return fmt.Errorf("%w: signature verification failed", ErrInvalidNotice)
	}
	if m.st.Revoked[notice.Target] {
		return ErrRevoked
	}

	if _, ok := m.st.Trusted[notice.Target]; ok {
		delete(m.st.Trusted, notice.Target)
	} else if _, ok := m.st.Candidates[notice.Target]; ok {
		delete(m.st.Candidates, notice.Target)
	} else {
		return ErrUnknownAnchor
	}
	m.st.Revoked[notice.Target] = true

	m.st.LastTime = now
	return m.persist()
}

// Status reports the current status of the anchor identified by id.
func (m *Machine) Status(id KeyID) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(id)
}

func (m *Machine) statusLocked(id KeyID) Status {
	if m.st.Revoked[id] {
		return StatusRevoked
	}
	if _, ok := m.st.Trusted[id]; ok {
		return StatusTrusted
	}
	if _, ok := m.st.Candidates[id]; ok {
		return StatusCandidate
	}
	return StatusUnknown
}

// Trusted returns the KeyIDs of all currently trusted anchors, sorted.
// Old and newly promoted anchors coexist until explicitly revoked.
func (m *Machine) Trusted() []KeyID {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]KeyID, 0, len(m.st.Trusted))
	for id := range m.st.Trusted {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// ObservedSince reports the start of the current continuous observation
// window for a candidate anchor.
func (m *Machine) ObservedSince(id KeyID) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate, ok := m.st.Candidates[id]
	if !ok {
		return time.Time{}, false
	}
	return candidate.ObservedSince, true
}

// trustedNow enforces the monotonic-time invariant against the persisted
// high-water mark. Any backwards jump fails closed.
func (m *Machine) trustedNow() (time.Time, error) {
	now := m.clock.Now()
	if now.Before(m.st.LastTime) {
		return time.Time{}, ErrClockRollback
	}
	return now, nil
}

func (m *Machine) persist() error {
	return saveState(m.path, m.st)
}

func cloneBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
