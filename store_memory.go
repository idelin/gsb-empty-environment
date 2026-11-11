package netsec

import (
	"sync"
)

// MemoryStore is an in-process reference Store used by storage hosts and
// tests. It keeps the legacy index (old digest -> candidates) and the new
// index (new digest -> identity), serializes each old key independently and
// preserves reference counts across migration.
type MemoryStore struct {
	mu sync.RWMutex

	oldIndex map[string][]*Blob
	newIndex map[string]*Blob

	keyLocksMu sync.Mutex
	keyLocks   map[string]*sync.Mutex
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		oldIndex: make(map[string][]*Blob),
		newIndex: make(map[string]*Blob),
		keyLocks: make(map[string]*sync.Mutex),
	}
}

func (s *MemoryStore) keyLock(oldDigest Digest) *sync.Mutex {
	key := string(oldDigest)
	s.keyLocksMu.Lock()
	lock, ok := s.keyLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.keyLocks[key] = lock
	}
	s.keyLocksMu.Unlock()
	return lock
}

// AddOld indexes a freshly ingested blob under its old digest. The blob is
// copied so later caller mutations cannot corrupt the store.
func (s *MemoryStore) AddOld(content []byte, oldH Hash, refs int) *Blob {
	blob := &Blob{
		Old:     append(Digest(nil), oldH(content)...),
		Content: append([]byte(nil), content...),
		Refs:    refs,
	}

	lock := s.keyLock(blob.Old)
	lock.Lock()
	defer lock.Unlock()

	s.mu.Lock()
	s.oldIndex[string(blob.Old)] = append(s.oldIndex[string(blob.Old)], blob)
	s.mu.Unlock()
	return blob
}

// Candidates implements Store. The migrator holds the per-old-digest gate
// while calling this method, so no additional key lock is taken here.
func (s *MemoryStore) Candidates(oldDigest Digest) ([]*Blob, error) {
	s.mu.RLock()
	stored := s.oldIndex[string(oldDigest)]
	out := make([]*Blob, len(stored))
	for i, blob := range stored {
		copy := *blob
		copy.Old = append(Digest(nil), blob.Old...)
		copy.NewDigest = append(Digest(nil), blob.NewDigest...)
		copy.Content = append([]byte(nil), blob.Content...)
		out[i] = &copy
	}
	s.mu.RUnlock()
	return out, nil
}

// LookupNew implements Store.
func (s *MemoryStore) LookupNew(newDigest Digest) (*Blob, error) {
	s.mu.RLock()
	blob, ok := s.newIndex[string(newDigest)]
	if !ok {
		s.mu.RUnlock()
		return nil, nil
	}
	copy := *blob
	copy.Old = append(Digest(nil), blob.Old...)
	copy.NewDigest = append(Digest(nil), blob.NewDigest...)
	copy.Content = append([]byte(nil), blob.Content...)
	s.mu.RUnlock()
	return &copy, nil
}

// PutNew implements Store. A new identity is installed exactly once; a
// repeated call for the same content and digest is a no-op. Distinct content
// claiming the same new digest is rejected instead of being merged. The
// matching legacy candidates are stamped with the new digest so subsequent
// reads observe the committed identity even before a full re-migration.
func (s *MemoryStore) PutNew(blob *Blob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := string(blob.NewDigest)
	if existing, ok := s.newIndex[key]; ok {
		if !equalBytes(existing.Content, blob.Content) {
			return &IntegrityError{OldDigest: append(Digest(nil), blob.Old...), Blob: blob, field: "new digest collision with distinct content"}
		}
		return nil
	}

	installed := *blob
	installed.Old = append(Digest(nil), blob.Old...)
	installed.NewDigest = append(Digest(nil), blob.NewDigest...)
	installed.Content = append([]byte(nil), blob.Content...)
	s.newIndex[key] = &installed

	for _, candidate := range s.oldIndex[string(blob.Old)] {
		if equalBytes(candidate.Content, blob.Content) {
			candidate.NewDigest = append(Digest(nil), blob.NewDigest...)
		}
	}
	return nil
}

// RangeOld implements Store. It snapshots the old keys first so concurrent
// migration cannot deadlock or starve the walk.
func (s *MemoryStore) RangeOld(fn func(oldDigest Digest) error) error {
	s.mu.RLock()
	keys := make([]string, 0, len(s.oldIndex))
	for key := range s.oldIndex {
		keys = append(keys, key)
	}
	s.mu.RUnlock()

	for _, key := range keys {
		if err := fn(Digest(key)); err != nil {
			return err
		}
	}
	return nil
}

// OldCount returns the number of distinct old index keys.
func (s *MemoryStore) OldCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.oldIndex)
}

// NewCount returns the number of distinct new identities installed.
func (s *MemoryStore) NewCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.newIndex)
}

// SnapshotRefs returns old-key -> total reference count and new-digest ->
// reference count, for conservation assertions in tests and host audits.
func (s *MemoryStore) SnapshotRefs() (oldRefs map[string]int, newRefs map[string]int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	oldRefs = make(map[string]int, len(s.oldIndex))
	for key, blobs := range s.oldIndex {
		total := 0
		for _, blob := range blobs {
			total += blob.Refs
		}
		oldRefs[key] = total
	}

	newRefs = make(map[string]int, len(s.newIndex))
	for key, blob := range s.newIndex {
		newRefs[key] = blob.Refs
	}
	return oldRefs, newRefs
}

// SnapshotNew returns a deep copy of every installed new identity.
func (s *MemoryStore) SnapshotNew() []*Blob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Blob, 0, len(s.newIndex))
	for _, blob := range s.newIndex {
		copy := *blob
		copy.Old = append(Digest(nil), blob.Old...)
		copy.NewDigest = append(Digest(nil), blob.NewDigest...)
		copy.Content = append([]byte(nil), blob.Content...)
		out = append(out, &copy)
	}
	return out
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
