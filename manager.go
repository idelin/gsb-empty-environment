package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Record is one indexed value. The manager validates it, computes an index, and
// does not retain the Record after the call returns.
type Record struct {
	Tenant     string
	Collection string
	Field      string
	Value      string
}

// Witness is a host-key-authenticated statement binding a record's previous
// index to a migration from one generation to another. A client cannot create
// a valid Witness without the host's key material.
type Witness struct {
	mac [sha256.Size]byte
}

// Equal reports whether two witnesses are identical.
func (w Witness) Equal(other Witness) bool {
	return subtle.ConstantTimeCompare(w.mac[:], other.mac[:]) == 1
}

// IsZero reports whether the witness is the zero value.
func (w Witness) IsZero() bool {
	var zero Witness
	return w.Equal(zero)
}

// MigrationEntry identifies one registered record that must be moved from From
// to To. The manager issues entries through PendingMigrations.
type MigrationEntry struct {
	RecordID string
	From     Version
	To       Version
	Witness  Witness
}

// QueryResult contains current-generation indexes and, while a rotation is in
// progress, the previous-generation index as well.
type QueryResult struct {
	Current  Index
	Previous *Index
}

// Indexes returns the available indexes in current, previous order.
func (q QueryResult) Indexes() []Index {
	out := make([]Index, 1, 2)
	out[0] = q.Current
	if q.Previous != nil {
		out = append(out, *q.Previous)
	}
	return out
}

// ForVersion returns the index for version when it is part of this result.
func (q QueryResult) ForVersion(version Version) (Index, bool) {
	if q.Current.Version == version {
		return q.Current, true
	}
	if q.Previous != nil && q.Previous.Version == version {
		return *q.Previous, true
	}
	return Index{}, false
}

// Manager owns the active generation state and coordinates rotation,
// migration, and retirement. It serializes all state transitions so that a
// concurrent write cannot escape registration during a rotation.
type Manager struct {
	mu       sync.Mutex
	indexer  *Indexer
	store    Store
	current  Version
	previous Version
	retired  map[Version]struct{}
	records  map[string]*managedRecord
}

type managedRecord struct {
	indexes map[Version]Index
	pending *MigrationEntry
}

// NewManager creates a manager with initial as the current generation. The key
// handle must be able to supply a key for initial and must continue to supply
// the previous generation's key until retirement completes.
func NewManager(keys KeyHandle, store Store, initial Version) (*Manager, error) {
	if keys == nil {
		return nil, ErrNilKeyHandle
	}
	if store == nil {
		return nil, ErrNilStore
	}
	if initial == "" {
		return nil, ErrEmptyDomain
	}
	indexer, err := NewIndexer(keys)
	if err != nil {
		return nil, err
	}
	key, err := keys.Key(initial)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}
	return &Manager{
		indexer: indexer,
		store:   store,
		current: initial,
		retired: make(map[Version]struct{}),
		records: make(map[string]*managedRecord),
	}, nil
}

// NewMemoryManager creates a manager backed by a new MemoryStore.
func NewMemoryManager(keys KeyHandle, initial Version) (*Manager, error) {
	return NewManager(keys, NewMemoryStore(), initial)
}

// Store returns the manager's backing store.
func (m *Manager) Store() Store {
	if m == nil {
		return nil
	}
	return m.store
}

// Current returns the active write generation.
func (m *Manager) Current() Version {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Previous returns the previous generation while a rotation is active.
func (m *Manager) Previous() (Version, bool) {
	if m == nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.previous, m.previous != ""
}

// Retired reports whether version has been retired.
func (m *Manager) Retired(version Version) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.retired[version]
	return ok
}

