// Package rehash implements a hash table with incremental (progressive)
// resizing. While a resize is in flight, keys migrate gradually from the
// old table to the new one: every operation moves at most a bounded number
// of buckets, so no single operation ever pays the full rehash cost.
//
// Invariants maintained at all times (including mid-migration):
//
//   - A key lives in exactly one table, so an update never produces two
//     visible values and a delete never leaves a stale copy behind that
//     could "resurrect" (tombstone safety).
//   - Get, Put, Delete and Snapshot all work during migration.
//   - Snapshot materializes a stable, versioned view: iteration over it
//     cannot miss or duplicate entries regardless of concurrent mutation
//     or migration.
//
// The table is NOT safe for concurrent use by multiple goroutines;
// guard it with a mutex if needed.
package rehash

const (
	minBuckets = 4
	// grow when count*growDen > buckets*growNum
	growNum = 3
	growDen = 4
	// shrink when count*shrinkDen < buckets
	shrinkDen = 8
)

// Option configures a Table.
type Option func(*config)

type config struct {
	initialBuckets int
	chunk          int
}

// WithCapacity pre-sizes the table for at least n entries.
func WithCapacity(n int) Option {
	return func(c *config) { c.initialBuckets = n }
}

// WithMigrateChunk sets the maximum number of old-table buckets migrated
// per operation. Default is 1. Larger values finish migration sooner at
// the cost of higher worst-case per-operation latency.
func WithMigrateChunk(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.chunk = n
		}
	}
}

type entry[K comparable, V any] struct {
	key   K
	value V
	hash  uint64
	ver   uint64
}

// level is one physical hash table (old or new during migration).
type level[K comparable, V any] struct {
	buckets [][]entry[K, V]
	count   int
}

func nextPow2(n int) int {
	p := minBuckets
	for p < n {
		p <<= 1
	}
	return p
}

func newLevel[K comparable, V any](nbuckets int) *level[K, V] {
	return &level[K, V]{buckets: make([][]entry[K, V], nextPow2(nbuckets))}
}

func (l *level[K, V]) slot(hash uint64) int { return int(hash) & (len(l.buckets) - 1) }

// find locates key, returning bucket index and element index (-1 if absent).
// probes is the number of chain elements inspected.
func (l *level[K, V]) find(key K, hash uint64) (bucket, idx, probes int) {
	bucket = l.slot(hash)
	chain := l.buckets[bucket]
	for i := range chain {
		probes++
		if chain[i].hash == hash && chain[i].key == key {
			return bucket, i, probes
		}
	}
	return bucket, -1, probes
}

// remove deletes key from l, swap-removing it from its chain.
func (l *level[K, V]) remove(key K, hash uint64) (bool, int) {
	b, i, probes := l.find(key, hash)
	if i < 0 {
		return false, probes
	}
	chain := l.buckets[b]
	chain[i] = chain[len(chain)-1]
	chain[len(chain)-1] = entry[K, V]{}
	l.buckets[b] = chain[:len(chain)-1]
	l.count--
	return true, probes
}

// Table is an incrementally resized hash table.
type Table[K comparable, V any] struct {
	hash HashFunc[K]

	cur *level[K, V] // primary table (the migration target while migrating)
	old *level[K, V] // previous table, non-nil only while migrating

	migrateNext int // next old-table bucket to migrate
	chunk       int // max buckets migrated per operation

	version uint64 // bumped on every logical mutation (Put / successful Delete)

	// metrics
	ops             uint64
	migratedEntries uint64
	lastWork        int
	worstWork       int
}

// New creates a Table using hash to distribute keys.
func New[K comparable, V any](hash HashFunc[K], opts ...Option) *Table[K, V] {
	cfg := config{initialBuckets: minBuckets, chunk: 1}
	for _, o := range opts {
		o(&cfg)
	}
	return &Table[K, V]{
		hash:  hash,
		cur:   newLevel[K, V](cfg.initialBuckets),
		chunk: cfg.chunk,
	}
}

// Len returns the number of live entries.
func (t *Table[K, V]) Len() int {
	n := t.cur.count
	if t.old != nil {
		n += t.old.count
	}
	return n
}

// Version returns the logical version: it increments on every mutation.
func (t *Table[K, V]) Version() uint64 { return t.version }

// Migrating reports whether an incremental resize is in flight.
func (t *Table[K, V]) Migrating() bool { return t.old != nil }

// Progress returns migration progress: buckets migrated so far and the
// total number of old-table buckets. total is 0 when not migrating.
func (t *Table[K, V]) Progress() (migrated, total int) {
	if t.old == nil {
		return 0, 0
	}
	return t.migrateNext, len(t.old.buckets)
}

func (t *Table[K, V]) beginOp() { t.ops++; t.lastWork = 0 }
func (t *Table[K, V]) addWork(n int) {
	t.lastWork += n
	if t.lastWork > t.worstWork {
		t.worstWork = t.lastWork
	}
}

