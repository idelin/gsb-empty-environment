// Package orset implements an Observed-Remove Set (OR-Set) CRDT.
//
// Design:
//   - Every Add is tagged with a globally unique Dot{Replica, Seq}.
//   - Remove(e) tombstones exactly the add-dots this replica has OBSERVED
//     for e; concurrently created (unobserved) dots survive.
//   - Merge is set-union over adds and tombstones, hence commutative,
//     associative and idempotent.
//   - Tombstones are garbage-collected only after the remove event is
//     CAUSALLY STABLE: every replica is known (via a gossiped matrix
//     clock) to have observed it. Dropping a tombstone earlier lets a
//     stale replica reintroduce the element (see DropTombstonesUnsafe
//     and the counterexample test).
package orset

import "sort"

// Dot uniquely identifies an event (add or remove) issued by a replica.
type Dot struct {
	Replica string
	Seq     uint64
}

// Less is a deterministic total order on dots, used to resolve duplicate
// tombstone entries for the same add-dot deterministically.
func (d Dot) Less(o Dot) bool {
	if d.Seq != o.Seq {
		return d.Seq < o.Seq
	}
	return d.Replica < o.Replica
}

// Snapshot is an immutable, deep-copied view of a replica's state,
// exchanged during anti-entropy (gossip).
type Snapshot struct {
	ID     string
	Adds   map[string]map[Dot]struct{}  // element -> all add-dots ever observed
	Tombs  map[string]map[Dot]Dot       // element -> add-dot -> remove-dot that killed it
	VV     map[string]uint64            // VV[r] = highest event seq from r applied here
	Matrix map[string]map[string]uint64 // Matrix[p][q] = highest seq of q known by p (as known here)
}

// CoreEqual compares the convergence-relevant payload (adds, tombstones,
// version vector), ignoring the matrix clock used only for GC decisions.
func (a Snapshot) CoreEqual(b Snapshot) bool {
	return dotSetMapEqual(a.Adds, b.Adds) &&
		tombMapEqual(a.Tombs, b.Tombs) &&
		vvEqual(a.VV, b.VV)
}

// ORSet is a single replica of the observed-remove set.
type ORSet struct {
	id     string
	peers  []string // full cluster membership, including id
	clock  uint64
	adds   map[string]map[Dot]struct{}
	tombs  map[string]map[Dot]Dot
	vv     map[string]uint64
	matrix map[string]map[string]uint64
}

// New creates a replica. peers must list every replica id in the cluster
// (including id); it is used only to decide causal stability for GC.
func New(id string, peers []string) *ORSet {
	s := &ORSet{
		id:     id,
		peers:  append([]string(nil), peers...),
		adds:   map[string]map[Dot]struct{}{},
		tombs:  map[string]map[Dot]Dot{},
		vv:     map[string]uint64{},
		matrix: map[string]map[string]uint64{},
	}
	s.matrix[id] = map[string]uint64{}
	return s
}

// next issues the next local event dot and records it in the version vector.
func (s *ORSet) next() Dot {
	s.clock++
	s.vv[s.id] = s.clock
	return Dot{Replica: s.id, Seq: s.clock}
}

// Add inserts e with a fresh, globally unique tag and returns its dot.
func (s *ORSet) Add(e string) Dot {
	d := s.next()
	set := s.adds[e]
	if set == nil {
		set = map[Dot]struct{}{}
		s.adds[e] = set
	}
	set[d] = struct{}{}
	return d
}

// Remove tombstones every add-dot of e observed so far at this replica.
// It returns the remove event's dot and how many tags were tombstoned
// (0 if the remove observed nothing new — a duplicate remove is a no-op
// on the data payload).
func (s *ORSet) Remove(e string) (Dot, int) {
	rd := s.next()
	n := 0
	for d := range s.adds[e] {
		if _, gone := s.tombs[e][d]; gone {
			continue
		}
		if s.tombs[e] == nil {
			s.tombs[e] = map[Dot]Dot{}
		}
		s.tombs[e][d] = rd
		n++
	}
	return rd, n
}

// Contains reports whether e has at least one live (non-tombstoned) add-dot.
func (s *ORSet) Contains(e string) bool {
	for d := range s.adds[e] {
		if _, gone := s.tombs[e][d]; !gone {
			return true
		}
	}
	return false
}

