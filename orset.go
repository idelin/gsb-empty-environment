// Package orset implements an offline-capable replicated set using
// observed-remove semantics (OR-Set with tombstones).
//
// Every Add assigns a globally unique tag (a Dot) to the element. Remove
// only tombstones the tags the removing replica has already observed, so a
// concurrent re-add on another replica is never swallowed by a remove.
//
// Tombstones are history that must be kept until every known replica has
// acknowledged them; GC only drops a tombstone once all known replicas
// (tracked through a matrix clock of version vectors) have seen the remove.
package orset

import "sort"

// Dot is a globally unique tag for a mutation: (replica ID, sequence).
type Dot struct {
	Replica string
	Seq     uint64
}

// tombstone records that the add tagged Add was removed by the mutation
// tagged RemovedBy.
type tombstone struct {
	Add       Dot
	RemovedBy Dot
}

// Replica is one replica of the observed-remove set. It is not safe for
// concurrent use from multiple goroutines; synchronize externally.
type Replica struct {
	id  string
	seq uint64

	// adds maps element -> set of live add tags.
	adds map[string]map[Dot]struct{}
	// removes maps element -> set of tombstones for observed-removed tags.
	removes map[string]map[tombstone]struct{}

	// vv is this replica's version vector over all mutations it has seen.
	vv map[string]uint64
	// known is the cluster membership used for GC safety. Tombstones are
	// retained until every known replica has acknowledged them, so an
	// offline replica blocks compaction instead of losing the delete record.
	known map[string]struct{}
	// acks is a matrix clock: acks[q] is a lower bound on the version
	// vector of replica q, learned by syncing with q (directly or via
	// gossip through other replicas).
	acks map[string]map[string]uint64
}

// New creates a replica with the given ID. cluster should list the IDs of
// all replicas in the deployment (including id); tombstones are only
// garbage-collected once every replica in this set has acknowledged them.
func New(id string, cluster ...string) *Replica {
	r := &Replica{
		id:      id,
		adds:    make(map[string]map[Dot]struct{}),
		removes: make(map[string]map[tombstone]struct{}),
		vv:      make(map[string]uint64),
		known:   make(map[string]struct{}),
		acks:    make(map[string]map[string]uint64),
	}
	r.known[id] = struct{}{}
	for _, peer := range cluster {
		r.known[peer] = struct{}{}
	}
	return r
}

// ID returns the replica's identifier.
func (r *Replica) ID() string { return r.id }

// nextDot allocates a fresh unique tag for a local mutation.
func (r *Replica) nextDot() Dot {
	r.seq++
	r.vv[r.id] = r.seq
	return Dot{Replica: r.id, Seq: r.seq}
}

// Add inserts elem and returns the unique tag assigned to this addition.
func (r *Replica) Add(elem string) Dot {
	d := r.nextDot()
	set, ok := r.adds[elem]
	if !ok {
		set = make(map[Dot]struct{})
		r.adds[elem] = set
	}
	set[d] = struct{}{}
	return d
}

// Remove tombstones every tag of elem this replica has observed. Tags
// created concurrently on other replicas are unaffected. It reports
// whether anything was removed.
func (r *Replica) Remove(elem string) bool {
	live := r.adds[elem]
	if len(live) == 0 {
		return false
	}
	by := r.nextDot()
	set, ok := r.removes[elem]
	if !ok {
		set = make(map[tombstone]struct{})
		r.removes[elem] = set
	}
	for tag := range live {
		set[tombstone{Add: tag, RemovedBy: by}] = struct{}{}
	}
	delete(r.adds, elem)
	return true
}

// Contains reports whether elem has at least one live tag.
func (r *Replica) Contains(elem string) bool {
	return len(r.adds[elem]) > 0
}

// Elements returns the live elements in sorted order.
func (r *Replica) Elements() []string {
	var out []string
	for elem, tags := range r.adds {
		if len(tags) > 0 {
			out = append(out, elem)
		}
	}
	sort.Strings(out)
	return out
}

