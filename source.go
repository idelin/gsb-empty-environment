package exporter

import (
	"fmt"
	"sort"
	"sync"
)

// Record is a single exported row.
type Record struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Source serves paginated reads pinned to a fixed snapshot.
type Source interface {
	// SnapshotID returns the id of the current snapshot.
	SnapshotID() string
	// Page returns up to limit records starting at offset, read from the
	// snapshot identified by snapshotID. It must fail if the snapshot no
	// longer matches the source's current snapshot.
	Page(snapshotID string, offset, limit int) ([]Record, error)
}

// MemSource is an in-memory Source. Every mutation rotates the snapshot id,
// so readers pinned to an older snapshot are rejected.
type MemSource struct {
	mu       sync.Mutex
	snapshot string
	records  []Record
	seq      int
}

func NewMemSource() *MemSource {
	s := &MemSource{}
	s.bumpLocked()
	return s
}

func (s *MemSource) bumpLocked() {
	s.seq++
	s.snapshot = fmt.Sprintf("snap-%d", s.seq)
}

// Set inserts or replaces a record, rotating the snapshot id.
func (s *MemSource) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.records {
		if r.Key == key {
			s.records[i].Value = value
			s.bumpLocked()
			return
		}
	}
	s.records = append(s.records, Record{Key: key, Value: value})
	sort.Slice(s.records, func(i, j int) bool { return s.records[i].Key < s.records[j].Key })
	s.bumpLocked()
}

func (s *MemSource) SnapshotID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

func (s *MemSource) Page(snapshotID string, offset, limit int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshotID != s.snapshot {
		return nil, fmt.Errorf("snapshot %q no longer valid (current %q)", snapshotID, s.snapshot)
	}
	if offset < 0 || limit <= 0 {
		return nil, fmt.Errorf("invalid offset/limit: %d/%d", offset, limit)
	}
	if offset >= len(s.records) {
		return nil, nil
	}
	end := offset + limit
	if end > len(s.records) {
		end = len(s.records)
	}
	out := make([]Record, end-offset)
	copy(out, s.records[offset:end])
	return out, nil
}
