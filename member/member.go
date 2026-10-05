// Package member defines the membership record model and its merge lattice.
// The merge is a join-semilattice: associative, commutative and idempotent,
// so stale, duplicated or reordered deliveries can never move a table
// backwards.
package member

import (
	"sort"
	"sync"
)

// State is the health state of a member. The order matters: within one
// incarnation, alive < suspect < dead and the greater state wins merges.
type State int

const (
	Alive State = iota
	Suspect
	Dead
)

func (s State) String() string {
	switch s {
	case Alive:
		return "alive"
	case Suspect:
		return "suspect"
	case Dead:
		return "dead"
	}
	return "unknown"
}

// Record is one membership entry: (nodeID, incarnation, state).
type Record struct {
	NodeID      string
	Incarnation uint64
	State       State
}

// Merge returns the winner of a and b under lexicographic (Incarnation,
// State) order. It is the join operation of the record lattice.
func Merge(a, b Record) Record {
	if a.NodeID != b.NodeID {
		panic("member.Merge: node ID mismatch")
	}
	switch {
	case b.Incarnation > a.Incarnation:
		return b
	case b.Incarnation < a.Incarnation:
		return a
	case b.State > a.State:
		return b
	default:
		return a
	}
}

// Table is a concurrency-safe membership table.
type Table struct {
	mu      sync.Mutex
	records map[string]Record
	rev     uint64
}

func NewTable() *Table { return &Table{records: make(map[string]Record)} }

// Apply merges recs into the table and returns how many entries changed.
// Because Merge is a lattice join, applying an old response after a newer
// local update is a no-op for the affected records.
func (t *Table) Apply(recs ...Record) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := 0
	for _, r := range recs {
		cur, ok := t.records[r.NodeID]
		if ok {
			if merged := Merge(cur, r); merged != cur {
				t.records[r.NodeID] = merged
				changed++
			}
			continue
		}
		t.records[r.NodeID] = r
		changed++
	}
	t.rev += uint64(changed)
	return changed
}

func (t *Table) Get(id string) (Record, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.records[id]
	return r, ok
}

func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.records)
}

// Revision increases whenever the table changes; servers use it to cache
// snapshots and invalidate them on local updates.
func (t *Table) Revision() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rev
}

// Snapshot returns all records ordered by NodeID.
func (t *Table) Snapshot() []Record {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Record, 0, len(t.records))
	for _, r := range t.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}
