// Package ghtable implements a gradually (incrementally) resizing hash table.
//
// The table keeps at most two hash levels: a main level (ht0) and, while a
// resize is in flight, a new level (ht1). Every operation migrates at most a
// bounded number of buckets from the old level to the new one, so no single
// call ever pays the cost of rehashing the whole table. Reads, deletes and
// snapshot iteration remain available throughout the migration.
//
// Design invariants:
//
//   - A key may be present in both levels while migrating, but the entry in
//     the new level always wins, so exactly one value is ever visible.
//   - Deletes that happen during a migration write a tombstone into the new
//     level. Migration skips any old-level entry whose key exists in the new
//     level (live or tombstone), so a deleted value can never be resurrected
//     by a late-migrating bucket.
//   - Entries and bucket chains are immutable once shared: every mutation
//     clones the affected chain. A Snapshot therefore pins a stable view of
//     the mapping (tagged with the mapping version) that cannot miss or
//     duplicate keys no matter how migration and writes proceed afterwards.
package ghtable

import (
	"hash/maphash"
	"math/bits"
)

const (
	// minBuckets is the smallest bucket count a level ever has.
	minBuckets = 8
	// defaultBudget is the default per-operation bucket migration budget.
	defaultBudget = 1
	// tombstoneCleanMin is the minimum tombstone count that can trigger a
	// cleanup rehash of a tombstone-dominated table.
	tombstoneCleanMin = 8
)

// Hasher hashes a key to a 64-bit value. Supplying a custom Hasher (for
// example a constant or modular one) gives tests full control over bucket
// placement and collisions.
type Hasher[K comparable] func(K) uint64

// Config tunes a Table. The zero value is valid.
type Config[K comparable] struct {
	// Hasher hashes keys. Nil selects a randomly seeded default hasher.
	Hasher Hasher[K]
	// InitialBuckets is the starting bucket count, rounded up to a power of
	// two with a minimum of 8. Zero selects the minimum.
	InitialBuckets int
	// MigrateBudget bounds how many buckets a single operation may migrate.
	// Values below 1 are treated as 1.
	MigrateBudget int
}

// Metrics records cumulative and worst-case migration work.
type Metrics struct {
	MigrationSteps  uint64 // migrateStep calls that did work
	BucketsMigrated uint64 // total buckets migrated across all resizes
	EntriesExamined uint64 // total old-level entries inspected
	EntriesMoved    uint64 // total entries carried into a new level
	MaxStepBuckets  int    // worst single-step buckets visited
	MaxStepEntries  int    // worst single-step entries examined
}

// Stats is a point-in-time report of table state and migration progress.
type Stats struct {
	Version         uint64  // mapping version; bumped by every visible write
	Count           int     // visible live keys
	Tombstones      int     // tombstone entries across both levels
	Migrating       bool    // whether a resize is in flight
	MainBuckets     int     // bucket count of the main (old) level
	NewBuckets      int     // bucket count of the new level, 0 if not migrating
	BucketsMigrated int     // buckets migrated so far in the current resize
	Progress        float64 // resize progress in [0,1]; 1 when not migrating
	Metrics         Metrics // cumulative and worst single-step work
}

// entry is immutable once stored in a bucket chain.
type entry[K comparable, V any] struct {
	key       K
	value     V
	tombstone bool
}

// level is one hash level. Its buckets slice is owned by exactly one
// structure (the table or a snapshot); chains are never mutated in place.
type level[K comparable, V any] struct {
	buckets [][]*entry[K, V]
	live    int // live (non-tombstone) entries
	tombs   int // tombstone entries
}

// Table is a gradually resizing hash table. It is not safe for concurrent
// use; external synchronization is required.
type Table[K comparable, V any] struct {
	hash   Hasher[K]
	budget int

	ht0 *level[K, V] // main level; the old level while migrating
	ht1 *level[K, V] // new level while migrating; nil otherwise

	migrateIdx int    // next old-level bucket to migrate
	count      int    // visible live keys
	version    uint64 // mapping version
	metrics    Metrics
}

// New returns an empty table configured by cfg (at most one may be given).
func New[K comparable, V any](cfg ...Config[K]) *Table[K, V] {
	var c Config[K]
	if len(cfg) > 0 {
		c = cfg[0]
	}
	n := pow2ceil(c.InitialBuckets)
	t := &Table[K, V]{
		hash:   c.Hasher,
		budget: c.MigrateBudget,
		ht0:    &level[K, V]{buckets: make([][]*entry[K, V], n)},
	}
	if t.hash == nil {
		seed := maphash.MakeSeed()
		t.hash = func(k K) uint64 { return maphash.Comparable(seed, k) }
	}
	if t.budget < 1 {
		t.budget = defaultBudget
	}
	return t
}

