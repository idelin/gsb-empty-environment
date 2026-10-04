// Package ormap implements an observed-remove multi-value map CRDT.
//
// Every assignment is tagged with a unique dot (replica, counter). A
// remove carries exactly the dots the remover had observed for the key
// and kills only those; concurrent assignments it never saw survive.
// Both operations and states merge with commutative, associative and
// idempotent joins. Tombstones are compressed into per-replica counter
// ranges that keep precisely the causal information needed to stop
// replays of old assignments from resurrecting removed values.
package ormap

import (
	"fmt"
	"sort"
)

// Dot uniquely identifies one assignment: (replica, counter).
type Dot struct {
	Replica string `json:"replica"`
	Counter uint64 `json:"counter"`
}

func (d Dot) String() string { return fmt.Sprintf("%s:%d", d.Replica, d.Counter) }

// Version is one assigned value with its causal metadata.
type Version struct {
	Dot   Dot    `json:"dot"`
	Value string `json:"value"`
	Time  uint64 `json:"time"` // Lamport timestamp of the assignment
}

// DotConflictError is returned when one dot is seen carrying different
// content. That can only come from a Byzantine replica or a bug, so it
// is rejected instead of silently merged.
type DotConflictError struct {
	Dot      Dot
	Existing Version
	Incoming Version
}

func (e *DotConflictError) Error() string {
	return fmt.Sprintf("ormap: dot %s seen with conflicting content (%q@%d vs %q@%d)",
		e.Dot, e.Existing.Value, e.Existing.Time, e.Incoming.Value, e.Incoming.Time)
}

// ranges is a compressed tombstone set for one replica: sorted,
// disjoint, non-adjacent inclusive [lo, hi] counter intervals.
type ranges [][2]uint64

func (rs ranges) contains(c uint64) bool {
	i := sort.Search(len(rs), func(i int) bool { return rs[i][0] > c })
	return i > 0 && rs[i-1][1] >= c
}

func (rs ranges) insert(c uint64) ranges {
	if rs.contains(c) {
		return rs
	}
	lo, hi := c, c
	i := sort.Search(len(rs), func(i int) bool { return rs[i][0] > c })
	start, end := i, i
	if i > 0 && rs[i-1][1]+1 >= lo {
		start = i - 1
		lo = rs[i-1][0]
	}
	for end < len(rs) && rs[end][0] <= hi+1 {
		if rs[end][1] > hi {
			hi = rs[end][1]
		}
		end++
	}
	out := make(ranges, 0, len(rs)-(end-start)+1)
	out = append(out, rs[:start]...)
	out = append(out, [2]uint64{lo, hi})
	return append(out, rs[end:]...)
}

// normalizeRanges sorts and coalesces arbitrary input, accepting
// overlapping, adjacent or unsorted intervals.
func normalizeRanges(in [][2]uint64) ranges {
	if len(in) == 0 {
		return nil
	}
	sorted := append(ranges(nil), in...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	out := sorted[:1]
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if r[0] <= last[1]+1 {
			if r[1] > last[1] {
				last[1] = r[1]
			}
		} else {
			out = append(out, r)
		}
	}
	return out
}

func unionRanges(a, b ranges) ranges {
	return normalizeRanges(append(append(ranges(nil), a...), b...))
}

// keyState is the per-key join-semilattice payload: a dot store of live
// versions plus the causal context of removed dots.
type keyState struct {
	live  map[Dot]Version
	tombs map[string]ranges // removed dots per replica, compressed
}

func (ks *keyState) tombstoned(d Dot) bool {
	rs, ok := ks.tombs[d.Replica]
	return ok && rs.contains(d.Counter)
}

// observed returns every dot known for the key, live or removed, in
// deterministic order. Removes carry exactly this set.
func (ks *keyState) observed() []Dot {
	var dots []Dot
	for d := range ks.live {
		dots = append(dots, d)
	}
	for rep, rs := range ks.tombs {
		for _, r := range rs {
			for c := r[0]; c <= r[1]; c++ {
				dots = append(dots, Dot{Replica: rep, Counter: c})
			}
		}
	}
	sort.Slice(dots, func(i, j int) bool {
		if dots[i].Replica != dots[j].Replica {
			return dots[i].Replica < dots[j].Replica
		}
		return dots[i].Counter < dots[j].Counter
	})
	return dots
}

func (ks *keyState) empty() bool { return len(ks.live) == 0 && len(ks.tombs) == 0 }

// OpKind discriminates replicated operations.
type OpKind int

const (
	// OpAdd assigns Version to Key.
	OpAdd OpKind = iota
	// OpRemove removes exactly the Dots observed by the remover.
	OpRemove
)

// Op is one replicated operation. Ops are idempotent and
// order-independent, so transport may duplicate and reorder them.
type Op struct {
	Kind    OpKind  `json:"kind"`
	Key     string  `json:"key"`
	Version Version `json:"version,omitempty"` // OpAdd payload
	Dots    []Dot   `json:"dots,omitempty"`    // OpRemove payload
}

// Map is one replica of the observed-remove multi-value map.
type Map struct {
	ReplicaID string
	counter   uint64 // last dot counter issued by this replica
	clock     uint64 // Lamport clock
	keys      map[string]*keyState
}

// NewMap creates a replica with the given ID.
func NewMap(replica string) *Map {
	return &Map{ReplicaID: replica, keys: make(map[string]*keyState)}
}

