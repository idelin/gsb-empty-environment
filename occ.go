// Package occ implements a non-blocking, spinlock-based optimistic
// concurrency control (OCC) engine with the classic three-phase
// execution kernel: read, validation, and write.
//
// Read phase: the transaction buffers reads into a ReadSet (record +
// version) and writes into a WriteSet (key + value). Reads are
// lock-free via per-record sequence locks (seqlocks).
//
// Validation phase: at commit time the engine checks whether any
// record in the ReadSet was modified by another committed transaction
// (version changed since it was read).
//
// Write phase: if validation succeeds, the whole WriteSet is applied
// in one atomic batch while holding the affected records' spinlocks;
// otherwise the transaction aborts cleanly and nothing is applied.
package occ

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// spinlock is a tiny test-and-set lock used to serialize writers of a
// single record. Readers never take it.
type spinlock struct {
	flag int32
}

func (s *spinlock) Lock() {
	for !atomic.CompareAndSwapInt32(&s.flag, 0, 1) {
		runtime.Gosched()
	}
}

func (s *spinlock) Unlock() {
	atomic.StoreInt32(&s.flag, 0)
}

// record is one versioned key/value cell. seq is a seqlock counter:
// odd while a write is in progress, even (and stable) otherwise.
// The committed version of the record is seq/2. value is an atomically
// published pointer to an immutable string, so readers never observe a
// torn string header.
// record is padded to a full 64-byte cache line so that a writer
// bumping one record's seq never invalidates a neighbouring record's
// line that concurrent readers are spinning on.
type record struct {
	seq   uint64
	value unsafe.Pointer // *string
	lock  spinlock
	_     [44]byte
}

var emptyValue string

func newRecord() *record {
	r := &record{}
	atomic.StorePointer(&r.value, unsafe.Pointer(&emptyValue))
	return r
}

// read spins until it observes a stable (even) sequence and returns
// the value together with the sequence it was read at. Lock-free.
func (r *record) read() (value string, seq uint64) {
	for {
		s1 := atomic.LoadUint64(&r.seq)
		if s1&1 != 0 {
			runtime.Gosched()
			continue
		}
		v := *(*string)(atomic.LoadPointer(&r.value))
		s2 := atomic.LoadUint64(&r.seq)
		if s1 == s2 {
			return v, s1
		}
		runtime.Gosched()
	}
}

// write publishes a new value and bumps the version.
// The caller must hold r.lock.
func (r *record) write(v string) {
	atomic.AddUint64(&r.seq, 1) // odd: write in progress
	p := new(string)
	*p = v
	atomic.StorePointer(&r.value, unsafe.Pointer(p))
	atomic.AddUint64(&r.seq, 1) // even: new committed version
}

// version spins until the record is stable and returns its seq.
func (r *record) version() uint64 {
	for {
		s := atomic.LoadUint64(&r.seq)
		if s&1 == 0 {
			return s
		}
		runtime.Gosched()
	}
}

// Stats reports cumulative commit/abort counters of a Store.
type Stats struct {
	Commits uint64
	Aborts  uint64
}

// ConflictRate returns Aborts / (Commits + Aborts); 0 if no commits ran.
func (s Stats) ConflictRate() float64 {
	total := s.Commits + s.Aborts
	if total == 0 {
		return 0
	}
	return float64(s.Aborts) / float64(total)
}

// Store is the OCC key/value engine. Records live in an immutable
// snapshot map published via atomic.Value (copy-on-write): the read
// path is a single atomic load plus a map lookup — no locks, no
// allocation, no mutation of shared cache lines — so concurrent reads
// scale linearly. Only the first sight of a brand-new key takes the
// grow mutex and republishes the map.
type Store struct {
	snap atomic.Value // map[string]*record, immutable once published
	grow sync.Mutex

	commits uint64
	aborts  uint64
}

// NewStore creates an empty Store.
func NewStore() *Store {
	s := &Store{}
	s.snap.Store(make(map[string]*record))
	return s
}

