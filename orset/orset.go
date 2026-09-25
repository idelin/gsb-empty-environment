// Package orset implements an Observed-Remove Set (OR-Set) CRDT.
//
// Design:
//   - Every mutation (add or remove) issued by a replica gets a unique Tag
//     (replica id, per-replica sequence number).
//   - Add(e) attaches a fresh unique tag to e.
//   - Remove(e) tombstones exactly the tags currently observed for e
//     ("observed-remove"): concurrent, not-yet-observed adds survive.
//   - Merge is a join on a semilattice (set union / max), hence
//     commutative, associative and idempotent.
//   - Tombstones are garbage-collected only after causal confirmation:
//     every replica in the roster is known (via version vectors) to have
//     applied the remove, so no live copy of the add tag can reappear.
package orset

import (
	"fmt"
	"reflect"
	"sort"
)

// Tag uniquely identifies a mutation issued by a replica.
type Tag struct {
	Replica string
	Seq     uint64
}

func (t Tag) String() string { return fmt.Sprintf("%s#%d", t.Replica, t.Seq) }

// State is the exported, mergeable state of one replica.
type State struct {
	// Adds maps an element to its live (not yet removed) add tags.
	Adds map[string]map[Tag]bool
	// Tombs maps a removed add tag to the tag of the remove mutation
	// that killed it (the tombstone set).
	Tombs map[Tag]Tag
	// VV is the version vector: VV[r] is the highest sequence number of
	// r's mutations reflected in this state.
	VV map[string]uint64
	// Known maps a replica id to the newest version vector this replica
	// has directly observed for it (including itself). It is the basis
	// for causal confirmation during garbage collection.
	Known map[string]map[string]uint64
}

func newState() State {
	return State{
		Adds:  make(map[string]map[Tag]bool),
		Tombs: make(map[Tag]Tag),
		VV:    make(map[string]uint64),
		Known: make(map[string]map[string]uint64),
	}
}

// Set is a single OR-Set replica.
type Set struct {
	id    string
	peers []string // full roster, always includes id
	seq   uint64
	st    State
}

// New creates a replica. peers is the full replica roster (id is added
// automatically if missing); the roster is used for GC causal confirmation.
func New(id string, peers ...string) *Set {
	s := &Set{id: id, st: newState()}
	found := false
	for _, p := range peers {
		if p == id {
			found = true
		}
	}
	s.peers = append([]string(nil), peers...)
	if !found {
		s.peers = append(s.peers, id)
	}
	s.st.Known[id] = make(map[string]uint64)
	return s
}

// ID returns the replica id.
func (s *Set) ID() string { return s.id }

// bump issues the next unique mutation tag and records it in the VV.
func (s *Set) bump() Tag {
	s.seq++
	s.st.VV[s.id] = s.seq
	s.st.Known[s.id] = copyVV(s.st.VV)
	return Tag{Replica: s.id, Seq: s.seq}
}

// Add inserts elem with a fresh unique tag and returns the tag.
func (s *Set) Add(elem string) Tag {
	tag := s.bump()
	if s.st.Adds[elem] == nil {
		s.st.Adds[elem] = make(map[Tag]bool)
	}
	s.st.Adds[elem][tag] = true
	return tag
}

// Remove tombstones every tag currently observed for elem and returns the
// removed tags. Removing an element with no observed live tags (never added,
// already removed, or duplicate remove) is a no-op.
func (s *Set) Remove(elem string) []Tag {
	tags := s.st.Adds[elem]
	if len(tags) == 0 {
		return nil
	}
	rm := s.bump()
	removed := make([]Tag, 0, len(tags))
	for t := range tags {
		joinTomb(&s.st, t, rm)
		removed = append(removed, t)
	}
	delete(s.st.Adds, elem)
	sortTags(removed)
	return removed
}

// Contains reports whether elem has at least one live add tag.
func (s *Set) Contains(elem string) bool { return len(s.st.Adds[elem]) > 0 }