func (m *Map) keyState(key string) *keyState {
	ks, ok := m.keys[key]
	if !ok {
		ks = &keyState{live: make(map[Dot]Version), tombs: make(map[string]ranges)}
		m.keys[key] = ks
	}
	return ks
}

// Assign sets key to value, stamps a fresh dot and Lamport time,
// applies the change locally and returns the op to broadcast.
func (m *Map) Assign(key, value string) Op {
	m.counter++
	m.clock++
	op := Op{Kind: OpAdd, Key: key, Version: Version{
		Dot:   Dot{Replica: m.ReplicaID, Counter: m.counter},
		Value: value,
		Time:  m.clock,
	}}
	if err := m.Apply(op); err != nil {
		panic(err) // a fresh dot can never conflict
	}
	return op
}

// Observe returns every dot currently known for key, live or removed.
func (m *Map) Observe(key string) []Dot {
	ks, ok := m.keys[key]
	if !ok {
		return nil
	}
	return ks.observed()
}

// Remove deletes exactly the dots observed for key at call time and
// returns the op to broadcast. Concurrent assignments not yet observed
// by this replica are not affected.
func (m *Map) Remove(key string) Op {
	op := Op{Kind: OpRemove, Key: key, Dots: m.Observe(key)}
	if err := m.Apply(op); err != nil {
		panic(err) // removes cannot conflict
	}
	return op
}

// Apply delivers one operation to this replica.
func (m *Map) Apply(op Op) error {
	switch op.Kind {
	case OpAdd:
		return m.applyAdd(op)
	case OpRemove:
		m.applyRemove(op)
		return nil
	default:
		return fmt.Errorf("ormap: unknown op kind %d", op.Kind)
	}
}

func (m *Map) applyAdd(op Op) error {
	v := op.Version
	m.observeTime(v.Time)
	ks := m.keyState(op.Key)
	if ks.tombstoned(v.Dot) {
		return nil // replay of an already-removed assignment
	}
	if existing, ok := ks.live[v.Dot]; ok {
		if existing != v {
			return &DotConflictError{Dot: v.Dot, Existing: existing, Incoming: v}
		}
		return nil // duplicate delivery
	}
	ks.live[v.Dot] = v
	if v.Dot.Replica == m.ReplicaID && v.Dot.Counter > m.counter {
		m.counter = v.Dot.Counter
	}
	return nil
}

func (m *Map) applyRemove(op Op) {
	if len(op.Dots) == 0 {
		return
	}
	ks := m.keyState(op.Key)
	for _, d := range op.Dots {
		delete(ks.live, d)
		ks.tombs[d.Replica] = ks.tombs[d.Replica].insert(d.Counter)
	}
}

func (m *Map) observeTime(t uint64) {
	if t > m.clock {
		m.clock = t
	}
}

// Merge joins the state of another replica into this one. The join is
// commutative, associative and idempotent. A dot seen with different
// content in the two states aborts the merge before any mutation.
func (m *Map) Merge(o *Map) error {
	for key, oks := range o.keys {
		ks, ok := m.keys[key]
		if !ok {
			continue
		}
		for d, v := range oks.live {
			if existing, ok := ks.live[d]; ok && existing != v {
				return &DotConflictError{Dot: d, Existing: existing, Incoming: v}
			}
		}
	}
	for key, oks := range o.keys {
		ks := m.keyState(key)
		for rep, rs := range oks.tombs {
			ks.tombs[rep] = unionRanges(ks.tombs[rep], rs)
		}
		for d, v := range oks.live {
			if _, ok := ks.live[d]; !ok {
				ks.live[d] = v
			}
		}
		for d := range ks.live {
			if ks.tombstoned(d) {
				delete(ks.live, d)
			}
		}
		if ks.empty() {
			delete(m.keys, key)
		}
	}
	m.observeTime(o.clock)
	if o.ReplicaID == m.ReplicaID && o.counter > m.counter {
		m.counter = o.counter
	}
	return nil
}

// Live returns all surviving assignments for key, sorted by logical
// time, then replica ID, then counter.
func (m *Map) Live(key string) []Version {
	ks, ok := m.keys[key]
	if !ok {
		return nil
	}
	var out []Version
	for _, v := range ks.live {
		out = append(out, v)
	}
	sortVersions(out)
	return out
}

// Display returns the single display value for key: latest logical
// time wins, ties break by replica ID. Concurrent losers stay live;
// nothing is deleted for display convenience.
func (m *Map) Display(key string) (Version, bool) {
	return Display(m.Live(key))
}

// Conflict reports whether key holds more than one distinct live value.
func (m *Map) Conflict(key string) bool {
	return Conflict(m.Live(key))
}

func sortVersions(vs []Version) {
	sort.Slice(vs, func(i, j int) bool { return lessVersion(vs[i], vs[j]) })
}

// lessVersion orders by logical time, then replica ID, then counter,
// giving a deterministic total order over versions.
func lessVersion(a, b Version) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	if a.Dot.Replica != b.Dot.Replica {
		return a.Dot.Replica < b.Dot.Replica
	}
	return a.Dot.Counter < b.Dot.Counter
}

// Display picks the display value out of a live set.
func Display(live []Version) (Version, bool) {
	if len(live) == 0 {
		return Version{}, false
	}
	best := live[0]
	for _, v := range live[1:] {
		if lessVersion(best, v) {
			best = v
		}
	}
	return best, true
}

// Conflict reports whether a live set holds distinct values.
func Conflict(live []Version) bool {
	if len(live) < 2 {
		return false
	}
	for _, v := range live[1:] {
		if v.Value != live[0].Value {
			return true
		}
	}
	return false
}
