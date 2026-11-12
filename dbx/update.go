package dbx

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrBadSignature is returned when the update is not authenticated by
	// the dbx update key (KEK-holder signature).
	ErrBadSignature = errors.New("dbx: update signature verification failed")
	// ErrStaleTimestamp is returned when the update timestamp is not
	// strictly greater than the timestamp of the current dbx, which
	// blocks replay of older dbx versions.
	ErrStaleTimestamp = errors.New("dbx: update timestamp is not strictly newer")
)

// Store persists dbx as an authenticated variable. Replace must commit the
// timestamp and all entries as one atomic unit: if it returns an error, the
// previously stored dbx must remain fully intact.
type Store interface {
	// Load returns the currently committed timestamp and entries.
	Load() (timestamp uint64, entries []Entry, err error)
	// Replace atomically commits a new timestamp and entry set.
	Replace(timestamp uint64, entries []Entry) error
}

// Updater applies authenticated, timestamped dbx updates.
type Updater struct {
	store Store
	key   ed25519.PublicKey
}

// NewUpdater returns an Updater that authenticates updates with key and
// commits them through store.
func NewUpdater(store Store, key ed25519.PublicKey) *Updater {
	return &Updater{store: store, key: key}
}

// Payload is the canonical signed encoding of a dbx update: an 8-byte
// big-endian timestamp followed by each entry as kind byte || 32-byte hash.
func Payload(timestamp uint64, entries []Entry) []byte {
	buf := make([]byte, 8, 8+len(entries)*(1+sha256.Size))
	binary.BigEndian.PutUint64(buf, timestamp)
	for _, e := range entries {
		buf = append(buf, byte(e.Kind))
		buf = append(buf, e.Hash[:]...)
	}
	return buf
}

// Sign produces the update signature over Payload(timestamp, entries).
func Sign(key ed25519.PrivateKey, timestamp uint64, entries []Entry) []byte {
	return ed25519.Sign(key, Payload(timestamp, entries))
}

// Apply authenticates and commits a dbx update.
//
// The update is fully validated — signature, strictly increasing timestamp,
// and every entry — before the store is touched, and the store is committed
// with a single atomic Replace. If any step fails, the previously committed
// dbx is left untouched, so a failed write can never take partial effect.
func (u *Updater) Apply(timestamp uint64, entries []Entry, signature []byte) error {
	if !ed25519.Verify(u.key, Payload(timestamp, entries), signature) {
		return ErrBadSignature
	}
	current, _, err := u.store.Load()
	if err != nil {
		return fmt.Errorf("dbx: reading current variable: %w", err)
	}
	if timestamp <= current {
		return fmt.Errorf("%w: update %d, current %d", ErrStaleTimestamp, timestamp, current)
	}
	staged := make([]Entry, len(entries))
	for i, e := range entries {
		if err := e.validate(); err != nil {
			return fmt.Errorf("dbx: entry %d: %w", i, err)
		}
		staged[i] = e
	}
	if err := u.store.Replace(timestamp, staged); err != nil {
		return fmt.Errorf("dbx: commit failed, previous dbx retained: %w", err)
	}
	return nil
}

// MemStore is an in-memory Store. Replace stages the new variable into a
// private copy and only then swaps it in, so a failed commit never leaves a
// half-written dbx behind.
type MemStore struct {
	mu        sync.Mutex
	timestamp uint64
	entries   []Entry
}

// NewMemStore seeds a MemStore with an initial dbx revision.
func NewMemStore(timestamp uint64, entries []Entry) *MemStore {
	return &MemStore{timestamp: timestamp, entries: append([]Entry(nil), entries...)}
}

// Load implements Store.
func (m *MemStore) Load() (uint64, []Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.timestamp, append([]Entry(nil), m.entries...), nil
}

// Replace implements Store.
func (m *MemStore) Replace(timestamp uint64, entries []Entry) error {
	staged := append([]Entry(nil), entries...)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timestamp = timestamp
	m.entries = staged
	return nil
}