// Elements returns the live elements in deterministic (sorted) order.
func (s *Set) Elements() []string {
	out := make([]string, 0, len(s.st.Adds))
	for e, tags := range s.st.Adds {
		if len(tags) > 0 {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// Tombstones returns the number of tombstones currently retained.
func (s *Set) Tombstones() int { return len(s.st.Tombs) }

// Snapshot returns a deep copy of the mergeable state (a "message").
func (s *Set) Snapshot() State { return cloneState(s.st) }

// Merge joins another replica's state into this one (see Join) and then
// refreshes this replica's self-knowledge entry.
func (s *Set) Merge(o State) {
	joinInto(&s.st, o)
	s.st.Known[s.id] = copyVV(s.st.VV)
}

// Join returns the least upper bound of two states. It is commutative,
// associative and idempotent: tombstones and adds are set joins (adds
// filtered by the tombstone join), version vectors are element-wise max.
// The states form a join-semilattice, which is what makes replicas
// converge (strong eventual consistency) under any message order.
func Join(x, y State) State {
	out := cloneState(x)
	joinInto(&out, y)
	return out
}

func joinInto(st *State, o State) {
	for at, rm := range o.Tombs {
		joinTomb(st, at, rm)
	}
	for e, tags := range o.Adds {
		for t := range tags {
			if _, dead := st.Tombs[t]; dead {
				continue
			}
			if st.Adds[e] == nil {
				st.Adds[e] = make(map[Tag]bool)
			}
			st.Adds[e][t] = true
		}
	}
	// Local adds killed by the newly joined tombstones.
	for e, tags := range st.Adds {
		for t := range tags {
			if _, dead := st.Tombs[t]; dead {
				delete(tags, t)
			}
		}
		if len(tags) == 0 {
			delete(st.Adds, e)
		}
	}
	maxInto(st.VV, o.VV)
	for r, vv := range o.Known {
		cur := st.Known[r]
		if cur == nil {
			cur = make(map[string]uint64)
			st.Known[r] = cur
		}
		maxInto(cur, vv)
	}
}

// joinTomb joins a tombstone into the set. Concurrent removes of the same
// element tombstone the same add tag with different remove stamps; keeping
// the minimum stamp makes the join commutative, associative and idempotent.
// GC stays sound (confirmation of the minimum implies the add tag is dead
// everywhere) and eventually live (all removes propagate, so the minimum is
// eventually confirmed).
func joinTomb(st *State, addTag, rm Tag) {
	if cur, ok := st.Tombs[addTag]; !ok || lessTag(rm, cur) {
		st.Tombs[addTag] = rm
	}
}

func lessTag(a, b Tag) bool {
	if a.Replica != b.Replica {
		return a.Replica < b.Replica
	}
	return a.Seq < b.Seq
}

// GC collects tombstones whose remove mutation is causally confirmed:
// for every replica p in the roster, the newest version vector observed
// from p covers the remove's stamp, i.e. every replica has applied the
// remove and therefore no replica can still hold the add tag. Returns the
// number of tombstones collected.
func (s *Set) GC() int {
	collected := 0
	for at, rm := range s.st.Tombs {
		if s.confirmed(rm) {
			delete(s.st.Tombs, at)
			collected++
		}
	}
	return collected
}

// confirmed reports whether every replica in the roster is known to have
// applied the mutation stamped rm.
func (s *Set) confirmed(rm Tag) bool {
	for _, p := range s.peers {
		vv, ok := s.st.Known[p]
		if !ok || vv[rm.Replica] < rm.Seq {
			return false
		}
	}
	return true
}

// UnsafeDropTombstones deletes ALL tombstones immediately, without any
// causal confirmation. It is unsafe: a replica that has not yet observed
// the remove can later re-merge the add tag and resurrect the element.
// It exists only to demonstrate that hazard in tests. Do not use.
func (s *Set) UnsafeDropTombstones() { s.st.Tombs = make(map[Tag]Tag) }

// Equal reports whether two states are identical.
func Equal(a, b State) bool { return reflect.DeepEqual(a, b) }

func copyVV(vv map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(vv))
	for k, v := range vv {
		out[k] = v
	}
	return out
}

func maxInto(dst, src map[string]uint64) {
	for k, v := range src {
		if v > dst[k] {
			dst[k] = v
		}
	}
}

func cloneState(st State) State {
	out := newState()
	for e, tags := range st.Adds {
		nt := make(map[Tag]bool, len(tags))
		for t := range tags {
			nt[t] = true
		}
		out.Adds[e] = nt
	}
	for at, rm := range st.Tombs {
		out.Tombs[at] = rm
	}
	out.VV = copyVV(st.VV)
	for r, vv := range st.Known {
		out.Known[r] = copyVV(vv)
	}
	return out
}

func sortTags(tags []Tag) {
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Replica != tags[j].Replica {
			return tags[i].Replica < tags[j].Replica
		}
		return tags[i].Seq < tags[j].Seq
	})
}
