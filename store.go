// Package scanmodel models a sharded key-value store whose shards may be
// split and merged while a scan is in flight. Scan cursors are bound to a
// logical key boundary (the last returned key) rather than a physical shard
// ID, and every page is read from a fixed snapshot taken when the scan
// started. As a result, topology changes can neither lose nor duplicate
// keys, and cursors from different snapshots can never be merged.
package scanmodel

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrSnapshotMismatch is returned when two cursors that belong to
	// different snapshots are merged.
	ErrSnapshotMismatch = errors.New("scanmodel: cursors belong to different snapshots")
	// ErrStaleSnapshot is returned when a cursor is resumed against a
	// snapshot other than the one it was issued from.
	ErrStaleSnapshot = errors.New("scanmodel: cursor snapshot does not match scanner snapshot")
	// ErrSplitOutOfRange is returned when a split key does not fall
	// strictly inside the shard being split.
	ErrSplitOutOfRange = errors.New("scanmodel: split key outside shard range")
	// ErrShardNotFound is returned when no shard with the given ID exists.
	ErrShardNotFound = errors.New("scanmodel: shard not found")
	// ErrNotAdjacent is returned when two shards cannot be merged because
	// their key ranges are not contiguous.
	ErrNotAdjacent = errors.New("scanmodel: shards are not adjacent")
	// ErrInvalidLimit is returned when a scan page limit is not positive.
	ErrInvalidLimit = errors.New("scanmodel: limit must be positive")
)

// Shard is a physical partition of the key space. The half-open range
// [Start, End) uses "" as the -inf (Start) and +inf (End) sentinels.
// Shard IDs are ephemeral: splits and merges always mint fresh IDs so a
// stale ID can never silently point at a different range.
type Shard struct {
	ID    uint64
	Start string
	End   string
}

// Contains reports whether key falls inside the shard range.
func (s *Shard) Contains(key string) bool {
	return (s.Start == "" || key >= s.Start) && (s.End == "" || key < s.End)
}

// Store is a sorted key set partitioned into a contiguous list of shards.
// The data itself is immutable between snapshots: scans always read from a
// Snapshot, so in-flight scans observe a stable data set even while the
// shard topology mutates underneath them.
type Store struct {
	mu        sync.Mutex
	data      map[string]struct{}
	shards    []*Shard // contiguous, sorted by range, covering (-inf, +inf)
	nextShard uint64
	nextSnap  uint64
}

// NewStore creates a store holding keys (duplicates ignored, empty keys
// rejected because "" is the range sentinel) covered by a single shard.
func NewStore(keys ...string) (*Store, error) {
	s := &Store{data: make(map[string]struct{}, len(keys)), nextShard: 1, nextSnap: 1}
	for _, k := range keys {
		if k == "" {
			return nil, errors.New("scanmodel: empty keys are not allowed")
		}
		s.data[k] = struct{}{}
	}
	s.shards = []*Shard{{ID: s.mintShard()}}
	return s, nil
}

func (s *Store) mintShard() uint64 {
	id := s.nextShard
	s.nextShard++
	return id
}

// Shards returns a snapshot of the current topology.
func (s *Store) Shards() []Shard {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Shard, len(s.shards))
	for i, sh := range s.shards {
		out[i] = *sh
	}
	return out
}

// shardIndex locates a shard by ID. Caller must hold s.mu.
func (s *Store) shardIndex(id uint64) (int, error) {
	for i, sh := range s.shards {
		if sh.ID == id {
			return i, nil
		}
	}
	return -1, ErrShardNotFound
}

// Split divides the shard with the given ID at key, replacing it with two
// adjacent shards [Start, key) and [key, End) with fresh IDs. The split
// key must lie strictly inside the shard range. Splitting never touches
// data, so it cannot affect any in-flight scan.
func (s *Store) Split(shardID uint64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.shardIndex(shardID)
	if err != nil {
		return err
	}
	sh := s.shards[i]
	if key == "" || (sh.Start != "" && key <= sh.Start) || (sh.End != "" && key >= sh.End) {
		return fmt.Errorf("%w: %q not inside [%q, %q)", ErrSplitOutOfRange, key, sh.Start, sh.End)
	}
	left := &Shard{ID: s.mintShard(), Start: sh.Start, End: key}
	right := &Shard{ID: s.mintShard(), Start: key, End: sh.End}
	next := make([]*Shard, 0, len(s.shards)+1)
	next = append(next, s.shards[:i]...)
	next = append(next, left, right)
	next = append(next, s.shards[i+1:]...)
	s.shards = next
	return nil
}

// Merge fuses the shard with the given ID and its right neighbor into a
// single shard with a fresh ID. Adjacency is validated explicitly even
// though the shard list is contiguous by construction.
func (s *Store) Merge(shardID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.shardIndex(shardID)
	if err != nil {
		return err
	}
	if i+1 >= len(s.shards) {
		return fmt.Errorf("%w: shard %d has no right neighbor", ErrNotAdjacent, shardID)
	}
	left, right := s.shards[i], s.shards[i+1]
	if left.End != right.Start {
		return fmt.Errorf("%w: [%q,%q) and [%q,%q)", ErrNotAdjacent, left.Start, left.End, right.Start, right.End)
	}
	merged := &Shard{ID: s.mintShard(), Start: left.Start, End: right.End}
	next := make([]*Shard, 0, len(s.shards)-1)
	next = append(next, s.shards[:i]...)
	next = append(next, merged)
	next = append(next, s.shards[i+2:]...)
	s.shards = next
	return nil
}

