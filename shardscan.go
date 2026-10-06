// Package shardscan models a distributed full-table scan in which shards may
// split and merge while a scan is in flight.
//
// A cursor binds to a logical key boundary (the last returned key) plus the
// snapshot it was issued from. It never references a physical shard ID, so
// shards may split or merge mid-scan without losing or duplicating keys:
// resuming only requires locating whichever shard currently owns the boundary
// key. Cursors merged from parallel sub-scans are validated to belong to the
// same snapshot.
package shardscan

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrSnapshotMismatch is returned when a cursor issued by one snapshot is
	// used with a scanner (or merged with a cursor) of another snapshot.
	ErrSnapshotMismatch = errors.New("shardscan: cursor belongs to a different snapshot")
	// ErrSnapshotGone is returned when the snapshot a scanner reads from has
	// been released.
	ErrSnapshotGone = errors.New("shardscan: snapshot has been released")
	// ErrInvalidLimit is returned for a page limit smaller than 1.
	ErrInvalidLimit = errors.New("shardscan: limit must be >= 1")
	// ErrEmptyKey is returned when putting an empty key. The empty string is
	// reserved as the -inf/+inf range sentinel.
	ErrEmptyKey = errors.New("shardscan: key must not be empty")
	// ErrInvalidSplit is returned for a split at the -inf sentinel.
	ErrInvalidSplit = errors.New("shardscan: cannot split at -inf")
	// ErrNoMergePeer is returned when a shard has no successor to merge with.
	ErrNoMergePeer = errors.New("shardscan: no adjacent shard to merge with")
)

// KV is a single row of the table.
type KV struct {
	Key   string
	Value string
}

// Cursor marks a scan position. It binds to the logical key boundary After
// (the last key returned, "" meaning the very beginning) and to the snapshot
// the scan reads from. It deliberately carries no shard ID.
type Cursor struct {
	Snapshot uint64
	After    string
	Done     bool
}

// MergeCursors combines two cursors from parallel sub-scans of the same
// snapshot. The merged cursor resumes after the further of the two
// boundaries and is done only when both halves are done. Cursors from
// different snapshots are rejected.
func MergeCursors(a, b Cursor) (Cursor, error) {
	if a.Snapshot == 0 {
		return b, nil
	}
	if b.Snapshot == 0 {
		return a, nil
	}
	if a.Snapshot != b.Snapshot {
		return Cursor{}, ErrSnapshotMismatch
	}
	merged := Cursor{Snapshot: a.Snapshot, After: a.After, Done: a.Done && b.Done}
	if b.After > a.After {
		merged.After = b.After
	}
	return merged, nil
}

// shard is a physical key range [start, end). The empty string is the
// sentinel for -inf (start of first shard) and +inf (end of last shard).
type shard struct {
	id    int
	start string
	end   string
}

// Store holds the live table, the pinned read snapshots, and the physical
// shard layout. Shards always form a contiguous cover of the whole key
// space, no matter how splits and merges interleave.
type Store struct {
	mu        sync.Mutex
	live      map[string]string
	snapshots map[uint64][]KV
	shards    []shard
	nextSnap  uint64
	nextShard int
}

// NewStore returns a store with a single shard covering all keys.
func NewStore() *Store {
	return &Store{
		live:      make(map[string]string),
		snapshots: make(map[uint64][]KV),
		shards:    []shard{{id: 1}},
		nextShard: 1,
	}
}

// Put upserts a row into the live table. Already pinned snapshots are not
// affected.
func (s *Store) Put(key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[key] = value
	return nil
}

// findShardIndex returns the index of the shard owning key. Callers must
// hold the lock. Total coverage guarantees a result for any key.
func (s *Store) findShardIndex(key string) int {
	i := sort.Search(len(s.shards), func(i int) bool { return s.shards[i].start > key })
	return i - 1
}

