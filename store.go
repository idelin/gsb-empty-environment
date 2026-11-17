package netsec

import (
	"errors"
	"sort"
	"sync"
)

// Store persists blind indexes. Its methods only accept record identifiers and
// Index values, so storage callbacks cannot observe plaintext.
type Store interface {
	Put(recordID string, index Index) error
	Get(recordID string, version Version) (Index, bool, error)
}

// VersionDeleter is implemented by stores that can physically purge a retired
// generation. The manager invokes it during retirement when available.
type VersionDeleter interface {
	DeleteVersion(version Version) error
}

// StoredIndex is one entry in a MemoryStore snapshot.
type StoredIndex struct {
	RecordID string
	Index    Index
}

// MemoryStore is a concurrency-safe in-memory Store implementation intended
// for tests, examples, and host integration doubles.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]map[Version]Index
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]map[Version]Index)}
}

// Put stores index for recordID. A record may have one index per generation.
func (s *MemoryStore) Put(recordID string, index Index) error {
	if recordID == "" {
		return ErrEmptyRecordID
	}
	if index.Version == "" {
		return ErrEmptyDomain
	}
	if s == nil {
		return ErrNilStore
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]map[Version]Index)
	}
	byVersion := s.entries[recordID]
	if byVersion == nil {
		byVersion = make(map[Version]Index)
		s.entries[recordID] = byVersion
	}
	byVersion[index.Version] = index
	return nil
}

// Get returns the index stored for recordID and version.
func (s *MemoryStore) Get(recordID string, version Version) (Index, bool, error) {
	if s == nil {
		return Index{}, false, ErrNilStore
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	byVersion := s.entries[recordID]
	index, ok := byVersion[version]
	return index, ok, nil
}

// Has reports whether an index exists for recordID and version.
func (s *MemoryStore) Has(recordID string, version Version) (bool, error) {
	_, ok, err := s.Get(recordID, version)
	return ok, err
}

// DeleteVersion removes every index from version. A stale query key cannot be
// matched against this store after deletion.
func (s *MemoryStore) DeleteVersion(version Version) error {
	if version == "" {
		return ErrEmptyDomain
	}
	if s == nil {
		return ErrNilStore
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for recordID, byVersion := range s.entries {
		delete(byVersion, version)
		if len(byVersion) == 0 {
			delete(s.entries, recordID)
		}
	}
	return nil
}

// Versions returns the sorted generations stored for recordID.
func (s *MemoryStore) Versions(recordID string) []Version {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	byVersion := s.entries[recordID]
	versions := make([]Version, 0, len(byVersion))
	for version := range byVersion {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

// List returns a deterministic snapshot ordered by record ID and generation.
func (s *MemoryStore) List() []StoredIndex {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StoredIndex, 0)
	for recordID, byVersion := range s.entries {
		for _, index := range byVersion {
			out = append(out, StoredIndex{RecordID: recordID, Index: index})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RecordID == out[j].RecordID {
			return out[i].Index.Version < out[j].Index.Version
		}
		return out[i].RecordID < out[j].RecordID
	})
	return out
}

// Len returns the number of stored record/generation entries.
func (s *MemoryStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, byVersion := range s.entries {
		n += len(byVersion)
	}
	return n
}

var (
	// ErrEmptyRecordID is returned when a record identifier is empty.
	ErrEmptyRecordID = errors.New("netsec: record ID must be non-empty")
	// ErrNilStore is returned when the manager has no backing store.
	ErrNilStore = errors.New("netsec: nil index store")
)
