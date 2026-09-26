package syncsim

import "strings"

// Version is a Lamport-style logical version: (Clock, Node).
type Version struct {
	Clock uint64
	Node  string
}

// Compare orders versions lexicographically by (Clock, Node).
func (a Version) Compare(b Version) int {
	switch {
	case a.Clock < b.Clock:
		return -1
	case a.Clock > b.Clock:
		return 1
	}
	return strings.Compare(a.Node, b.Node)
}

// Record is a single key's replicated state. A tombstone is a normal
// record with Tombstone set; it carries a version like any write, so a
// delete can win/lose conflicts by the same explicit rule as a put.
type Record struct {
	Key       string
	Value     string
	Ver       Version
	Tombstone bool
}

// Resolve applies the explicit conflict rule:
// the record with the higher (Clock, Node) version wins.
// The rule is total and deterministic, so every node that has seen the
// same set of records resolves every key identically.
func Resolve(a, b Record) Record {
	if a.Ver.Compare(b.Ver) >= 0 {
		return a
	}
	return b
}

// Store is one node's durable key/value state plus its logical clock.
type Store struct {
	nodeID  string
	clock   uint64
	records map[string]Record
}

func NewStore(nodeID string) *Store {
	return &Store{nodeID: nodeID, records: make(map[string]Record)}
}

// Put writes a value with a fresh local version.
func (s *Store) Put(key, value string) Record {
	s.clock++
	r := Record{Key: key, Value: value, Ver: Version{Clock: s.clock, Node: s.nodeID}}
	s.records[key] = r
	return r
}

// Delete writes a tombstone with a fresh local version. The tombstone is
// retained so the delete itself replicates and older values cannot win.
func (s *Store) Delete(key string) Record {
	s.clock++
	r := Record{Key: key, Ver: Version{Clock: s.clock, Node: s.nodeID}, Tombstone: true}
	s.records[key] = r
	return r
}

// Apply merges a remote record under the explicit conflict rule.
// It returns true iff local state changed. Because the rule only ever
// moves a key to a higher version, Apply is idempotent and
// commutative — safe against duplicate and reordered deliveries.
func (s *Store) Apply(r Record) bool {
	cur, ok := s.records[r.Key]
	if ok && cur.Ver.Compare(r.Ver) >= 0 {
		return false
	}
	s.records[r.Key] = r
	return true
}

// Get returns the raw record (possibly a tombstone) for a key.
func (s *Store) Get(key string) (Record, bool) {
	r, ok := s.records[key]
	return r, ok
}

// LiveValue reports the value of a key that is present and not deleted.
func (s *Store) LiveValue(key string) (string, bool) {
	r, ok := s.records[key]
	if !ok || r.Tombstone {
		return "", false
	}
	return r.Value, true
}

// Records returns a copy of all records, tombstones included.
func (s *Store) Records() map[string]Record {
	out := make(map[string]Record, len(s.records))
	for k, r := range s.records {
		out[k] = r
	}
	return out
}
