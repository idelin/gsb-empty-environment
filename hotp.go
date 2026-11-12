package netsec

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

const (
	hotpDigits = 6
	hotpModulo = 1_000_000
)

// HOTP computes the RFC 4226 HOTP value (HMAC-SHA1, 6 digits) for the
// given secret and counter.
func HOTP(secret []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := uint32(sum[offset])&0x7f<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", hotpDigits, code%hotpModulo)
}

// CounterStore persists the HOTP counter so the authentication host can
// keep durable state across restarts.
type CounterStore interface {
	Load() (uint64, error)
	Store(counter uint64) error
}

// MemoryStore is a goroutine-safe in-memory CounterStore.
type MemoryStore struct {
	mu      sync.Mutex
	counter uint64
}

// NewMemoryStore returns a MemoryStore initialized to start.
func NewMemoryStore(start uint64) *MemoryStore {
	return &MemoryStore{counter: start}
}

// Load returns the stored counter.
func (m *MemoryStore) Load() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counter, nil
}

// Store persists counter.
func (m *MemoryStore) Store(counter uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter = counter
	return nil
}

// Options configures the Verifier windows. The normal look-ahead window
// and the resync window are disjoint: the resync window starts right
// after the look-ahead window ends.
type Options struct {
	// LookAhead is the number of counters checked by Verify, starting at
	// the current counter. Must be >= 1.
	LookAhead uint64
	// ResyncWindow is the number of candidate first-step counters checked
	// by Resync, starting at counter+LookAhead. Must be >= 1.
	ResyncWindow uint64
}

// Verifier is an HOTP state machine for an authentication host. It is
// safe for concurrent use; counter check-and-advance is atomic.
type Verifier struct {
	mu           sync.Mutex
	secret       []byte
	store        CounterStore
	counter      uint64
	lookAhead    uint64
	resyncWindow uint64
}

// NewVerifier builds a Verifier around a persistent CounterStore. The
// secret is copied.
func NewVerifier(secret []byte, store CounterStore, opts Options) (*Verifier, error) {
	if len(secret) == 0 {
		return nil, errors.New("netsec: empty HOTP secret")
	}
	if store == nil {
		return nil, errors.New("netsec: nil CounterStore")
	}
	if opts.LookAhead < 1 {
		return nil, errors.New("netsec: LookAhead must be >= 1")
	}
	if opts.ResyncWindow < 1 {
		return nil, errors.New("netsec: ResyncWindow must be >= 1")
	}
	counter, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("netsec: load counter: %w", err)
	}
	key := make([]byte, len(secret))
	copy(key, secret)
	return &Verifier{
		secret:       key,
		store:        store,
		counter:      counter,
		lookAhead:    opts.LookAhead,
		resyncWindow: opts.ResyncWindow,
	}, nil
}

// Counter returns the current counter (the next expected normal-window
// counter).
func (v *Verifier) Counter() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.counter
}

// Verify checks code against the normal window
// [counter, counter+LookAhead). On the first match it atomically advances
// the persistent counter to matched+1. A code outside the normal window
// (including any code in the resync window) is rejected and never moves
// the counter.
func (v *Verifier) Verify(code string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for c := v.counter; c < v.counter+v.lookAhead; c++ {
		if subtle.ConstantTimeCompare([]byte(HOTP(v.secret, c)), []byte(code)) == 1 {
			if err := v.advanceLocked(c + 1); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// Resync performs two-step resynchronization. Both codes must be
// presented in the same call, binding them to a single attempt: first
// must match HOTP(c) and second must match HOTP(c+1) for some c in the
// resync window [counter+LookAhead, counter+LookAhead+ResyncWindow).
//
// On success the persistent counter atomically advances to c+2 (the
// latter counter plus one). On failure nothing is stored and no
// intermediate state is kept, so a single guessed value can never push
// the counter forward.
func (v *Verifier) Resync(first, second string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	start := v.counter + v.lookAhead
	for c := start; c < start+v.resyncWindow; c++ {
		if subtle.ConstantTimeCompare([]byte(HOTP(v.secret, c)), []byte(first)) != 1 {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(HOTP(v.secret, c+1)), []byte(second)) != 1 {
			continue
		}
		if err := v.advanceLocked(c + 2); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// advanceLocked persists the new counter before updating memory, keeping
// the durable store and the in-memory state consistent. Callers must hold
// v.mu.
func (v *Verifier) advanceLocked(next uint64) error {
	if err := v.store.Store(next); err != nil {
		return fmt.Errorf("netsec: persist counter: %w", err)
	}
	v.counter = next
	return nil
}
