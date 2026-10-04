// Package orset implements an offline-capable replicated set with
// Observed-Remove semantics (OR-Set CRDT).
//
// Every Add assigns a globally unique tag to the element. Remove only
// removes the tags the removing replica has observed, so a concurrent
// re-add on a partitioned replica always survives a later merge.
//
// Tombstones (deletion records) may be garbage-collected via Compact,
// but only once every known replica has acknowledged (observed) the
// removal. Membership must be configured up front (NewSet peers) for
// compaction to be safe; replicas learned through merges are also
// tracked automatically.
package orset

// Tag uniquely identifies one Add event.
type Tag struct {
	Replica string
	Seq     uint64
}

// tombstone records that Tag was removed by replica By at its event BySeq.
type tombstone struct {
	By    string
	BySeq uint64
}

// VersionVector maps replica ID to the highest event sequence observed.
type VersionVector map[string]uint64

// Set is a replicated observed-remove set.
type Set struct {
	id         string
	seq        uint64
	dots       map[string]map[Tag]struct{}
	tombstones map[Tag]tombstone
	clock      VersionVector
	peers      map[string]struct{}
	acks       map[string]VersionVector
}

// NewSet creates a replica. peers must list every other replica ID that
// may hold elements; compaction stays conservative until all of them
// acknowledge a removal.
func NewSet(id string, peers ...string) *Set {
	s := &Set{
		id:         id,
		dots:       make(map[string]map[Tag]struct{}),
		tombstones: make(map[Tag]tombstone),
		clock:      make(VersionVector),
		peers:      make(map[string]struct{}),
		acks:       make(map[string]VersionVector),
	}
	s.peers[id] = struct{}{}
	for _, p := range peers {
		s.peers[p] = struct{}{}
	}
	return s
}

// bump records a new local event (add or remove) in the version clock.
func (s *Set) bump() uint64 {
	s.seq++
	s.clock[s.id] = s.seq
	return s.seq
}

// Add inserts elem with a fresh unique tag and returns the tag.
func (s *Set) Add(elem string) Tag {
	tag := Tag{Replica: s.id, Seq: s.bump()}
	set, ok := s.dots[elem]
	if !ok {
		set = make(map[Tag]struct{})
		s.dots[elem] = set
	}
	set[tag] = struct{}{}
	return tag
}

// Remove deletes exactly the tags of elem observed by this replica and
// returns how many tags were removed.
func (s *Set) Remove(elem string) int {
	set, ok := s.dots[elem]
	if !ok || len(set) == 0 {
		return 0
	}
	event := s.bump()
	for tag := range set {
		s.tombstones[tag] = tombstone{By: s.id, BySeq: event}
	}
	delete(s.dots, elem)
	return len(set)
}

// Contains reports whether elem has any live tag.
func (s *Set) Contains(elem string) bool {
	return len(s.dots[elem]) > 0
}

// Len returns the number of live elements.
func (s *Set) Len() int {
	return len(s.dots)
}

// LiveTags returns the live tags of elem, sorted for determinism.
func (s *Set) LiveTags(elem string) []Tag {
	var tags []Tag
	for tag := range s.dots[elem] {
		tags = append(tags, tag)
	}
	for i := 1; i < len(tags); i++ {
		for j := i; j > 0 && less(tags[j], tags[j-1]); j-- {
			tags[j], tags[j-1] = tags[j-1], tags[j]
		}
	}
	return tags
}

func less(a, b Tag) bool {
	if a.Replica != b.Replica {
		return a.Replica < b.Replica
	}
	return a.Seq < b.Seq
}

// TombstoneCount returns the number of retained deletion records.
func (s *Set) TombstoneCount() int {
	return len(s.tombstones)
}

// Clock returns a copy of the replica's version clock.
func (s *Set) Clock() VersionVector {
	return join(nil, s.clock)
}

// Merge merges another replica's state into s. It is idempotent,
// commutative and associative.
func (s *Set) Merge(o *Set) {
	for tag, ts := range o.tombstones {
		if _, ok := s.tombstones[tag]; !ok {
			s.tombstones[tag] = ts
		}
	}
	mergeDots := func(from map[string]map[Tag]struct{}) {
		for elem, set := range from {
			for tag := range set {
				if _, removed := s.tombstones[tag]; removed {
					continue
				}
				if _, removed := o.tombstones[tag]; removed {
					continue
				}
				dst, ok := s.dots[elem]
				if !ok {
					dst = make(map[Tag]struct{})
					s.dots[elem] = dst
				}
				dst[tag] = struct{}{}
			}
		}
	}
	mergeDots(s.dots)
	mergeDots(o.dots)
	for elem, set := range s.dots {
		for tag := range set {
			if _, removed := s.tombstones[tag]; removed {
				delete(set, tag)
			}
		}
		if len(set) == 0 {
			delete(s.dots, elem)
		}
	}
	s.clock = join(s.clock, o.clock)
	s.learnPeer(o.id)
	for p := range o.peers {
		s.learnPeer(p)
	}
	for p, v := range o.acks {
		s.learnPeer(p)
		s.acks[p] = join(s.acks[p], v)
	}
	s.acks[o.id] = join(s.acks[o.id], o.clock)
	s.acks[s.id] = join(s.acks[s.id], s.clock)
}

func (s *Set) learnPeer(id string) {
	s.peers[id] = struct{}{}
}

// Compact drops deletion records that every known peer has acknowledged.
// A tombstone is acknowledged by peer p once p's observed clock covers
// the remove event, which guarantees p carries the tombstone itself and
// can never present the removed tag as live again. Returns the number of
// records dropped.
func (s *Set) Compact() int {
	dropped := 0
	for tag, ts := range s.tombstones {
		if s.acknowledged(ts) {
			delete(s.tombstones, tag)
			dropped++
		}
	}
	return dropped
}

func (s *Set) acknowledged(ts tombstone) bool {
	for p := range s.peers {
		if s.acks[p][ts.By] < ts.BySeq {
			return false
		}
	}
	return true
}

// DropAllTombstones discards every deletion record unconditionally.
// It is unsafe and exists only to demonstrate resurrection in tests and
// verification tooling; never call it in production.
func (s *Set) DropAllTombstones() {
	s.tombstones = make(map[Tag]tombstone)
}

// Clone returns a deep copy of the set.
func (s *Set) Clone() *Set {
	c := &Set{
		id:         s.id,
		seq:        s.seq,
		dots:       make(map[string]map[Tag]struct{}, len(s.dots)),
		tombstones: make(map[Tag]tombstone, len(s.tombstones)),
		clock:      make(VersionVector, len(s.clock)),
		peers:      make(map[string]struct{}, len(s.peers)),
		acks:       make(map[string]VersionVector, len(s.acks)),
	}
	for elem, set := range s.dots {
		cs := make(map[Tag]struct{}, len(set))
		for tag := range set {
			cs[tag] = struct{}{}
		}
		c.dots[elem] = cs
	}
	for tag, ts := range s.tombstones {
		c.tombstones[tag] = ts
	}
	for r, n := range s.clock {
		c.clock[r] = n
	}
	for p := range s.peers {
		c.peers[p] = struct{}{}
	}
	for p, v := range s.acks {
		c.acks[p] = join(nil, v)
	}
	return c
}

func join(a, b VersionVector) VersionVector {
	if a == nil {
		a = make(VersionVector)
	}
	for r, n := range b {
		if n > a[r] {
			a[r] = n
		}
	}
	return a
}
