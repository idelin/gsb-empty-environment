// Package kvindex implements an in-memory key-value index that accepts
// point writes and half-open range deletes. Visibility is decided by
// monotonically increasing sequence numbers: a newer point write can
// resurrect a key covered by an older tombstone. The index supports
// historical queries (read at any past sequence number) and range scans
// with pagination.
//
// Visibility model (per-key history): every key owns an ascending list of
// events (put / tombstone) stamped with sequence numbers. A key is visible
// at sequence q iff the latest event with seq <= q is a put. Because
// sequence numbers are monotonic, a range delete at seq s can only affect
// keys whose latest event is older than s, i.e. keys already present in
// the index; the delete is therefore materialized as one tombstone event
// per existing key in [start, end). Writes that arrive later carry higher
// sequence numbers and naturally sit after the tombstone in the history.
package kvindex

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrNonMonotonicSeq is returned when an operation carries a sequence
	// number lower than the last applied one.
	ErrNonMonotonicSeq = errors.New("kvindex: sequence number went backwards")
	// ErrConflictingOp is returned when the last sequence number is reused
	// by a different operation. Replaying the identical operation with the
	// same sequence number is accepted as a no-op.
	ErrConflictingOp = errors.New("kvindex: sequence number reused by a different operation")
	// ErrInvalidRange is returned for an empty or inverted delete/scan range.
	ErrInvalidRange = errors.New("kvindex: range start must be less than end")
)

// OpKind identifies the type of a mutation.
type OpKind int

const (
	// OpPut is a point write of Key/Value.
	OpPut OpKind = iota
	// OpDeleteRange is a tombstone over the half-open range [Start, End).
	OpDeleteRange
)

// Operation is a single sequenced mutation. It is a comparable value, which
// is what makes idempotent-replay detection a simple equality check.
type Operation struct {
	Kind  OpKind
	Seq   uint64
	Key   string // OpPut
	Value string // OpPut
	Start string // OpDeleteRange, inclusive
	End   string // OpDeleteRange, exclusive
}

// event is one entry in a key's history.
type event struct {
	seq       uint64
	value     string
	tombstone bool
}

// keyHistory holds the events of one key in ascending sequence order.
type keyHistory struct {
	events []event
}

// visibleAt returns the value visible at sequence seq, if any.
func (h *keyHistory) visibleAt(seq uint64) (string, bool) {
	// Index of the last event with event.seq <= seq.
	i := sort.Search(len(h.events), func(i int) bool { return h.events[i].seq > seq }) - 1
	if i < 0 || h.events[i].tombstone {
		return "", false
	}
	return h.events[i].value, true
}

// Entry is one visible key/value pair returned by a scan.
type Entry struct {
	Key   string
	Value string
}

// Index is a goroutine-safe sequenced key-value index.
type Index struct {
	mu      sync.RWMutex
	hist    map[string]*keyHistory
	order   []string // all known keys, sorted
	lastSeq uint64
	lastOp  Operation
	hasOp   bool
}

// New returns an empty index.
func New() *Index {
	return &Index{hist: make(map[string]*keyHistory)}
}

// begin validates the sequence number of op. It reports whether the caller
// should apply the mutation: an identical replay of the last operation is
// accepted but must not be applied twice.
func (ix *Index) begin(op Operation) (apply bool, err error) {
	if ix.hasOp {
		switch {
		case op.Seq < ix.lastSeq:
			return false, ErrNonMonotonicSeq
		case op.Seq == ix.lastSeq:
			if op != ix.lastOp {
				return false, ErrConflictingOp
			}
			return false, nil // idempotent replay
		}
	}
	ix.lastSeq, ix.lastOp, ix.hasOp = op.Seq, op, true
	return true, nil
}

// LastSeq returns the highest applied sequence number and whether any
// operation has been applied at all.
func (ix *Index) LastSeq() (uint64, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.lastSeq, ix.hasOp
}

// Put applies a point write of value at key with sequence number seq.
func (ix *Index) Put(key, value string, seq uint64) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	op := Operation{Kind: OpPut, Seq: seq, Key: key, Value: value}
	apply, err := ix.begin(op)
	if err != nil || !apply {
		return err
	}
	h := ix.hist[key]
	if h == nil {
		h = &keyHistory{}
		ix.hist[key] = h
		ix.insertKey(key)
	}
	h.events = append(h.events, event{seq: seq, value: value})
	return nil
}

// DeleteRange tombstones every key currently present in the half-open
// range [start, end) at sequence number seq. Keys written later (with a
// higher sequence number) are unaffected, even if they sort inside the
// range. Deleting an already-deleted key simply nests another tombstone
// in its history.
func (ix *Index) DeleteRange(start, end string, seq uint64) error {
	if start >= end {
		return ErrInvalidRange
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	op := Operation{Kind: OpDeleteRange, Seq: seq, Start: start, End: end}
	apply, err := ix.begin(op)
	if err != nil || !apply {
		return err
	}
	lo := sort.SearchStrings(ix.order, start)
	hi := sort.SearchStrings(ix.order, end)
	for _, k := range ix.order[lo:hi] {
		h := ix.hist[k]
		h.events = append(h.events, event{seq: seq, tombstone: true})
	}
	return nil
}

// Get returns the value of key visible at sequence number seq.
func (ix *Index) Get(key string, seq uint64) (string, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	h := ix.hist[key]
	if h == nil {
		return "", false
	}
	return h.visibleAt(seq)
}

// ScanOptions configures a range scan.
type ScanOptions struct {
	Start string // inclusive lower bound
	End   string // exclusive upper bound; empty means unbounded
	After string // exclusive continuation token from a previous page
	Seq   uint64 // sequence number at which the scan reads
	Limit int    // maximum entries per page; <= 0 means unlimited
}

// Scan returns the entries visible at o.Seq inside [o.Start, o.End),
// starting strictly after o.After when it is set. If more visible entries
// remain, hasMore is true and next is the continuation token to pass back
// as ScanOptions.After.
func (ix *Index) Scan(o ScanOptions) (entries []Entry, next string, hasMore bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	lo := sort.SearchStrings(ix.order, o.Start)
	if o.After != "" {
		// First key strictly greater than the continuation token.
		if up := sort.Search(len(ix.order), func(i int) bool { return ix.order[i] > o.After }); up > lo {
			lo = up
		}
	}

	limit := o.Limit
	for i := lo; i < len(ix.order); i++ {
		k := ix.order[i]
		if o.End != "" && k >= o.End {
			break
		}
		if o.After != "" && k <= o.After {
			continue
		}
		v, ok := ix.hist[k].visibleAt(o.Seq)
		if !ok {
			continue
		}
		if limit > 0 && len(entries) >= limit {
			// A further visible entry exists: the page is full.
			return entries, entries[len(entries)-1].Key, true
		}
		entries = append(entries, Entry{Key: k, Value: v})
	}
	return entries, "", false
}

// insertKey inserts k into the sorted key list. Callers must hold the lock
// and guarantee k is not present yet.
func (ix *Index) insertKey(k string) {
	i := sort.SearchStrings(ix.order, k)
	ix.order = append(ix.order, "")
	copy(ix.order[i+1:], ix.order[i:])
	ix.order[i] = k
}