func (s *Store) getRecord(key string) *record {
	m := s.snap.Load().(map[string]*record)
	if r, ok := m[key]; ok {
		return r
	}
	s.grow.Lock()
	m = s.snap.Load().(map[string]*record)
	if r, ok := m[key]; ok {
		s.grow.Unlock()
		return r
	}
	nm := make(map[string]*record, len(m)+1)
	for k, v := range m {
		nm[k] = v
	}
	r := newRecord()
	nm[key] = r
	s.snap.Store(nm)
	s.grow.Unlock()
	return r
}

// Put writes a key directly (outside any transaction); used for setup.
func (s *Store) Put(key, value string) {
	r := s.getRecord(key)
	r.lock.Lock()
	r.write(value)
	r.lock.Unlock()
}

// Get reads a key directly (outside any transaction); used for checks.
func (s *Store) Get(key string) string {
	v, _ := s.getRecord(key).read()
	return v
}

// Stats returns a snapshot of the commit/abort counters.
func (s *Store) Stats() Stats {
	return Stats{
		Commits: atomic.LoadUint64(&s.commits),
		Aborts:  atomic.LoadUint64(&s.aborts),
	}
}

// readEntry is one ReadSet element: the record, the version it was
// read at, and the value observed (for repeatable reads inside a txn).
type readEntry struct {
	rec   *record
	seq   uint64
	value string
}

// writeEntry is one WriteSet element.
type writeEntry struct {
	key   string
	rec   *record
	value string
}

// Txn is a single optimistic transaction: ReadSet + WriteSet.
// The inline buffers keep small transactions allocation-free.
type Txn struct {
	store *Store

	readBuf [4]readEntry
	reads   []readEntry

	writeBuf [2]writeEntry
	writes   []writeEntry
}

// Begin starts a new transaction on the store.
func (s *Store) Begin() Txn {
	t := Txn{store: s}
	t.reads = t.readBuf[:0]
	t.writes = t.writeBuf[:0]
	return t
}

// Read returns the transactional value of key: the buffered write if
// present, the value from the first read if the key is already in the
// ReadSet, otherwise a lock-free read from the store whose version is
// recorded into the ReadSet.
func (t *Txn) Read(key string) string {
	for i := range t.writes {
		if t.writes[i].key == key {
			return t.writes[i].value
		}
	}
	r := t.store.getRecord(key)
	for i := range t.reads {
		if t.reads[i].rec == r {
			return t.reads[i].value
		}
	}
	v, seq := r.read()
	t.reads = append(t.reads, readEntry{rec: r, seq: seq, value: v})
	return v
}

// Write buffers a value into the WriteSet; nothing touches the store
// until Commit's write phase.
func (t *Txn) Write(key, value string) {
	for i := range t.writes {
		if t.writes[i].key == key {
			t.writes[i].value = value
			return
		}
	}
	t.writes = append(t.writes, writeEntry{key: key, rec: t.store.getRecord(key), value: value})
}

// Commit runs the validation and write phases.
//
// Validation: every ReadSet record's current version must equal the
// version recorded during the read phase; a mismatch means another
// transaction committed a concurrent modification and we abort.
//
// Write: on success the whole WriteSet is applied as one batch while
// holding the involved records' spinlocks (taken in sorted key order
// to avoid deadlock).
//
// Returns true on commit, false on abort. On abort nothing is applied.
func (t *Txn) Commit() bool {
	if len(t.writes) > 1 {
		sort.Slice(t.writes, func(i, j int) bool { return t.writes[i].key < t.writes[j].key })
	}

	for i := range t.writes {
		t.writes[i].rec.lock.Lock()
	}

	ok := true
	for i := range t.reads {
		if t.reads[i].rec.version() != t.reads[i].seq {
			ok = false
			break
		}
	}

	if ok {
		for i := range t.writes {
			t.writes[i].rec.write(t.writes[i].value)
		}
	}

	for i := range t.writes {
		t.writes[i].rec.lock.Unlock()
	}

	if ok {
		atomic.AddUint64(&t.store.commits, 1)
	} else {
		atomic.AddUint64(&t.store.aborts, 1)
	}
	return ok
}

// Transact runs fn inside a fresh transaction, retrying with a new
// transaction on every abort until it commits. It returns the number
// of attempts (1 = committed first try).
func (s *Store) Transact(fn func(*Txn)) int {
	attempts := 0
	for {
		attempts++
		txn := s.Begin()
		fn(&txn)
		if txn.Commit() {
			return attempts
		}
	}
}
