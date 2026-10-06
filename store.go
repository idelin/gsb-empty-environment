// Package reservation implements an in-memory key-value store with
// serializable transactions over integer keys.
//
// Each transaction records:
//   - read predicates: half-open intervals [Lo, Hi) observed by Get/Scan,
//     recorded even when the result is empty;
//   - a write set: keys to put or delete.
//
// Commit validates against the committed history: if any transaction
// committed after this transaction's snapshot inserted or deleted a key
// inside one of its read predicates (which would change its read results),
// the commit is rejected. Writes outside all predicates never conflict.
package reservation

import (
	"errors"
	"sort"
	"sync"
)

// ErrConflict is returned by Commit when validation fails.
var ErrConflict = errors.New("reservation: serialization conflict")

// Interval is a half-open key range [Lo, Hi).
type Interval struct {
	Lo, Hi int
}

// Contains reports whether key k falls in [Lo, Hi).
func (iv Interval) Contains(k int) bool { return iv.Lo <= k && k < iv.Hi }

// writeKind describes a committed mutation.
type writeKind int

const (
	put writeKind = iota
	del
)

// WriteRecord is one committed mutation in the history.
type WriteRecord struct {
	Seq  uint64 // commit sequence, strictly increasing
	Key  int
	Kind writeKind
}

// Entry is a key/value pair returned by Scan.
type Entry struct {
	Key   int
	Value string
}

// Store is the committed state plus the committed write history.
type Store struct {
	mu      sync.Mutex
	data    map[int]string
	seq     uint64
	history []WriteRecord
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{data: make(map[int]string)}
}

// History returns a copy of the committed write history.
func (s *Store) History() []WriteRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WriteRecord, len(s.history))
	copy(out, s.history)
	return out
}

// Begin starts a transaction at the current commit sequence.
func (s *Store) Begin() *Txn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Txn{
		store:    s,
		snapshot: s.seq,
		writes:   make(map[int]*string),
	}
}

// Txn is a single optimistic transaction.
type Txn struct {
	store      *Store
	snapshot   uint64
	predicates []Interval
	writes     map[int]*string // nil value means delete
	done       bool
}

// Get reads one key, recording the degenerate predicate [key, key+1).
func (t *Txn) Get(key int) (string, bool) {
	t.predicates = append(t.predicates, Interval{key, key + 1})
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	v, ok := t.store.data[key]
	return v, ok
}

// Scan returns committed entries in [lo, hi) and records the predicate,
// even when the result is empty.
func (t *Txn) Scan(lo, hi int) []Entry {
	t.predicates = append(t.predicates, Interval{lo, hi})
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	var out []Entry
	for k, v := range t.store.data {
		if lo <= k && k < hi {
			out = append(out, Entry{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Set stages a put.
func (t *Txn) Set(key int, value string) {
	v := value
	t.writes[key] = &v
}

// Delete stages a delete.
func (t *Txn) Delete(key int) {
	t.writes[key] = nil
}

// Commit validates the read predicates against the committed history and,
// if no snapshot-later write intersects them, applies the write set.
func (t *Txn) Commit() error {
	if t.done {
		return errors.New("reservation: transaction already finished")
	}
	t.done = true

	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, rec := range s.history {
		if rec.Seq <= t.snapshot {
			continue
		}
		for _, p := range t.predicates {
			if p.Contains(rec.Key) {
				return ErrConflict
			}
		}
	}

	s.seq++
	for k, vp := range t.writes {
		if vp == nil {
			delete(s.data, k)
			s.history = append(s.history, WriteRecord{Seq: s.seq, Key: k, Kind: del})
		} else {
			s.data[k] = *vp
			s.history = append(s.history, WriteRecord{Seq: s.seq, Key: k, Kind: put})
		}
	}
	return nil
}
