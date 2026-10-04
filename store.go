// Package antientropy implements an anti-entropy synchronization tool:
// two peers each build a fixed-range digest tree over their key-value set,
// then recursively exchange digests to locate and reconcile differences.
package antientropy

import "sort"

// Record is a single versioned key-value entry. Tombstones (Deleted=true)
// are kept in the store and participate in digest computation so that
// deletions propagate like ordinary updates.
type Record struct {
	Key     string
	Value   []byte
	Version uint64
	Deleted bool
}

// digest returns the record's content hash. The deleted flag and version
// are mixed in, so a tombstone and a live record never collide.
func (r Record) digest() uint64 {
	h := fnv1a64([]byte(r.Key))
	h = fnv1a64Add(h, uint64Bytes(r.Version))
	if r.Deleted {
		h = fnv1a64Add(h, []byte{1})
	} else {
		h = fnv1a64Add(h, []byte{0})
	}
	h = fnv1a64Add(h, r.Value)
	return splitmix64(h)
}

// Store is a versioned key-value set with tombstone deletes.
type Store struct {
	records map[string]Record
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{records: make(map[string]Record)}
}

// Put upserts a live record at the given version.
func (s *Store) Put(key string, value []byte, version uint64) {
	s.apply(Record{Key: key, Value: value, Version: version})
}

// Delete writes a tombstone at the given version.
func (s *Store) Delete(key string, version uint64) {
	s.apply(Record{Key: key, Version: version, Deleted: true})
}

// Get returns the record for key, if any.
func (s *Store) Get(key string) (Record, bool) {
	r, ok := s.records[key]
	return r, ok
}

// Len returns the number of records including tombstones.
func (s *Store) Len() int { return len(s.records) }

// apply merges a record using last-writer-wins semantics:
// higher version wins; on a tie the tombstone wins; if still tied the
// lexicographically larger value wins, so convergence is deterministic.
func (s *Store) apply(r Record) bool {
	cur, ok := s.records[r.Key]
	if !ok || wins(r, cur) {
		s.records[r.Key] = r
		return true
	}
	return false
}

func wins(a, b Record) bool {
	if a.Version != b.Version {
		return a.Version > b.Version
	}
	if a.Deleted != b.Deleted {
		return a.Deleted
	}
	return string(a.Value) > string(b.Value)
}

// keysInRange returns the sorted keys whose placement hash falls in [lo, hi).
func (s *Store) keysInRange(lo, hi uint64) []string {
	var keys []string
	for k := range s.records {
		h := KeyHash(k)
		if h >= lo && h < hi {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// equal reports whether both stores hold identical records.
func (s *Store) equal(o *Store) bool {
	if len(s.records) != len(o.records) {
		return false
	}
	for k, r := range s.records {
		or, ok := o.records[k]
		if !ok || or.Version != r.Version || or.Deleted != r.Deleted ||
			string(or.Value) != string(r.Value) {
			return false
		}
	}
	return true
}
