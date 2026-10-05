package rehash

// Snapshot is a stable, materialized view of a Table at a specific logical
// version. Because the entries are copied at creation time, iteration is
// immune to concurrent mutation and migration: it can neither miss entries
// that existed at the snapshot version nor yield duplicates.
//
// A key exists in exactly one physical table at any moment (the table's
// core invariant), so collecting the union of both tables cannot duplicate
// keys either.
type Snapshot[K comparable, V any] struct {
	entries []entry[K, V]
	version uint64
	pos     int
}

// Snapshot captures the current contents and logical version of t.
// Cost is O(n) once, at creation; iteration itself is allocation-free.
func (t *Table[K, V]) Snapshot() *Snapshot[K, V] {
	s := &Snapshot[K, V]{
		entries: make([]entry[K, V], 0, t.Len()),
		version: t.version,
	}
	s.entries = append(s.entries, flatten(t.cur)...)
	if t.old != nil {
		s.entries = append(s.entries, flatten(t.old)...)
	}
	return s
}

func flatten[K comparable, V any](l *level[K, V]) []entry[K, V] {
	var out []entry[K, V]
	for _, chain := range l.buckets {
		out = append(out, chain...)
	}
	return out
}

// Version returns the logical table version at capture time.
func (s *Snapshot[K, V]) Version() uint64 { return s.version }

// Len returns the number of entries in the snapshot.
func (s *Snapshot[K, V]) Len() int { return len(s.entries) }

// Next advances the iterator, returning the next key/value pair.
// ok is false when iteration is exhausted.
func (s *Snapshot[K, V]) Next() (key K, value V, ok bool) {
	if s.pos >= len(s.entries) {
		var zk K
		var zv V
		return zk, zv, false
	}
	e := s.entries[s.pos]
	s.pos++
	return e.key, e.value, true
}

// Reset rewinds the iterator to the beginning.
func (s *Snapshot[K, V]) Reset() { s.pos = 0 }

// Collect drains the remaining entries into a map. Mainly a test helper.
func (s *Snapshot[K, V]) Collect() map[K]V {
	m := make(map[K]V, len(s.entries))
	for _, e := range s.entries {
		m[e.key] = e.value
	}
	return m
}
