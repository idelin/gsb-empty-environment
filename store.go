package dvv

import "sort"

// Value is one version of a key: the payload plus the DVV clock that
// records exactly which events it causally knows.
type Value struct {
	Data  string
	Clock Clock
}

// Store is a single replica of a leaderless key/value store using DVV
// for causality tracking and deduplication.
type Store struct {
	node string
	data map[string][]Value
}

// NewStore creates a replica identified by node.
func NewStore(node string) *Store {
	return &Store{node: node, data: map[string][]Value{}}
}

// Node returns the replica's node id.
func (s *Store) Node() string { return s.node }

// Get returns all live (non-superseded) versions of key.
func (s *Store) Get(key string) []Value {
	src := s.data[key]
	out := make([]Value, len(src))
	copy(out, src)
	return out
}

// Context returns the causal context of key: the join of the clocks of
// all live versions. Clients pass it back to Put so overwrites are
// recognized as causal successors and superseded versions are pruned.
func (s *Store) Context(key string) Clock {
	ctx := EmptyClock()
	for _, v := range s.data[key] {
		ctx = ctx.Merge(v.Clock)
	}
	return ctx
}

// reconcile reduces a set of versions to its minimal form:
//  1. versions dominated by another version are dropped — a causal
//     overwrite never leaves a ghost branch behind;
//  2. concurrent versions carrying identical data are duplicates of the
//     same logical write (retries, re-broadcasts) and are fused into a
//     single version whose clock is the union of their events;
//  3. only concurrent versions with different data — genuine conflicts —
//     remain as siblings.
//
// reconcile is associative, commutative and idempotent, so replicas
// exchanging versions in any order, any number of times, converge.
func reconcile(entries []Value) []Value {
	// 1. drop dominated versions.
	kept := entries[:0]
	for i, e := range entries {
		dominated := false
		for j, o := range entries {
			if i == j || !o.Clock.Descends(e.Clock) {
				continue
			}
			// Strictly dominated, or an exact duplicate that appears
			// later: drop it. The first of equal clocks survives.
			if !e.Clock.Descends(o.Clock) || j < i {
				dominated = true
				break
			}
		}
		if !dominated {
			kept = append(kept, e)
		}
	}
	// 2. fuse same-data duplicates; keep genuine conflicts (3).
	byData := map[string]Clock{}
	order := []string{}
	for _, e := range kept {
		if _, ok := byData[e.Data]; !ok {
			order = append(order, e.Data)
			byData[e.Data] = e.Clock
		} else {
			byData[e.Data] = byData[e.Data].Merge(e.Clock)
		}
	}
	sort.Strings(order)
	out := make([]Value, 0, len(order))
	for _, d := range order {
		out = append(out, Value{Data: d, Clock: byData[d]})
	}
	return out
}

// Put writes data under key with the given causal context (from Context
// or a previous Put, EmptyClock for a context-free write) and returns
// the clock of the stored version.
//
// The write is stamped with a fresh dot (s.node, k) where k is one past
// every counter this replica has seen from s.node for this key, making
// the event globally unique. Applying the same Put twice is a no-op:
// the second clock is already covered by the stored version.
func (s *Store) Put(key, data string, ctx Clock) Clock {
	entries := s.data[key]

	var maxSeen uint64
	for _, e := range entries {
		if m := e.Clock.maxCounter(s.node); m > maxSeen {
			maxSeen = m
		}
	}
	if m := ctx.maxCounter(s.node); m > maxSeen {
		maxSeen = m
	}
	clock := ctx.withDot(Dot{Node: s.node, Counter: maxSeen + 1})

	for _, e := range entries {
		if e.Clock.Descends(clock) {
			return e.Clock // duplicate delivery of an already-stored write
		}
	}
	s.data[key] = reconcile(append(entries, Value{Data: data, Clock: clock}))
	return clock
}

// Merge integrates one received version (e.g. from a broadcast) into
// the local state. It is idempotent: re-delivering a version changes
// nothing.
func (s *Store) Merge(key string, v Value) {
	s.data[key] = reconcile(append(s.data[key], v))
}

// Sync pulls every version of every key from other into s. Because
// Merge is commutative, associative and idempotent, replicas converge
// regardless of broadcast order, duplication or interleaving.
func (s *Store) Sync(other *Store) {
	for key, entries := range other.data {
		for _, v := range entries {
			s.Merge(key, v)
		}
	}
}

// Snapshot returns a flat, deterministically ordered view of all
// versions, useful for comparing replica states.
func (s *Store) Snapshot() []string {
	var out []string
	for key, entries := range s.data {
		for _, v := range entries {
			out = append(out, key+"="+v.Data+"@"+v.Clock.String())
		}
	}
	sort.Strings(out)
	return out
}

// Stats reports the metadata footprint: number of keys, live versions
// (siblings), and total clock entries across all versions.
func (s *Store) Stats() (keys, versions, clockEntries int) {
	keys = len(s.data)
	for _, entries := range s.data {
		versions += len(entries)
		for _, v := range entries {
			clockEntries += v.Clock.Size()
		}
	}
	return
}
