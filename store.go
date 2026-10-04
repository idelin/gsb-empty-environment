package snapshotpage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// Record is one row: a unique ID plus named field values. A field value is
// either nil (NULL) or string, int64, float64, bool.
type Record struct {
	ID     string
	Fields map[string]any
}

// Store is a mutable dataset identified by a snapshot ID. Records may be
// inserted, updated and deleted freely while clients paginate; cursors are
// keyset-based so they stay valid across mutations. Reset starts a new
// snapshot, which invalidates every previously issued cursor.
type Store struct {
	mu         sync.RWMutex
	snapshotID string
	records    map[string]Record
}

// NewStore returns an empty store with a fresh snapshot ID.
func NewStore() *Store {
	return &Store{
		snapshotID: newSnapshotID(),
		records:    make(map[string]Record),
	}
}

func newSnapshotID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf[:])
}

// SnapshotID returns the ID cursors are bound to.
func (s *Store) SnapshotID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotID
}

// Reset discards all records and starts a new snapshot. Any cursor issued
// before Reset fails with ErrSnapshotMismatch afterwards.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotID = newSnapshotID()
	s.records = make(map[string]Record)
}

// Upsert inserts or replaces a record. Field values must be nil, string,
// int64, float64 or bool.
func (s *Store) Upsert(rec Record) error {
	if rec.ID == "" {
		return fmt.Errorf("snapshotpage: record ID must not be empty")
	}
	for name, v := range rec.Fields {
		switch v.(type) {
		case nil, string, int64, float64, bool:
		default:
			return fmt.Errorf("snapshotpage: field %q has unsupported value type %T", name, v)
		}
	}
	fields := make(map[string]any, len(rec.Fields))
	for name, v := range rec.Fields {
		fields[name] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.ID] = Record{ID: rec.ID, Fields: fields}
	return nil
}

// Delete removes a record. Deleting the row a cursor points at is harmless:
// the cursor carries the row's key values, not its position.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
}

// view atomically reads the snapshot ID and a copy of all records.
func (s *Store) view() (string, []Record) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	recs := make([]Record, 0, len(s.records))
	for _, rec := range s.records {
		recs = append(recs, rec)
	}
	return s.snapshotID, recs
}
