package par

import (
	"sync"
	"time"
)

// Entry is the pushed authorization request bound to a one-time
// request_uri. Only the server ever sees Params; clients hold the
// opaque reference.
type Entry struct {
	// ClientID is the authenticated client that performed the push.
	ClientID string
	// Params holds the pushed authorization request parameters, excluding
	// client authentication credentials. client_id is always bound to
	// ClientID.
	Params map[string]string
	// ExpiresAt is the point in time after which the entry must be rejected.
	ExpiresAt time.Time
}

// Store persists pushed authorization requests. Implementations must make
// Consume atomic so that a reference can never be consumed twice, even under
// concurrent requests.
type Store interface {
	// Put stores an entry under ref. It returns ErrConflict if ref is
	// already in use by a live entry.
	Put(ref string, entry Entry) error
	// Consume atomically looks up, validates and invalidates ref. It
	// returns ErrNotFound, ErrExpired or ErrUsed when the reference cannot
	// be consumed.
	Consume(ref string, now time.Time) (Entry, error)
}

type storedEntry struct {
	entry Entry
	used  bool
}

// MemoryStore is a concurrency-safe in-memory Store. It has no clock of its
// own: expiry is evaluated purely against the time passed to Put/Consume, so
// the manager's configured clock governs every lifetime decision. Expired
// entries are removed lazily whenever the store is touched. It is intended as
// a default implementation and for tests; production deployments can back
// Store with a shared cache or database.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]storedEntry
	now     func() time.Time
}

// NewMemoryStore creates an empty MemoryStore using the wall clock for lazy
// expiry cleanup during Put.
func NewMemoryStore() *MemoryStore {
	return newMemoryStore(time.Now)
}

func newMemoryStore(now func() time.Time) *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]storedEntry),
		now:     now,
	}
}

func (s *MemoryStore) purgeExpired(now time.Time) {
	for ref, stored := range s.entries {
		if !stored.entry.ExpiresAt.After(now) {
			delete(s.entries, ref)
		}
	}
}

// Put implements Store.
func (s *MemoryStore) Put(ref string, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.purgeExpired(s.now())
	if _, ok := s.entries[ref]; ok {
		return ErrConflict
	}
	s.entries[ref] = storedEntry{entry: entry}
	return nil
}

// Consume implements Store. The entry is invalidated before it is returned,
// so concurrent and repeated calls can never succeed twice.
func (s *MemoryStore) Consume(ref string, now time.Time) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.entries[ref]
	if !ok {
		return Entry{}, ErrNotFound
	}
	if !stored.entry.ExpiresAt.After(now) {
		delete(s.entries, ref)
		return Entry{}, ErrExpired
	}
	if stored.used {
		return Entry{}, ErrUsed
	}
	stored.used = true
	s.entries[ref] = stored
	return stored.entry, nil
}
