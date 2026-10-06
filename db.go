// Package kvdb implements an in-memory integer-keyed store with
// serializable optimistic transactions.
//
// Each transaction records the half-open key-range predicates it scanned
// and the point keys it read, together with its write set. At commit time
// the transaction is validated against every transaction that committed
// after its snapshot: if any of them inserted, updated, or deleted a key
// that falls inside a recorded read predicate (or collides with the write
// set), the committing transaction is rejected. Writes outside all read
// predicates never conflict, and predicates are recorded even when a scan
// returned no rows, so phantoms are prevented.
package kvdb

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrTxnAborted is returned by Commit when a serialization conflict
	// is detected; the transaction applies none of its writes.
	ErrTxnAborted = errors.New("kvdb: transaction aborted: serialization conflict")
	// ErrTxnClosed is returned when operating on a finished transaction.
	ErrTxnClosed = errors.New("kvdb: transaction is already closed")
)

// Interval is a half-open key range [Start, End).
type Interval struct {
	Start, End int
}

// Contains reports whether key k lies in [Start, End).
func (iv Interval) Contains(k int) bool { return k >= iv.Start && k < iv.End }

// Pair is a key/value result returned by Scan.
type Pair struct {
	Key   int
	Value string
}

type writeOp struct {
	value string
	del   bool
}

// committedTxn records the write set of a committed transaction and the
// commit sequence number at which it became visible.
type committedTxn struct {
	seq    uint64
	writes map[int]writeOp
}

// DB is an in-memory integer-keyed store. It is safe for concurrent use.
type DB struct {
	mu      sync.Mutex
	data    map[int]string
	seq     uint64
	history []committedTxn
	active  map[uint64]int // snapshot seq -> number of open transactions
}

// Open returns an empty database.
func Open() *DB {
	return &DB{
		data:   make(map[int]string),
		active: make(map[uint64]int),
	}
}

// Begin starts a transaction at the current commit sequence.
func (db *DB) Begin() *Txn {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.active[db.seq]++
	return &Txn{
		db:       db,
		snapshot: db.seq,
		points:   make(map[int]struct{}),
		writes:   make(map[int]writeOp),
	}
}

// All returns a copy of the committed contents.
func (db *DB) All() map[int]string {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make(map[int]string, len(db.data))
	for k, v := range db.data {
		out[k] = v
	}
	return out
}

// leave unregisters an open transaction and prunes history that no open
// transaction can still validate against.
func (db *DB) leave(snapshot uint64) {
	db.active[snapshot]--
	if db.active[snapshot] == 0 {
		delete(db.active, snapshot)
	}
	if len(db.active) == 0 {
		db.history = nil
		return
	}
	min := db.seq
	for s := range db.active {
		if s < min {
			min = s
		}
	}
	kept := db.history[:0]
	for _, c := range db.history {
		if c.seq > min {
			kept = append(kept, c)
		}
	}
	db.history = kept
}

// Txn is an optimistic serializable transaction.
type Txn struct {
	db       *DB
	snapshot uint64
	ranges   []Interval
	points   map[int]struct{}
	writes   map[int]writeOp
	done     bool
}

// Get returns the value for key, reading the transaction's own writes first.
// The key is recorded as a point predicate.
func (t *Txn) Get(key int) (string, bool) {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	t.points[key] = struct{}{}
	if w, ok := t.writes[key]; ok {
		if w.del {
			return "", false
		}
		return w.value, true
	}
	v, ok := t.db.data[key]
	return v, ok
}

// Scan returns the committed pairs in the half-open range [start, end),
// overlaid with the transaction's own writes, ordered by key. The range
// predicate is recorded even when the result is empty.
func (t *Txn) Scan(start, end int) []Pair {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	t.ranges = append(t.ranges, Interval{Start: start, End: end})
	var out []Pair
	for k, v := range t.db.data {
		if k < start || k >= end {
			continue
		}
		if w, ok := t.writes[k]; ok {
			if !w.del {
				out = append(out, Pair{Key: k, Value: w.value})
			}
			continue
		}
		out = append(out, Pair{Key: k, Value: v})
	}
	for k, w := range t.writes {
		if w.del || k < start || k >= end {
			continue
		}
		if _, committed := t.db.data[k]; !committed {
			out = append(out, Pair{Key: k, Value: w.value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Put stages a write of value at key.
func (t *Txn) Put(key int, value string) {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	t.writes[key] = writeOp{value: value}
}

// Delete stages a deletion of key.
func (t *Txn) Delete(key int) {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	t.writes[key] = writeOp{del: true}
}

// readsKey reports whether key k is covered by any recorded read predicate.
func (t *Txn) readsKey(k int) bool {
	if _, ok := t.points[k]; ok {
		return true
	}
	for _, iv := range t.ranges {
		if iv.Contains(k) {
			return true
		}
	}
	return false
}

// Commit validates the transaction against everything committed after its
// snapshot and, if validation succeeds, applies the write set atomically.
func (t *Txn) Commit() error {
	db := t.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if t.done {
		return ErrTxnClosed
	}
	t.done = true
	for _, c := range db.history {
		if c.seq <= t.snapshot {
			continue
		}
		for k := range c.writes {
			if _, ok := t.writes[k]; ok {
				return ErrTxnAborted // write/write conflict
			}
			if t.readsKey(k) {
				return ErrTxnAborted // a committed write changed a read result
			}
		}
	}
	for k, w := range t.writes {
		if w.del {
			delete(db.data, k)
		} else {
			db.data[k] = w.value
		}
	}
	if len(t.writes) > 0 {
		db.seq++
		writes := make(map[int]writeOp, len(t.writes))
		for k, w := range t.writes {
			writes[k] = w
		}
		db.history = append(db.history, committedTxn{seq: db.seq, writes: writes})
	}
	db.leave(t.snapshot)
	return nil
}

// Rollback discards the transaction.
func (t *Txn) Rollback() error {
	db := t.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if t.done {
		return ErrTxnClosed
	}
	t.done = true
	db.leave(t.snapshot)
	return nil
}
