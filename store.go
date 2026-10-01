package blkdict

import (
	"fmt"
	"sort"
	"sync"
)

// Stats collects operational counters.
type Stats struct {
	RandomReads       int // Get calls served
	MaxDecodedPerRead int // max entries decoded by a single random read (<= BlockSize)
	PrefixQueries     int // PrefixRange calls served
	SegmentsReclaimed int // segments whose last pin was dropped
}

// Store owns the active segment plus open snapshots. Segments are immutable;
// a batch update merges the batch with the active segment into a NEW segment
// and atomically swaps the view. Old segments stay alive while any snapshot
// pins them and are reclaimed exactly when their pin count reaches zero
// (i.e. replaced in the active view AND no open snapshot references them).
type Store struct {
	mu       sync.Mutex
	active   *Segment
	nextID   uint64
	nextSnap uint64
	stats    Stats
}

// NewStore builds a store from sorted-or-unsorted unique keys.
// Duplicate keys are rejected (see buildSegment).
func NewStore(keys []string) (*Store, error) {
	sorted := sortKeys(keys)
	st := &Store{}
	seg, err := st.buildNext(sorted)
	if err != nil {
		return nil, err
	}
	st.active = seg
	return st, nil
}

func sortKeys(keys []string) []string {
	out := make([]string, len(keys))
	copy(out, keys)
	sort.Strings(out)
	return out
}

func (st *Store) buildNext(sorted []string) (*Segment, error) {
	st.nextID++
	return buildSegment(st.nextID, sorted)
}

// ApplyBatch adds a batch of keys. Duplicates WITHIN the batch are rejected;
// keys already present in the dictionary are ignored (idempotent insert).
// The result is a single freshly merged immutable segment; the previous
// segment remains available to existing snapshots.
func (st *Store) ApplyBatch(adds []string) error {
	sorted := sortKeys(adds)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return &DuplicateError{Key: sorted[i]}
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	merged := sorted
	if st.active != nil {
		cur, err := st.active.allKeys()
		if err != nil {
			return err
		}
		merged = mergeKeys(cur, sorted)
	}
	seg, err := st.buildNext(merged)
	if err != nil {
		return err
	}
	old := st.active
	st.active = seg
	if old != nil {
		st.unpinLocked(old)
	}
	return nil
}

// DuplicateError reports a repeated key inside one batch.
type DuplicateError struct{ Key string }

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("blkdict: duplicate key %q in batch", e.Key)
}

// unpinLocked drops one pin; a segment with zero pins is reclaimed.
func (st *Store) unpinLocked(s *Segment) {
	s.refs--
	if s.refs == 0 {
		st.stats.SegmentsReclaimed++
	}
}

// Len returns the number of keys in the active view.
func (st *Store) Len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.active == nil {
		return 0
	}
	return st.active.Len()
}

// Get returns the k-th key (0-based) of the active view and records the
// number of entries decoded into Stats.MaxDecodedPerRead.
func (st *Store) Get(k int) (string, error) {
	st.mu.Lock()
	seg := st.active
	st.mu.Unlock()
	if seg == nil {
		return "", &CorruptError{Reason: "empty store"}
	}
	key, decoded, err := seg.Get(k)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	st.stats.RandomReads++
	if decoded > st.stats.MaxDecodedPerRead {
		st.stats.MaxDecodedPerRead = decoded
	}
	st.mu.Unlock()
	return key, nil
}

// PrefixRange returns all active-view keys with the given byte prefix.
func (st *Store) PrefixRange(prefix string) ([]string, error) {
	st.mu.Lock()
	seg := st.active
	st.mu.Unlock()
	if seg == nil {
		return nil, nil
	}
	out, err := seg.PrefixRange(prefix)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.stats.PrefixQueries++
	st.mu.Unlock()
	return out, nil
}

// Stats returns a copy of the current counters.
func (st *Store) Stats() Stats {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.stats
}

// ActiveSegmentBytes returns the encoded byte size of the active segment.
func ActiveSegmentBytes(st *Store) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.active == nil {
		return 0
	}
	return st.active.Bytes()
}

// Snapshot is a stable read view pinning one immutable segment.
type Snapshot struct {
	store  *Store
	id     uint64
	seg    *Segment
	closed bool
}

// Snapshot captures the current view; the underlying segment is not
// reclaimed until Close, even after later batch updates.
func (st *Store) Snapshot() *Snapshot {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.nextSnap++
	if st.active != nil {
		st.active.refs++
	}
	return &Snapshot{store: st, id: st.nextSnap, seg: st.active}
}

// Close releases the snapshot's pin; the segment is reclaimed when this was
// its last pin.
func (sn *Snapshot) Close() {
	st := sn.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if sn.closed {
		return
	}
	sn.closed = true
	if sn.seg != nil {
		st.unpinLocked(sn.seg)
	}
}

// Len returns the number of keys visible to the snapshot.
func (sn *Snapshot) Len() int {
	if sn.seg == nil {
		return 0
	}
	return sn.seg.Len()
}

// Get returns the k-th key visible to the snapshot.
func (sn *Snapshot) Get(k int) (string, error) {
	if sn.seg == nil {
		return "", &CorruptError{Reason: "empty snapshot"}
	}
	key, _, err := sn.seg.Get(k)
	return key, err
}

// PrefixRange returns the snapshot's keys with the given byte prefix.
func (sn *Snapshot) PrefixRange(prefix string) ([]string, error) {
	if sn.seg == nil {
		return nil, nil
	}
	return sn.seg.PrefixRange(prefix)
}
