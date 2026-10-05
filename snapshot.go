package ghtable

// Snapshot is a stable, read-only view of the mapping at a specific version.
//
// Because entries and bucket chains are immutable once shared, a snapshot
// only copies the bucket slice headers; iteration over a snapshot can
// neither miss nor duplicate keys, regardless of subsequent writes,
// deletes, or migration progress on the originating table.
type Snapshot[K comparable, V any] struct {
	version uint64
	count   int
	hash    Hasher[K]
	hi      *level[K, V] // new level if the table was migrating, else main
	lo      *level[K, V] // old level if the table was migrating, else nil
}

// Snapshot captures the current mapping version and returns a stable view
// of it. It is safe to keep and iterate while the table continues to be
// mutated and migrated.
func (t *Table[K, V]) Snapshot() *Snapshot[K, V] {
	s := &Snapshot[K, V]{
		version: t.version,
		count:   t.count,
		hash:    t.hash,
	}
	if t.ht1 != nil {
		s.hi = &level[K, V]{buckets: cloneBuckets(t.ht1.buckets)}
		s.lo = &level[K, V]{buckets: cloneBuckets(t.ht0.buckets)}
	} else {
		s.hi = &level[K, V]{buckets: cloneBuckets(t.ht0.buckets)}
	}
	return s
}

// Version returns the mapping version this snapshot was taken at.
func (s *Snapshot[K, V]) Version() uint64 { return s.version }

// Len returns the number of visible keys in the snapshot.
func (s *Snapshot[K, V]) Len() int { return s.count }

// Iterator returns an iterator over the snapshot. Every visible key is
// yielded exactly once; keys shadowed or tombstoned in the newer level are
// suppressed when the older level is scanned.
func (s *Snapshot[K, V]) Iterator() *Iterator[K, V] {
	return &Iterator[K, V]{snap: s}
}

// Range calls fn for every key/value pair in the snapshot until fn returns
// false.
func (s *Snapshot[K, V]) Range(fn func(K, V) bool) {
	it := s.Iterator()
	for it.Next() {
		if !fn(it.Key(), it.Value()) {
			return
		}
	}
}

// Range iterates a stable snapshot of the table, calling fn for every
// key/value pair until fn returns false.
func (t *Table[K, V]) Range(fn func(K, V) bool) {
	t.Snapshot().Range(fn)
}

// Iterator walks a Snapshot. The zero value is not valid; obtain one from
// Snapshot.Iterator.
type Iterator[K comparable, V any] struct {
	snap   *Snapshot[K, V]
	lo     bool // currently scanning the old level
	bucket int
	idx    int
	key    K
	value  V
}

// Next advances the iterator, reporting whether a pair is available via
// Key and Value.
func (it *Iterator[K, V]) Next() bool {
	for {
		l := it.snap.hi
		if it.lo {
			l = it.snap.lo
		}
		if l == nil {
			return false
		}
		if it.bucket >= len(l.buckets) {
			if it.lo {
				return false
			}
			it.lo = true
			it.bucket, it.idx = 0, 0
			continue
		}
		chain := l.buckets[it.bucket]
		if it.idx >= len(chain) {
			it.bucket++
			it.idx = 0
			continue
		}
		e := chain[it.idx]
		it.idx++
		if e.tombstone {
			continue
		}
		if it.lo && findEntry(it.snap.hi, it.snap.hash, e.key) != nil {
			continue // shadowed or tombstoned in the newer level
		}
		it.key, it.value = e.key, e.value
		return true
	}
}

// Key returns the current key. Valid only after Next returned true.
func (it *Iterator[K, V]) Key() K { return it.key }

// Value returns the current value. Valid only after Next returned true.
func (it *Iterator[K, V]) Value() V { return it.value }

func cloneBuckets[K comparable, V any](b [][]*entry[K, V]) [][]*entry[K, V] {
	next := make([][]*entry[K, V], len(b))
	copy(next, b)
	return next
}
