package netsec

import (
	"context"
	"sort"
	"sync"
)

// StoredRecord is the only record representation passed to storage callbacks.
// It intentionally has no plaintext field. The host can persist this record
// directly while retaining encrypted values elsewhere.
type StoredRecord struct {
	ID      string
	Scope   Scope
	Indexes []Index
}

// IndexFor returns the index for a generation.
func (r StoredRecord) IndexFor(generation Generation) (Index, bool) {
	for _, index := range r.Indexes {
		if index.Generation == generation {
			return index, true
		}
	}
	return Index{}, false
}

func (r StoredRecord) clone() StoredRecord {
	r.Indexes = append([]Index(nil), r.Indexes...)
	return r
}

func (r StoredRecord) validate() error {
	if r.ID == "" {
		return ErrInvalidRecord
	}
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if len(r.Indexes) == 0 {
		return ErrInvalidRecord
	}
	seen := make(map[Generation]struct{}, len(r.Indexes))
	for _, index := range r.Indexes {
		if index.Generation == 0 {
			return ErrInvalidRecord
		}
		if _, exists := seen[index.Generation]; exists {
			return ErrInvalidRecord
		}
		seen[index.Generation] = struct{}{}
	}
	return nil
}

// RecordStore persists sanitized records. Implementations never receive raw
// plaintext. The records returned by Load and List must be copies or otherwise
// safe for concurrent use.
type RecordStore interface {
	Load(ctx context.Context, id string) (StoredRecord, bool, error)
	Store(ctx context.Context, record StoredRecord) error
	List(ctx context.Context) ([]StoredRecord, error)
}

// MemoryStore is an in-memory RecordStore suitable for tests and small hosts.
// It is safe for concurrent use.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]StoredRecord
}

// NewMemoryStore creates an empty in-memory record store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]StoredRecord)}
}

// Load implements RecordStore.
func (m *MemoryStore) Load(ctx context.Context, id string) (StoredRecord, bool, error) {
	if err := contextError(ctx); err != nil {
		return StoredRecord{}, false, err
	}
	m.mu.RLock()
	record, ok := m.records[id]
	m.mu.RUnlock()
	if !ok {
		return StoredRecord{}, false, nil
	}
	return record.clone(), true, nil
}

// Store implements RecordStore.
func (m *MemoryStore) Store(ctx context.Context, record StoredRecord) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	record = record.clone()
	if err := record.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.records[record.ID] = record
	m.mu.Unlock()
	return nil
}

// List implements RecordStore. Results are sorted by record ID.
func (m *MemoryStore) List(ctx context.Context) ([]StoredRecord, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	m.mu.RLock()
	records := make([]StoredRecord, 0, len(m.records))
	for _, record := range m.records {
		records = append(records, record.clone())
	}
	m.mu.RUnlock()
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	return records, nil
}

// Len returns the number of records in the store.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.records)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
