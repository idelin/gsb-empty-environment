package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// RotationWindow is the number of key generations retained for ticket
	// validation: the current generation and the one immediately preceding it.
	RotationWindow uint = 2

	// DefaultTicketTTL is used by Authority.Renew when Authority.TTL is not
	// positive.
	DefaultTicketTTL = 24 * time.Hour
)

var (
	// ErrExpired is returned when a ticket is presented at or after its
	// expiration time.
	ErrExpired = errors.New("netsec: ticket expired")

	// ErrKeyRetired is returned when a ticket's key is unknown, from the
	// future, or outside the retained rotation window.
	ErrKeyRetired = errors.New("netsec: ticket key is outside the rotation window")

	// ErrNotSubset is returned when a renewal would add rights or widen scope.
	ErrNotSubset = errors.New("netsec: renewal caps are not a subset of old caps")

	// ErrEscalation is an alias for ErrNotSubset.
	ErrEscalation = ErrNotSubset

	// ErrInvalidTicket is returned when a ticket contains malformed caps.
	ErrInvalidTicket = errors.New("netsec: invalid ticket")

	// ErrNoKeyRing is returned when ticket validation has no key ring to
	// consult.
	ErrNoKeyRing = errors.New("netsec: no key ring")
)

// KeyRing tracks signing-key generations. A key is active while its
// generation is no more than RotationWindow-1 behind the current generation.
type KeyRing struct {
	mu      sync.RWMutex
	current uint64
	window  uint
	keys    map[string]uint64
}

// NewKeyRing creates a key ring with the two-generation rotation window.
func NewKeyRing(currentGeneration uint64) *KeyRing {
	return NewKeyRingWithWindow(currentGeneration, RotationWindow)
}

// NewKeyRingWithWindow creates a key ring with an explicit retained-generation
// count. A zero window is replaced with RotationWindow.
func NewKeyRingWithWindow(currentGeneration uint64, window uint) *KeyRing {
	if window == 0 {
		window = RotationWindow
	}
	return &KeyRing{
		current: currentGeneration,
		window:  window,
		keys:    make(map[string]uint64),
	}
}

// AddKey associates a key id with its signing generation. Server-side
// configuration should use distinct ids for distinct generations. An empty id
// is stored but can never validate a ticket.
func (r *KeyRing) AddKey(id string, generation uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[string]uint64)
	}
	r.keys[id] = generation
}

// Rotate advances the current generation and records the new signing key. It
// returns the generation assigned to id.
func (r *KeyRing) Rotate(id string) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[string]uint64)
	}
	r.current++
	r.keys[id] = r.current
	return r.current
}

// SetCurrentGeneration advances or rewinds the current generation pointer.
func (r *KeyRing) SetCurrentGeneration(generation uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = generation
}

// CurrentGeneration returns the newest signing generation.
func (r *KeyRing) CurrentGeneration() uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Window returns the number of retained generations.
func (r *KeyRing) Window() uint {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.window
}

// KeyGeneration resolves a key id to its signing generation.
func (r *KeyRing) KeyGeneration(id string) (uint64, bool) {
	if r == nil || id == "" {
		return 0, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	generation, ok := r.keys[id]
	return generation, ok
}

// KeyActive reports whether the key is known and inside the retained
// two-generation window.
func (r *KeyRing) KeyActive(id string) bool {
	if r == nil || id == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	generation, ok := r.keys[id]
	if !ok || generation > r.current {
		return false
	}
	return r.current-generation < uint64(r.window)
}

// Ticket carries the granted capabilities, their expiration, and the id of
// the rotation key that signed them.
type Ticket struct {
	Caps      Caps
	ExpiresAt time.Time
	KeyID     string
}

// Expired reports whether the ticket is at or past its expiration time. A
// ticket with a zero expiration is treated as expired.
func (t Ticket) Expired(now time.Time) bool {
	return t.ExpiresAt.IsZero() || !now.Before(t.ExpiresAt)
}

// Validate checks expiration, the signing-key rotation window, and the ticket
// capability syntax.
func (t Ticket) Validate(now time.Time, keys *KeyRing) error {
	if t.Expired(now) {
		return ErrExpired
	}
	if keys == nil {
		return ErrNoKeyRing
	}
	if !keys.KeyActive(t.KeyID) {
		return fmt.Errorf("%w: key %q", ErrKeyRetired, t.KeyID)
	}
	if err := t.Caps.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTicket, err)
	}
	return nil
}

// Authority validates and issues renewed tickets.
type Authority struct {
	// Keys is required. It defines the retained signing-key generations.
	Keys *KeyRing

	// TTL is the lifetime of a renewed ticket measured from the renewal time.
	// When TTL is not positive, DefaultTicketTTL is used.
	TTL time.Duration

	// SigningKeyID, when set, is used for the returned ticket. When empty, the
	// old ticket's key id is retained.
	SigningKeyID string
}

// ValidateRenewal rejects an expired or retired-key ticket and any requested
// capabilities that are not covered by the old ticket.
func (a *Authority) ValidateRenewal(old Ticket, newCaps Caps, now time.Time) error {
	if a == nil || a.Keys == nil {
		return ErrNoKeyRing
	}
	if err := old.Validate(now, a.Keys); err != nil {
		return err
	}
	if err := newCaps.Validate(); err != nil {
		return err
	}
	if !newCaps.IsSubsetOf(old.Caps) {
		return ErrNotSubset
	}
	return nil
}

// Renew validates the renewal and returns a ticket with the requested caps and
// a new expiration derived from Authority.TTL. The returned ticket is not
// cryptographically signed by this package; SigningKeyID identifies the key
// the caller should use.
func (a *Authority) Renew(old Ticket, newCaps Caps, now time.Time) (Ticket, error) {
	if err := a.ValidateRenewal(old, newCaps, now); err != nil {
		return Ticket{}, err
	}

	keyID := a.SigningKeyID
	if keyID == "" {
		keyID = old.KeyID
	}
	if !a.Keys.KeyActive(keyID) {
		return Ticket{}, fmt.Errorf("%w: signing key %q", ErrKeyRetired, keyID)
	}

	ttl := a.TTL
	if ttl <= 0 {
		ttl = DefaultTicketTTL
	}

	renewed := old
	renewed.Caps = append(Caps(nil), newCaps...)
	renewed.ExpiresAt = now.Add(ttl)
	renewed.KeyID = keyID
	return renewed, nil
}

// RenewUntil is like Renew but uses an explicit expiration time. The
// expiration must be strictly after now.
func (a *Authority) RenewUntil(old Ticket, newCaps Caps, now, expiresAt time.Time) (Ticket, error) {
	if err := a.ValidateRenewal(old, newCaps, now); err != nil {
		return Ticket{}, err
	}
	if !expiresAt.After(now) {
		return Ticket{}, fmt.Errorf("%w: renewed expiration %s is not after %s", ErrExpired, expiresAt, now)
	}

	keyID := a.SigningKeyID
	if keyID == "" {
		keyID = old.KeyID
	}
	if !a.Keys.KeyActive(keyID) {
		return Ticket{}, fmt.Errorf("%w: signing key %q", ErrKeyRetired, keyID)
	}

	renewed := old
	renewed.Caps = append(Caps(nil), newCaps...)
	renewed.ExpiresAt = expiresAt
	renewed.KeyID = keyID
	return renewed, nil
}

// ValidateRenewal is the package-level form of Authority.ValidateRenewal.
func ValidateRenewal(old Ticket, newCaps Caps, now time.Time, keys *KeyRing) error {
	return (&Authority{Keys: keys}).ValidateRenewal(old, newCaps, now)
}
