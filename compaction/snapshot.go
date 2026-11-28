package compaction

import "sync"

// Snapshot anchors an immutable view at a sequence number. Compactions must
// preserve every version that an active snapshot could still observe.
type Snapshot struct {
	seq uint64
}

// Seq returns the sequence number the snapshot was taken at.
func (s *Snapshot) Seq() uint64 { return s.seq }

// SnapshotTracker records the set of active read snapshots. The oldest
// snapshot sequence is the compaction visibility floor.
type SnapshotTracker struct {
	mu     sync.Mutex
	active map[*Snapshot]struct{}
	nextID uint64
}

// NewSnapshotTracker creates an empty tracker.
func NewSnapshotTracker() *SnapshotTracker {
	return &SnapshotTracker{active: make(map[*Snapshot]struct{})}
}

// Acquire pins a new snapshot at seq. The caller must eventually Release it.
func (t *SnapshotTracker) Acquire(seq uint64) *Snapshot {
	snap := &Snapshot{seq: seq}
	t.mu.Lock()
	t.active[snap] = struct{}{}
	t.mu.Unlock()
	return snap
}

// Release unpins a snapshot so the versions it protected become reclaimable.
func (t *SnapshotTracker) Release(snap *Snapshot) {
	t.mu.Lock()
	delete(t.active, snap)
	t.mu.Unlock()
}

// HasActive reports whether at least one snapshot is pinned.
func (t *SnapshotTracker) HasActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.active) > 0
}

// OldestSeq returns the smallest active snapshot sequence and true, or zero
// and false when no snapshot is pinned.
func (t *SnapshotTracker) OldestSeq() (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var oldest uint64
	found := false
	for snap := range t.active {
		if !found || snap.seq < oldest {
			oldest = snap.seq
			found = true
		}
	}
	return oldest, found
}
