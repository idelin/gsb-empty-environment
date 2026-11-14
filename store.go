package netsec

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// SaveInput carries the data for one store write.
type SaveInput struct {
	TenantID    string
	BusinessKey string
	OwnerID     string
	Status      Status
	Fields      map[string]string
	ActorID     string
}

// Store is the persistence boundary used by the import merger.
// Implementations must be safe for concurrent use.
type Store interface {
	// Get returns the current record for (tenantID, businessKey) and
	// ErrNotFound when no such record exists.
	Get(tenantID, businessKey string) (StoredRecord, error)

	// Exists reports whether a record with the key exists in any tenant.
	// It is used to detect cross-tenant collisions on the same business key.
	Exists(businessKey string) bool

	// Save inserts a new record or overwrites an existing one. Overwriting
	// must archive the previous revision and increment the version.
	Save(in SaveInput) (StoredRecord, error)

	// Versions returns the archived revisions of a key, oldest first. The
	// currently active record is not included.
	Versions(tenantID, businessKey string) ([]RecordVersion, error)

	// List returns every current record across all tenants.
	List() ([]StoredRecord, error)
}

// ErrNotFound is returned by Store.Get for missing records.
var ErrNotFound = fmt.Errorf("record not found")

type storeKey struct {
	tenant string
	key    string
}

// MemoryStore is an in-memory Store implementation. It retains every
// overwritten revision so callers can verify the audit trail.
type MemoryStore struct {
	mu      sync.Mutex
	current map[storeKey]StoredRecord
	history map[storeKey][]RecordVersion
	nextID  int64
	now     func() time.Time
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		current: make(map[storeKey]StoredRecord),
		history: make(map[storeKey][]RecordVersion),
		nextID:  1,
		now:     time.Now,
	}
}

func cloneFields(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRecord(r StoredRecord) StoredRecord {
	r.Fields = cloneFields(r.Fields)
	return r
}

// Get implements Store.
func (m *MemoryStore) Get(tenantID, businessKey string) (StoredRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.current[storeKey{tenantID, businessKey}]
	if !ok {
		return StoredRecord{}, ErrNotFound
	}
	return cloneRecord(rec), nil
}

// Exists implements Store.
func (m *MemoryStore) Exists(businessKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sk := range m.current {
		if sk.key == businessKey {
			return true
		}
	}
	return false
}

// Save implements Store. An overwrite archives the previous revision and
// bumps the version; an insert starts at version 1.
func (m *MemoryStore) Save(in SaveInput) (StoredRecord, error) {
	if in.TenantID == "" || in.BusinessKey == "" {
		return StoredRecord{}, fmt.Errorf("tenant and business key are required")
	}
	if !in.Status.Valid() {
		return StoredRecord{}, fmt.Errorf("invalid status %q", in.Status)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sk := storeKey{in.TenantID, in.BusinessKey}
	now := m.now()
	rec := StoredRecord{
		ID:          m.nextID,
		TenantID:    in.TenantID,
		BusinessKey: in.BusinessKey,
		OwnerID:     in.OwnerID,
		Status:      in.Status,
		Fields:      cloneFields(in.Fields),
		UpdatedBy:   in.ActorID,
		UpdatedAt:   now,
	}

	if prev, ok := m.current[sk]; ok {
		m.history[sk] = append(m.history[sk], RecordVersion{
			TenantID:    prev.TenantID,
			BusinessKey: prev.BusinessKey,
			Record:      cloneRecord(prev),
			ArchivedBy:  in.ActorID,
			ArchivedAt:  now,
		})
		rec.ID = prev.ID
		rec.Version = prev.Version + 1
	} else {
		m.nextID++
		rec.Version = 1
	}

	m.current[sk] = rec
	return cloneRecord(rec), nil
}

// Versions implements Store.
func (m *MemoryStore) Versions(tenantID, businessKey string) ([]RecordVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	archived := m.history[storeKey{tenantID, businessKey}]
	out := make([]RecordVersion, len(archived))
	for i, v := range archived {
		v.Record = cloneRecord(v.Record)
		out[i] = v
	}
	return out, nil
}

// List implements Store. Records are returned in a stable key order.
func (m *MemoryStore) List() ([]StoredRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]storeKey, 0, len(m.current))
	for sk := range m.current {
		keys = append(keys, sk)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenant != keys[j].tenant {
			return keys[i].tenant < keys[j].tenant
		}
		return keys[i].key < keys[j].key
	})
	out := make([]StoredRecord, 0, len(keys))
	for _, sk := range keys {
		out = append(out, cloneRecord(m.current[sk]))
	}
	return out, nil
}