// TombstoneCount returns the number of retained delete records.
func (r *Replica) TombstoneCount() int {
	n := 0
	for _, set := range r.removes {
		n += len(set)
	}
	return n
}

// LiveTags returns the live add tags of elem in sorted order. It is
// intended for inspection and tests.
func (r *Replica) LiveTags(elem string) []Dot {
	var out []Dot
	for tag := range r.adds[elem] {
		out = append(out, tag)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Replica != out[j].Replica {
			return out[i].Replica < out[j].Replica
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// Merge folds the state of other into r. It is commutative, associative
// and idempotent. It also absorbs other's acknowledged version vectors
// (matrix-clock gossip), so compaction safety propagates through
// transitive syncs.
func (r *Replica) Merge(other *Replica) {
	for elem, set := range other.removes {
		dst, ok := r.removes[elem]
		if !ok {
			dst = make(map[tombstone]struct{})
			r.removes[elem] = dst
		}
		for t := range set {
			dst[t] = struct{}{}
		}
	}
	for elem, set := range other.adds {
		for tag := range set {
			if r.isTombstoned(elem, tag) {
				continue
			}
			dst, ok := r.adds[elem]
			if !ok {
				dst = make(map[Dot]struct{})
				r.adds[elem] = dst
			}
			dst[tag] = struct{}{}
		}
	}
	// Drop local tags that other's tombstones cover.
	for elem, set := range r.adds {
		for tag := range set {
			if r.isTombstoned(elem, tag) {
				delete(set, tag)
			}
		}
		if len(set) == 0 {
			delete(r.adds, elem)
		}
	}
	mergeVV(r.vv, other.vv)
	// Merging directly with other proves other had seen everything in its
	// own version vector: record it as an acknowledgement from other.
	peer, ok := r.acks[other.id]
	if !ok {
		peer = make(map[string]uint64)
		r.acks[other.id] = peer
	}
	mergeVV(peer, other.vv)
	for q, vec := range other.acks {
		dst, ok := r.acks[q]
		if !ok {
			dst = make(map[string]uint64)
			r.acks[q] = dst
		}
		mergeVV(dst, vec)
	}
	for q := range other.known {
		r.known[q] = struct{}{}
	}
}

// Sync performs a bidirectional anti-entropy exchange between two
// replicas: afterwards both hold the union of their states and
// acknowledgements.
func Sync(a, b *Replica) {
	a.Merge(b)
	b.Merge(a)
}

func (r *Replica) isTombstoned(elem string, tag Dot) bool {
	for t := range r.removes[elem] {
		if t.Add == tag {
			return true
		}
	}
	return false
}

func mergeVV(dst, src map[string]uint64) {
	for k, v := range src {
		if v > dst[k] {
			dst[k] = v
		}
	}
}

// GC drops every tombstone that all known replicas have acknowledged and
// returns how many were dropped. A tombstone is safe to drop only when
// every known replica's version vector covers the remove mutation; until
// then an offline replica could still re-present the tag as live, so the
// delete record must be retained.
func (r *Replica) GC() int {
	// A replica always acknowledges its own observations.
	self, ok := r.acks[r.id]
	if !ok {
		self = make(map[string]uint64)
		r.acks[r.id] = self
	}
	mergeVV(self, r.vv)

	dropped := 0
	for elem, set := range r.removes {
		for t := range set {
			if r.safeToDrop(t) {
				delete(set, t)
				dropped++
			}
		}
		if len(set) == 0 {
			delete(r.removes, elem)
		}
	}
	return dropped
}

func (r *Replica) safeToDrop(t tombstone) bool {
	for q := range r.known {
		if r.acks[q][t.RemovedBy.Replica] < t.RemovedBy.Seq {
			return false
		}
	}
	return true
}