// Get returns the value for key, migrating a bounded number of buckets first.
func (t *Table[K, V]) Get(key K) (V, bool) {
	t.beginOp()
	t.migrateStep()
	h := t.hash(key)
	if _, i, p := t.cur.find(key, h); i >= 0 {
		t.addWork(p)
		return t.cur.buckets[t.cur.slot(h)][i].value, true
	} else {
		t.addWork(p)
	}
	if t.old != nil {
		if b, i, p := t.old.find(key, h); i >= 0 {
			t.addWork(p)
			return t.old.buckets[b][i].value, true
		} else {
			t.addWork(p)
		}
	}
	var zero V
	return zero, false
}

// Put inserts or updates key. Exactly one visible value exists for key at
// all times: the update happens in whichever table currently holds the key,
// and new keys are always inserted into the newest table.
func (t *Table[K, V]) Put(key K, value V) {
	t.beginOp()
	t.migrateStep()
	h := t.hash(key)
	t.version++
	if t.old != nil {
		if b, i, p := t.old.find(key, h); i >= 0 {
			t.addWork(p)
			t.old.buckets[b][i].value = value
			t.old.buckets[b][i].ver = t.version
			return
		} else {
			t.addWork(p)
		}
	}
	if b, i, p := t.cur.find(key, h); i >= 0 {
		t.addWork(p)
		t.cur.buckets[b][i].value = value
		t.cur.buckets[b][i].ver = t.version
		return
	} else {
		t.addWork(p)
	}
	b := t.cur.slot(h)
	t.cur.buckets[b] = append(t.cur.buckets[b], entry[K, V]{key: key, value: value, hash: h, ver: t.version})
	t.cur.count++
	t.maybeGrow()
}

// Delete removes key from BOTH tables, so no stale copy can survive and be
// resurrected by a later lookup or by migration. Reports whether key existed.
func (t *Table[K, V]) Delete(key K) bool {
	t.beginOp()
	t.migrateStep()
	h := t.hash(key)
	found := false
	if t.old != nil {
		ok, p := t.old.remove(key, h)
		t.addWork(p)
		found = ok
	}
	ok, p := t.cur.remove(key, h)
	t.addWork(p)
	found = found || ok
	if found {
		t.version++
		t.maybeShrink()
	}
	return found
}

// maybeGrow starts an incremental grow when the load factor exceeds 3/4.
// A new resize never starts while one is already in flight.
func (t *Table[K, V]) maybeGrow() {
	if t.old != nil {
		return
	}
	if t.cur.count*growDen > len(t.cur.buckets)*growNum {
		t.startResize(len(t.cur.buckets) * 2)
	}
}

// maybeShrink starts an incremental shrink when the load factor drops below 1/8.
func (t *Table[K, V]) maybeShrink() {
	if t.old != nil || len(t.cur.buckets) <= minBuckets {
		return
	}
	if t.cur.count*shrinkDen < len(t.cur.buckets) {
		target := nextPow2(t.cur.count * 2)
		if target < len(t.cur.buckets) {
			t.startResize(target)
		}
	}
}

func (t *Table[K, V]) startResize(nbuckets int) {
	t.old = t.cur
	t.cur = newLevel[K, V](nbuckets)
	t.migrateNext = 0
}

// migrateStep moves at most t.chunk old-table buckets into the new table.
// Bucket visits and entry moves are counted as work units. Entries keep
// their version across the move; migration is not a logical mutation.
func (t *Table[K, V]) migrateStep() {
	if t.old == nil {
		return
	}
	for visited := 0; visited < t.chunk && t.migrateNext < len(t.old.buckets); visited++ {
		chain := t.old.buckets[t.migrateNext]
		t.old.buckets[t.migrateNext] = nil
		t.migrateNext++
		t.addWork(1) // one bucket visit
		for _, e := range chain {
			b := t.cur.slot(e.hash)
			t.cur.buckets[b] = append(t.cur.buckets[b], e)
			t.cur.count++
			t.old.count--
			t.migratedEntries++
			t.addWork(1) // one entry move
		}
	}
	if t.migrateNext >= len(t.old.buckets) {
		t.old = nil // migration complete
	}
}

// Stats is a point-in-time snapshot of table metrics.
type Stats struct {
	Version         uint64 // logical version (mutations so far)
	Size            int    // live entries
	Buckets         int    // buckets in the current (newest) table
	OldBuckets      int    // buckets in the old table, 0 when not migrating
	MigrateNext     int    // old-table buckets already migrated
	MigratedEntries uint64 // total entries moved by all migrations
	Ops             uint64 // total operations performed
	LastOpWork      int    // work units spent by the most recent operation
	WorstOpWork     int    // worst single-operation work units ever observed
}

// Stats returns current metrics, including migration progress and the
// worst single-step work observed (bucket visits + entry moves + probes).
func (t *Table[K, V]) Stats() Stats {
	s := Stats{
		Version:         t.version,
		Size:            t.Len(),
		Buckets:         len(t.cur.buckets),
		MigrateNext:     t.migrateNext,
		MigratedEntries: t.migratedEntries,
		Ops:             t.ops,
		LastOpWork:      t.lastWork,
		WorstOpWork:     t.worstWork,
	}
	if t.old != nil {
		s.OldBuckets = len(t.old.buckets)
	}
	return s
}