// SplitAt divides the shard owning key into [start, key) and [key, end).
// Splitting at an existing boundary or at a key with no data near it
// legitimately produces an empty shard.
func (s *Store) SplitAt(key string) error {
	if key == "" {
		return ErrInvalidSplit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findShardIndex(key)
	sh := s.shards[i]
	s.nextShard++
	left := shard{id: s.nextShard, start: sh.start, end: key}
	s.nextShard++
	right := shard{id: s.nextShard, start: key, end: sh.end}
	next := make([]shard, 0, len(s.shards)+1)
	next = append(next, s.shards[:i]...)
	next = append(next, left, right)
	next = append(next, s.shards[i+1:]...)
	s.shards = next
	return nil
}

// MergeAt merges the shard owning key with its successor.
func (s *Store) MergeAt(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findShardIndex(key)
	if i < 0 || i+1 >= len(s.shards) {
		return ErrNoMergePeer
	}
	s.nextShard++
	merged := shard{id: s.nextShard, start: s.shards[i].start, end: s.shards[i+1].end}
	next := make([]shard, 0, len(s.shards)-1)
	next = append(next, s.shards[:i]...)
	next = append(next, merged)
	next = append(next, s.shards[i+2:]...)
	s.shards = next
	return nil
}

// BeginScan pins the current live table as an immutable sorted snapshot and
// returns a scanner over it. Later writes, splits, and merges do not change
// what this scanner sees.
func (s *Store) BeginScan() *Scanner {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSnap++
	snap := make([]KV, 0, len(s.live))
	for k, v := range s.live {
		snap = append(snap, KV{Key: k, Value: v})
	}
	sort.Slice(snap, func(i, j int) bool { return snap[i].Key < snap[j].Key })
	s.snapshots[s.nextSnap] = snap
	return &Scanner{store: s, snap: s.nextSnap}
}

// checkInvariants verifies the shards form a contiguous, ordered cover of
// the whole key space.
func (s *Store) checkInvariants() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.shards) == 0 {
		return errors.New("no shards")
	}
	if s.shards[0].start != "" {
		return fmt.Errorf("first shard starts at %q, want -inf", s.shards[0].start)
	}
	for i, sh := range s.shards {
		if sh.end != "" && sh.start > sh.end {
			return fmt.Errorf("shard %d has start %q after end %q", sh.id, sh.start, sh.end)
		}
		if i+1 < len(s.shards) && sh.end != s.shards[i+1].start {
			return fmt.Errorf("gap or overlap between shards %d and %d (%q != %q)",
				sh.id, s.shards[i+1].id, sh.end, s.shards[i+1].start)
		}
	}
	if s.shards[len(s.shards)-1].end != "" {
		return fmt.Errorf("last shard ends at %q, want +inf", s.shards[len(s.shards)-1].end)
	}
	return nil
}

// Scanner reads one pinned snapshot page by page.
type Scanner struct {
	store *Store
	snap  uint64
}

// Snapshot returns the ID of the snapshot this scanner reads from.
func (sc *Scanner) Snapshot() uint64 { return sc.snap }

// Close releases the pinned snapshot.
func (sc *Scanner) Close() {
	sc.store.mu.Lock()
	defer sc.store.mu.Unlock()
	delete(sc.store.snapshots, sc.snap)
}

// Next returns up to limit rows after cur, walking shards from whichever one
// currently owns the resume key. A zero Cursor starts at the beginning.
func (sc *Scanner) Next(cur Cursor, limit int) ([]KV, Cursor, error) {
	if limit < 1 {
		return nil, cur, ErrInvalidLimit
	}
	sc.store.mu.Lock()
	defer sc.store.mu.Unlock()

	snap, ok := sc.store.snapshots[sc.snap]
	if !ok {
		return nil, cur, ErrSnapshotGone
	}
	if cur.Snapshot != 0 && cur.Snapshot != sc.snap {
		return nil, cur, ErrSnapshotMismatch
	}
	if cur.Done {
		return nil, cur, nil
	}

	out := make([]KV, 0, limit)
	// Resume position: first snapshot entry strictly after the boundary key.
	pos := sort.Search(len(snap), func(i int) bool { return snap[i].Key > cur.After })

	// Walk shards starting at the owner of the boundary key. Empty shards
	// simply contribute nothing; splits and merges since the last page only
	// change which physical shard serves which key, never the key order.
	si := 0
	if cur.After != "" {
		si = sc.store.findShardIndex(cur.After)
	}
	for si < len(sc.store.shards) && len(out) < limit {
		sh := sc.store.shards[si]
		hi := len(snap)
		if sh.end != "" {
			hi = sort.Search(len(snap), func(i int) bool { return snap[i].Key >= sh.end })
		}
		for pos < hi && len(out) < limit {
			out = append(out, snap[pos])
			pos++
		}
		si++
	}

	next := Cursor{Snapshot: sc.snap, After: cur.After}
	if len(out) > 0 {
		next.After = out[len(out)-1].Key
	}
	if pos >= len(snap) {
		next.Done = true
	}
	return out, next, nil
}
