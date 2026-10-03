package hexgrid

import "sync"

// Record is one stored cell entry inspected by the Inspector.
//
// Parent is a declared reference to the parent record; the zero Cell
// means "no parent declared". Neighbors declares adjacency references.
// Payload is an opaque data weight used for skew analysis.
type Record struct {
	Cell      Cell
	Parent    Cell
	Neighbors []Cell
	Payload   int64
}

// Store is a concurrency-safe in-memory cell registry. All accesses are
// guarded by a RWMutex and records are deep-copied on the way in and
// out, so callers can never alias internal state.
type Store struct {
	mu   sync.RWMutex
	recs map[Cell]Record
}

func NewStore() *Store {
	return &Store{recs: make(map[Cell]Record)}
}

// Upsert inserts or replaces a record (keyed by Record.Cell).
func (s *Store) Upsert(r Record) {
	r.Neighbors = append([]Cell(nil), r.Neighbors...)
	s.mu.Lock()
	s.recs[r.Cell] = r
	s.mu.Unlock()
}

// Get fetches a record by cell.
func (s *Store) Get(c Cell) (Record, bool) {
	s.mu.RLock()
	r, ok := s.recs[c]
	s.mu.RUnlock()
	if ok {
		r.Neighbors = append([]Cell(nil), r.Neighbors...)
	}
	return r, ok
}

// Delete removes a record, reporting whether it existed.
func (s *Store) Delete(c Cell) bool {
	s.mu.Lock()
	_, ok := s.recs[c]
	delete(s.recs, c)
	s.mu.Unlock()
	return ok
}

// Len returns the number of stored records.
func (s *Store) Len() int {
	s.mu.RLock()
	n := len(s.recs)
	s.mu.RUnlock()
	return n
}

// Snapshot returns a consistent deep copy of all records.
func (s *Store) Snapshot() []Record {
	s.mu.RLock()
	out := make([]Record, 0, len(s.recs))
	for _, r := range s.recs {
		r.Neighbors = append([]Cell(nil), r.Neighbors...)
		out = append(out, r)
	}
	s.mu.RUnlock()
	return out
}