// Write computes and stores an index using only the current generation. It
// registers the record atomically with respect to rotation, migration, and
// retirement. Record IDs are immutable index-entry identifiers.
func (m *Manager) Write(recordID string, record Record) (Index, error) {
	if recordID == "" {
		return Index{}, ErrEmptyRecordID
	}
	if m == nil {
		return Index{}, ErrNilStore
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.records[recordID]; exists {
		return Index{}, ErrRecordExists
	}

	index, err := m.indexer.Index(
		m.current,
		record.Tenant,
		record.Collection,
		record.Field,
		record.Value,
	)
	if err != nil {
		return Index{}, err
	}
	if err := m.store.Put(recordID, index); err != nil {
		return Index{}, err
	}
	m.records[recordID] = &managedRecord{
		indexes: map[Version]Index{index.Version: index},
	}
	return index, nil
}

// Query returns the current-generation index and, during a rotation, the
// previous-generation index. After retirement the previous index is absent.
func (m *Manager) Query(tenant, collection, field, value string) (QueryResult, error) {
	if m == nil {
		return QueryResult{}, ErrNilStore
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.indexer.Index(m.current, tenant, collection, field, value)
	if err != nil {
		return QueryResult{}, err
	}
	result := QueryResult{Current: current}
	if m.previous != "" {
		previous, err := m.indexer.Index(m.previous, tenant, collection, field, value)
		if err != nil {
			return QueryResult{}, err
		}
		result.Previous = &previous
	}
	return result, nil
}

// QueryVersion computes an index for an explicitly requested generation.
// Retired generations are rejected, even if the host key handle still contains
// their key.
func (m *Manager) QueryVersion(version Version, tenant, collection, field, value string) (Index, error) {
	if m == nil {
		return Index{}, ErrNilStore
	}
	if err := validateIndexDomains(version, tenant, collection, field); err != nil {
		return Index{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, retired := m.retired[version]; retired {
		return Index{}, ErrRetiredGeneration
	}
	if version != m.current && (m.previous == "" || version != m.previous) {
		return Index{}, ErrUnknownGeneration
	}
	return m.indexer.Index(version, tenant, collection, field, value)
}

// StartRotation makes next the only write generation and retains the old
// generation for queries. Existing records receive trusted migration entries.
// New writes after this call use next and are already current.
func (m *Manager) StartRotation(next Version) error {
	if m == nil {
		return ErrNilStore
	}
	if next == "" {
		return ErrEmptyDomain
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous != "" {
		return ErrRotationInProgress
	}
	if next == m.current {
		return ErrSameGeneration
	}
	if _, reused := m.retired[next]; reused {
		return ErrGenerationReuse
	}
	if key, err := m.indexer.keys.Key(next); err != nil {
		return err
	} else if len(key) == 0 {
		return ErrInvalidKey
	}

	oldVersion := m.current
	oldKey, err := m.indexer.keys.Key(oldVersion)
	if err != nil {
		return err
	}
	if len(oldKey) == 0 {
		return ErrInvalidKey
	}

	pending := make(map[string]*MigrationEntry, len(m.records))
	for recordID, record := range m.records {
		oldIndex, ok := record.indexes[oldVersion]
		if !ok {
			return ErrMigrationIncomplete
		}
		pending[recordID] = &MigrationEntry{
			RecordID: recordID,
			From:     oldVersion,
			To:       next,
			Witness:  makeWitness(oldKey, recordID, oldIndex, next),
		}
	}

	m.previous = oldVersion
	m.current = next
	for recordID, entry := range pending {
		m.records[recordID].pending = entry
	}
	return nil
}

// PendingMigrations returns a deterministic snapshot of entries that still
// need a current-generation index. Entries contain no plaintext.
func (m *Manager) PendingMigrations() []MigrationEntry {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous == "" {
		return nil
	}

	out := make([]MigrationEntry, 0, len(m.records))
	for _, record := range m.records {
		if record.pending != nil {
			out = append(out, *record.pending)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RecordID < out[j].RecordID })
	return out
}

// Migrate creates and stores the current-generation index for entry.
//
// Migrate does not accept a completion flag. It verifies the host-key witness,
// then proves that record reproduces the stored previous-generation index
// before committing the new index.
func (m *Manager) Migrate(entry MigrationEntry, record Record) (Index, error) {
	if m == nil {
		return Index{}, ErrNilStore
	}
	if entry.RecordID == "" {
		return Index{}, ErrEmptyRecordID
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous == "" {
		return Index{}, ErrNoRotation
	}
	if entry.From != m.previous || entry.To != m.current || entry.Witness.IsZero() {
		return Index{}, ErrInvalidWitness
	}

	state, ok := m.records[entry.RecordID]
	if !ok {
		return Index{}, ErrUnknownRecord
	}
	if state.pending == nil {
		return Index{}, ErrNotPendingMigration
	}
	if *state.pending != entry {
		return Index{}, ErrInvalidWitness
	}

	oldIndex, ok := state.indexes[entry.From]
	if !ok {
		return Index{}, ErrMigrationProof
	}
	oldKey, err := m.indexer.keys.Key(entry.From)
	if err != nil {
		return Index{}, err
	}
	if len(oldKey) == 0 {
		return Index{}, ErrInvalidKey
	}
	expectedWitness := makeWitness(oldKey, entry.RecordID, oldIndex, entry.To)
	if !entry.Witness.Equal(expectedWitness) {
		return Index{}, ErrInvalidWitness
	}

	proof, err := m.indexer.Index(
		entry.From,
		record.Tenant,
		record.Collection,
		record.Field,
		record.Value,
	)
	if err != nil {
		return Index{}, err
	}
	if !proof.Equal(oldIndex) {
		return Index{}, ErrMigrationProof
	}

	current, err := m.indexer.Index(
		entry.To,
		record.Tenant,
		record.Collection,
		record.Field,
		record.Value,
	)
	if err != nil {
		return Index{}, err
	}
	if err := m.store.Put(entry.RecordID, current); err != nil {
		return Index{}, err
	}

	state.indexes[current.Version] = current
	state.pending = nil
	return current, nil
}

// RetirePrevious removes the previous generation after verifying that every
// registered record has the exact current-generation index committed. If the
// store supports VersionDeleter, old-generation entries are purged as part of
// the same serialized transition.
func (m *Manager) RetirePrevious() error {
	if m == nil {
		return ErrNilStore
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous == "" {
		return ErrNoRotation
	}

	recordIDs := make([]string, 0, len(m.records))
	for recordID := range m.records {
		recordIDs = append(recordIDs, recordID)
	}
	sort.Strings(recordIDs)

	for _, recordID := range recordIDs {
		record := m.records[recordID]
		want, ok := record.indexes[m.current]
		if !ok {
			return fmt.Errorf("%w: record %q", ErrMigrationIncomplete, recordID)
		}
		got, found, err := m.store.Get(recordID, m.current)
		if err != nil {
			return err
		}
		if !found || !got.Equal(want) {
			return fmt.Errorf("%w: record %q", ErrMigrationIncomplete, recordID)
		}
	}

	oldVersion := m.previous
	if deleter, ok := m.store.(VersionDeleter); ok {
		if err := deleter.DeleteVersion(oldVersion); err != nil {
			return err
		}
	}
	for _, record := range m.records {
		delete(record.indexes, oldVersion)
		record.pending = nil
	}
	m.retired[oldVersion] = struct{}{}
	m.previous = ""
	return nil
}

func makeWitness(key []byte, recordID string, oldIndex Index, to Version) Witness {
	mac := hmac.New(sha256.New, key)
	writeComponent(mac, "netsec/version-witness/v1")
	writeComponent(mac, recordID)
	writeComponent(mac, string(oldIndex.Version))
	writeComponent(mac, string(to))
	writeComponent(mac, string(oldIndex.Digest[:]))

	var witness Witness
	copy(witness.mac[:], mac.Sum(nil))
	return witness
}

var (
	// ErrRotationInProgress is returned when a rotation has not been retired.
	ErrRotationInProgress = errors.New("netsec: previous generation is still active")
	// ErrNoRotation is returned when an operation requires an active rotation.
	ErrNoRotation = errors.New("netsec: no active generation rotation")
	// ErrSameGeneration is returned when rotating a generation into itself.
	ErrSameGeneration = errors.New("netsec: next generation equals current generation")
	// ErrGenerationReuse is returned when a retired generation is reused.
	ErrGenerationReuse = errors.New("netsec: generation was already retired")
	// ErrRetiredGeneration is returned for an old-generation query after retirement.
	ErrRetiredGeneration = errors.New("netsec: generation has been retired")
	// ErrUnknownGeneration is returned for a generation the manager does not
	// currently serve.
	ErrUnknownGeneration = errors.New("netsec: generation is not current or previous")
	// ErrRecordExists is returned when a write reuses an existing record ID.
	ErrRecordExists = errors.New("netsec: record already registered")
	// ErrUnknownRecord is returned when a migration names an unregistered record.
	ErrUnknownRecord = errors.New("netsec: record is not registered")
	// ErrNotPendingMigration is returned when a migration is not outstanding.
	ErrNotPendingMigration = errors.New("netsec: record has no pending migration")
	// ErrInvalidWitness is returned when a migration witness is forged, stale,
	// or bound to different generations or a different record.
	ErrInvalidWitness = errors.New("netsec: invalid migration witness")
	// ErrMigrationProof is returned when supplied source data does not reproduce
	// the stored previous-generation index.
	ErrMigrationProof = errors.New("netsec: migration source does not match previous index")
	// ErrMigrationIncomplete is returned when retirement finds a registered
	// record without an exact current-generation index in the store.
	ErrMigrationIncomplete = errors.New("netsec: migration incomplete")
)