// Len returns the number of visible live keys.
func (t *Table[K, V]) Len() int { return t.count }

// Version returns the current mapping version. It is bumped by every Set or
// Delete that changes the visible mapping; migration steps do not change it.
func (t *Table[K, V]) Version() uint64 { return t.version }

// Migrating reports whether a resize is currently in flight.
func (t *Table[K, V]) Migrating() bool { return t.ht1 != nil }

// Stats returns a snapshot of table state, migration progress and the
// recorded worst single-step work.
func (t *Table[K, V]) Stats() Stats {
	s := Stats{
		Version:     t.version,
		Count:       t.count,
		Migrating:   t.ht1 != nil,
		MainBuckets: len(t.ht0.buckets),
		Progress:    1,
		Metrics:     t.metrics,
		Tombstones:  t.ht0.tombs,
	}
	if t.ht1 != nil {
		s.NewBuckets = len(t.ht1.buckets)
		s.BucketsMigrated = t.migrateIdx
		s.Progress = float64(t.migrateIdx) / float64(len(t.ht0.buckets))
		s.Tombstones += t.ht1.tombs
	}
	return s
}

// Get returns the value for key, or ok == false if the key is absent. Like
// every operation, it first migrates up to the configured bucket budget.
func (t *Table[K, V]) Get(key K) (value V, ok bool) {
	t.migrateStep()
	return t.get(key)
}

// Set associates value with key. During a migration the write always lands
// in the new level, shadowing any stale old-level copy, so exactly one value
// is visible for the key at all times.
func (t *Table[K, V]) Set(key K, value V) {
	t.migrateStep()
	_, existed := t.get(key)
	target := t.ht0
	if t.ht1 != nil {
		target = t.ht1
	}
	t.putLive(target, key, value)
	if !existed {
		t.count++
	}
	t.version++
	if t.ht1 == nil {
		t.maybeGrow()
	}
}

// Delete removes key, reporting whether it was present. During a migration
// the delete is recorded as a tombstone in the new level; when the old
// bucket holding the stale value later migrates, the entry is dropped
// instead of being resurrected.
func (t *Table[K, V]) Delete(key K) bool {
	t.migrateStep()
	if _, ok := t.get(key); !ok {
		return false
	}
	if t.ht1 != nil {
		t.putTombstone(t.ht1, key)
	} else {
		removeKey(t.ht0, t.hash, key)
		t.maybeShrinkOrClean()
	}
	t.count--
	t.version++
	return true
}

// MigrateAll forces the current migration (if any) to completion, still
// honoring the per-step bucket budget for work accounting.
func (t *Table[K, V]) MigrateAll() {
	for t.ht1 != nil {
		t.migrateStep()
	}
}

// get is the read path without the migration step.
func (t *Table[K, V]) get(key K) (V, bool) {
	if t.ht1 != nil {
		if e := findEntry(t.ht1, t.hash, key); e != nil {
			if e.tombstone {
				var zero V
				return zero, false
			}
			return e.value, true
		}
	}
	if e := findEntry(t.ht0, t.hash, key); e != nil {
		if e.tombstone {
			var zero V
			return zero, false
		}
		return e.value, true
	}
	var zero V
	return zero, false
}

// migrateStep moves at most budget buckets from ht0 to ht1. Old entries
// whose key already exists in ht1 (live or tombstone) are dropped, as are
// tombstones themselves, so migration also compacts.
func (t *Table[K, V]) migrateStep() {
	if t.ht1 == nil {
		return
	}
	visited, examined, moved := 0, 0, 0
	for t.migrateIdx < len(t.ht0.buckets) && visited < t.budget {
		chain := t.ht0.buckets[t.migrateIdx]
		t.ht0.buckets[t.migrateIdx] = nil
		for _, e := range chain {
			examined++
			if e.tombstone {
				t.ht0.tombs--
				continue
			}
			t.ht0.live--
			if findEntry(t.ht1, t.hash, e.key) != nil {
				continue // newer write or tombstone in ht1 wins
			}
			t.putLive(t.ht1, e.key, e.value)
			moved++
		}
		visited++
		t.migrateIdx++
	}
	m := &t.metrics
	m.MigrationSteps++
	m.BucketsMigrated += uint64(visited)
	m.EntriesExamined += uint64(examined)
	m.EntriesMoved += uint64(moved)
	if visited > m.MaxStepBuckets {
		m.MaxStepBuckets = visited
	}
	if examined > m.MaxStepEntries {
		m.MaxStepEntries = examined
	}
	if t.migrateIdx >= len(t.ht0.buckets) {
		t.ht0 = t.ht1
		t.ht1 = nil
		t.migrateIdx = 0
		// A finished resize may leave the table sparse or dominated by
		// tombstones; chain a follow-up resize so the table converges.
		t.maybeShrinkOrClean()
	}
}