// Snapshot is a fixed, immutable view of the store's data. Scans read only
// from a snapshot, so splits and merges during a scan cannot change what
// the scan sees.
type Snapshot struct {
	id   uint64
	keys []string // sorted, immutable
}

// ID identifies the snapshot; cursors carry it so mixed-snapshot cursors
// can be rejected.
func (s *Snapshot) ID() uint64 { return s.id }

// Keys returns the sorted key set frozen into the snapshot.
func (s *Snapshot) Keys() []string {
	out := make([]string, len(s.keys))
	copy(out, s.keys)
	return out
}

// BeginSnapshot freezes the current data set into a new snapshot.
func (s *Store) BeginSnapshot() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	id := s.nextSnap
	s.nextSnap++
	return &Snapshot{id: id, keys: keys}
}

// Cursor marks a scan position. It binds to a logical key boundary
// (LastKey, the last key returned; "" means "before the first key") and to
// the snapshot the scan reads from — never to a physical shard ID, so it
// stays valid across any number of splits and merges.
type Cursor struct {
	SnapshotID uint64
	LastKey    string
}

// MergeCursors combines two cursors into the one that has made more
// progress. Cursors from different snapshots are incomparable and must be
// rejected: merging them would silently mix two inconsistent views.
func MergeCursors(a, b Cursor) (Cursor, error) {
	if a.SnapshotID != b.SnapshotID {
		return Cursor{}, fmt.Errorf("%w: %d vs %d", ErrSnapshotMismatch, a.SnapshotID, b.SnapshotID)
	}
	if a.LastKey >= b.LastKey {
		return a, nil
	}
	return b, nil
}

// Scanner iterates a snapshot in ascending key order, one page at a time.
// Each page is resolved against the *current* shard topology — the cursor
// is mapped to whichever shard now covers its logical boundary — while the
// keys themselves come from the fixed snapshot.
type Scanner struct {
	store *Store
	snap  *Snapshot
	cur   Cursor
	done  bool
}

// NewScanner starts a scan over snap at the beginning of the key space.
func (s *Store) NewScanner(snap *Snapshot) *Scanner {
	return &Scanner{store: s, snap: snap, cur: Cursor{SnapshotID: snap.id}}
}

// Resume continues a scan from a previously obtained cursor. The cursor
// must have been issued by a scanner over the same snapshot.
func (s *Store) Resume(snap *Snapshot, cur Cursor) (*Scanner, error) {
	if cur.SnapshotID != snap.id {
		return nil, fmt.Errorf("%w: cursor snapshot %d, scanner snapshot %d", ErrStaleSnapshot, cur.SnapshotID, snap.id)
	}
	return &Scanner{store: s, snap: snap, cur: cur}, nil
}

// Cursor returns the current scan position.
func (sc *Scanner) Cursor() Cursor { return sc.cur }

// locateShard returns the index of the first shard that may still contain
// keys greater than pos, i.e. the first shard whose exclusive End is
// strictly above pos. pos == "" (scan start) selects the first shard.
func locateShard(shards []*Shard, pos string) int {
	for i, sh := range shards {
		if sh.End == "" || pos < sh.End {
			return i
		}
	}
	return len(shards)
}

// Next returns the next page of up to limit keys. done is true once the
// scan is exhausted; the final page may be short or empty. Keys are always
// drawn from the snapshot, but the walk advances shard by shard over the
// live topology, so empty shards and freshly split/merged ranges are
// exercised exactly as a real shard-aware scan would.
func (sc *Scanner) Next(limit int) (page []string, done bool, err error) {
	if limit <= 0 {
		return nil, false, ErrInvalidLimit
	}
	if sc.done {
		return nil, true, nil
	}

	sc.store.mu.Lock()
	shards := make([]*Shard, len(sc.store.shards))
	copy(shards, sc.store.shards)
	sc.store.mu.Unlock()

	pos := sc.cur.LastKey
	page = make([]string, 0, limit)
	for i := locateShard(shards, pos); i < len(shards) && len(page) < limit; i++ {
		sh := shards[i]
		// First snapshot key that is both > pos and >= shard start.
		lo := sort.Search(len(sc.snap.keys), func(j int) bool { return sc.snap.keys[j] > pos })
		for lo < len(sc.snap.keys) && sc.snap.keys[lo] < sh.Start {
			lo++
		}
		for lo < len(sc.snap.keys) && len(page) < limit {
			k := sc.snap.keys[lo]
			if sh.End != "" && k >= sh.End {
				break
			}
			page = append(page, k)
			lo++
		}
		// Note: pos deliberately stays at the last returned key. A key
		// equal to a shard boundary belongs to the right-hand shard, and
		// the per-shard lower bound (keys > pos and >= shard.Start)
		// already lands exactly on it.
	}

	if len(page) > 0 {
		sc.cur.LastKey = page[len(page)-1]
	}
	if len(page) < limit {
		sc.done = true
	}
	return page, sc.done, nil
}