// Elements returns the sorted live members of the set.
func (s *ORSet) Elements() []string {
	var out []string
	for e := range s.adds {
		if s.Contains(e) {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// Snapshot returns a deep copy of the replica state for gossip.
func (s *ORSet) Snapshot() Snapshot {
	snap := Snapshot{
		ID:     s.id,
		Adds:   map[string]map[Dot]struct{}{},
		Tombs:  map[string]map[Dot]Dot{},
		VV:     map[string]uint64{},
		Matrix: map[string]map[string]uint64{},
	}
	for e, dots := range s.adds {
		m := map[Dot]struct{}{}
		for d := range dots {
			m[d] = struct{}{}
		}
		snap.Adds[e] = m
	}
	for e, tm := range s.tombs {
		m := map[Dot]Dot{}
		for ad, rd := range tm {
			m[ad] = rd
		}
		snap.Tombs[e] = m
	}
	for r, n := range s.vv {
		snap.VV[r] = n
	}
	for p, row := range s.matrix {
		m := map[string]uint64{}
		for q, n := range row {
			m[q] = n
		}
		snap.Matrix[p] = m
	}
	return snap
}

// Merge folds a remote snapshot into the local state. Both adds and
// tombstones are merged by set union (with a deterministic max on
// duplicate tombstone entries), so Merge is commutative, associative
// and idempotent.
func (s *ORSet) Merge(o Snapshot) {
	for e, dots := range o.Adds {
		set := s.adds[e]
		if set == nil {
			set = map[Dot]struct{}{}
			s.adds[e] = set
		}
		for d := range dots {
			set[d] = struct{}{}
		}
	}
	for e, tm := range o.Tombs {
		mine := s.tombs[e]
		if mine == nil {
			mine = map[Dot]Dot{}
			s.tombs[e] = mine
		}
		for ad, rd := range tm {
			if cur, ok := mine[ad]; !ok || cur.Less(rd) {
				mine[ad] = rd
			}
		}
	}
	for r, n := range o.VV {
		if n > s.vv[r] {
			s.vv[r] = n
		}
	}
	for p, row := range o.Matrix {
		mr := s.matrix[p]
		if mr == nil {
			mr = map[string]uint64{}
			s.matrix[p] = mr
		}
		for q, n := range row {
			if n > mr[q] {
				mr[q] = n
			}
		}
	}
	// Our own row of the matrix always reflects our real version vector.
	mr := s.matrix[s.id]
	if mr == nil {
		mr = map[string]uint64{}
		s.matrix[s.id] = mr
	}
	for r, n := range s.vv {
		mr[r] = n
	}
}

// stable reports whether the event dot d is causally stable here: every
// replica in the cluster is known to have observed it. Knowledge about
// other replicas is a lower bound of their real version vectors, so a
// positive answer is always sound (a stale answer only delays GC).
func (s *ORSet) stable(d Dot) bool {
	for _, p := range s.peers {
		if s.matrix[p][d.Replica] < d.Seq {
			return false
		}
	}
	return true
}

// GC collects tombstones whose remove event is causally stable, together
// with the add-dots they kill. It returns the number of tombstones dropped.
//
// Safety: if every replica has observed the remove, no replica holds the
// add-dot as live, so deleting both the tombstone and the dead add-dot
// cannot change membership anywhere, now or after any future merge.
func (s *ORSet) GC() int {
	return s.collect(s.stable)
}

// collect drops tombstones whose remove-dot is accepted by admit, together
// with the add-dots they kill.
func (s *ORSet) collect(admit func(Dot) bool) int {
	collected := 0
	for e, tm := range s.tombs {
		for ad, rd := range tm {
			if !admit(rd) {
				continue
			}
			delete(tm, ad)
			if set := s.adds[e]; set != nil {
				delete(set, ad)
				if len(set) == 0 {
					delete(s.adds, e)
				}
			}
			collected++
		}
		if len(tm) == 0 {
			delete(s.tombs, e)
		}
	}
	return collected
}

// DropTombstonesUnsafe deletes ALL tombstones without any causal-stability
// check. It exists ONLY to demonstrate, in tests, the resurrection anomaly
// caused by premature tombstone collection. Never use it in real code.
func (s *ORSet) DropTombstonesUnsafe() int {
	collected := 0
	for e, tm := range s.tombs {
		for ad := range tm {
			delete(tm, ad)
			if set := s.adds[e]; set != nil {
				delete(set, ad)
				if len(set) == 0 {
					delete(s.adds, e)
				}
			}
			collected++
		}
		if len(tm) == 0 {
			delete(s.tombs, e)
		}
	}
	return collected
}

// TombCount returns the number of retained tombstones (for tests/metrics).
func (s *ORSet) TombCount() int {
	n := 0
	for _, tm := range s.tombs {
		n += len(tm)
	}
	return n
}

// AddDotCount returns the number of retained add-dots (for tests/metrics).
func (s *ORSet) AddDotCount() int {
	n := 0
	for _, set := range s.adds {
		n += len(set)
	}
	return n
}

// LiveDots returns the live (non-tombstoned) add-dots of e.
func (s *ORSet) LiveDots(e string) []Dot {
	var out []Dot
	for d := range s.adds[e] {
		if _, gone := s.tombs[e][d]; !gone {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

func dotSetMapEqual(a, b map[string]map[Dot]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for e, dots := range a {
		od, ok := b[e]
		if !ok || len(od) != len(dots) {
			return false
		}
		for d := range dots {
			if _, ok := od[d]; !ok {
				return false
			}
		}
	}
	return true
}

func tombMapEqual(a, b map[string]map[Dot]Dot) bool {
	if len(a) != len(b) {
		return false
	}
	for e, tm := range a {
		om, ok := b[e]
		if !ok || len(om) != len(tm) {
			return false
		}
		for ad, rd := range tm {
			if ord, ok := om[ad]; !ok || ord != rd {
				return false
			}
		}
	}
	return true
}

func vvEqual(a, b map[string]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for r, n := range a {
		if b[r] != n {
			return false
		}
	}
	return true
}
