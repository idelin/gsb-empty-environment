package nestedjwt

import (
	"sync"
	"time"
)

type replayEntry struct {
	expiresAt time.Time
}

// MemoryReplayStore is the default ReplayStore: an in-memory, mutex-protected
// set of consumed (iss, jti) pairs with TTL expiry and a hard size cap.
// Multi-instance deployments should supply a distributed ReplayStore via
// WithReplayStore.
type MemoryReplayStore struct {
	mu      sync.Mutex
	entries map[string]replayEntry
	ttl     time.Duration
	maxSize int
	now     func() time.Time
}

// NewMemoryReplayStore builds a replay cache. A ttl of zero disables expiry;
// maxSize bounds memory usage and oldest entries are evicted first.
func NewMemoryReplayStore(ttl time.Duration, maxSize int) *MemoryReplayStore {
	if maxSize <= 0 {
		maxSize = 100_000
	}
	return &MemoryReplayStore{
		entries: make(map[string]replayEntry),
		ttl:     ttl,
		maxSize: maxSize,
		now:     time.Now,
	}
}

// Seen records key and reports whether it was already present and unexpired.
func (m *MemoryReplayStore) Seen(issuer, jti string) (bool, error) {
	key := replayKey(issuer, jti)
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.entries[key]; ok {
		if entry.expiresAt.IsZero() || entry.expiresAt.After(now) {
			return true, nil
		}
		delete(m.entries, key)
	}

	if len(m.entries) >= m.maxSize {
		m.evictExpired(now)
	}
	if len(m.entries) >= m.maxSize {
		m.evictOldest()
	}

	var expiresAt time.Time
	if m.ttl > 0 {
		expiresAt = now.Add(m.ttl)
	}
	m.entries[key] = replayEntry{expiresAt: expiresAt}
	return false, nil
}

func (m *MemoryReplayStore) evictExpired(now time.Time) {
	for key, entry := range m.entries {
		if !entry.expiresAt.IsZero() && !entry.expiresAt.After(now) {
			delete(m.entries, key)
		}
	}
}

// evictOldest drops the entry with the nearest expiry. Entries without expiry
// (zero time) are treated as the oldest for eviction purposes.
func (m *MemoryReplayStore) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for key, entry := range m.entries {
		if oldestKey == "" {
			oldestKey, oldestTime = key, entry.expiresAt
			continue
		}
		switch {
		case entry.expiresAt.IsZero():
			oldestKey, oldestTime = key, entry.expiresAt
		case !oldestTime.IsZero() && entry.expiresAt.Before(oldestTime):
			oldestKey, oldestTime = key, entry.expiresAt
		}
	}
	if oldestKey != "" {
		delete(m.entries, oldestKey)
	}
}

// Len returns the current number of retained replay entries.
func (m *MemoryReplayStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func replayKey(issuer, jti string) string {
	// NUL cannot appear in a normal JWT string value, so the pair cannot
	// collide across issuers/jtis of different lengths.
	return issuer + "\x00" + jti
}