// maybeGrow starts a resize when the main level is full.
func (t *Table[K, V]) maybeGrow() {
	if t.ht0.live+t.ht0.tombs >= len(t.ht0.buckets) {
		t.startMigration(pow2ceil(2 * (t.count + 1)))
	}
}

// maybeShrinkOrClean starts a downsize when the table is sparse, or a
// same-size cleanup rehash when tombstones dominate.
func (t *Table[K, V]) maybeShrinkOrClean() {
	n := len(t.ht0.buckets)
	if n > minBuckets && t.count*4 <= n {
		if target := pow2ceil(2 * (t.count + 1)); target < n {
			t.startMigration(target)
			return
		}
	}
	if t.ht0.tombs >= tombstoneCleanMin && t.ht0.tombs > t.ht0.live {
		t.startMigration(pow2ceil(2 * (t.count + 1)))
	}
}

// startMigration opens ht1 with n buckets. It is a no-op while a migration
// is already in flight.
func (t *Table[K, V]) startMigration(n int) {
	if t.ht1 != nil {
		return
	}
	if n < minBuckets {
		n = minBuckets
	}
	t.ht1 = &level[K, V]{buckets: make([][]*entry[K, V], n)}
	t.migrateIdx = 0
}

// putLive inserts or replaces a live entry for key in l, cloning the chain.
func (t *Table[K, V]) putLive(l *level[K, V], key K, value V) {
	bi := bucketIndex(l, t.hash, key)
	chain := l.buckets[bi]
	for i, e := range chain {
		if e.key == key {
			next := make([]*entry[K, V], len(chain))
			copy(next, chain)
			next[i] = &entry[K, V]{key: key, value: value}
			l.buckets[bi] = next
			if e.tombstone {
				l.tombs--
				l.live++
			}
			return
		}
	}
	l.buckets[bi] = append(cloneChain(chain), &entry[K, V]{key: key, value: value})
	l.live++
}

// putTombstone records a tombstone for key in l, cloning the chain.
func (t *Table[K, V]) putTombstone(l *level[K, V], key K) {
	bi := bucketIndex(l, t.hash, key)
	chain := l.buckets[bi]
	for i, e := range chain {
		if e.key == key {
			if e.tombstone {
				return
			}
			next := make([]*entry[K, V], len(chain))
			copy(next, chain)
			next[i] = &entry[K, V]{key: key, tombstone: true}
			l.buckets[bi] = next
			l.live--
			l.tombs++
			return
		}
	}
	l.buckets[bi] = append(cloneChain(chain), &entry[K, V]{key: key, tombstone: true})
	l.tombs++
}

// removeKey deletes the entry for key from l if present, cloning the chain.
func removeKey[K comparable, V any](l *level[K, V], hash Hasher[K], key K) {
	bi := bucketIndex(l, hash, key)
	chain := l.buckets[bi]
	for i, e := range chain {
		if e.key == key {
			next := make([]*entry[K, V], 0, len(chain)-1)
			next = append(next, chain[:i]...)
			next = append(next, chain[i+1:]...)
			l.buckets[bi] = next
			if e.tombstone {
				l.tombs--
			} else {
				l.live--
			}
			return
		}
	}
}

// findEntry returns the entry for key in l, or nil.
func findEntry[K comparable, V any](l *level[K, V], hash Hasher[K], key K) *entry[K, V] {
	for _, e := range l.buckets[bucketIndex(l, hash, key)] {
		if e.key == key {
			return e
		}
	}
	return nil
}

func bucketIndex[K comparable, V any](l *level[K, V], hash Hasher[K], key K) int {
	return int(hash(key) & uint64(len(l.buckets)-1))
}

func cloneChain[K comparable, V any](chain []*entry[K, V]) []*entry[K, V] {
	next := make([]*entry[K, V], len(chain))
	copy(next, chain)
	return next
}

// pow2ceil rounds n up to a power of two, with a floor of minBuckets.
func pow2ceil(n int) int {
	if n <= minBuckets {
		return minBuckets
	}
	return 1 << bits.Len(uint(n-1))
}
