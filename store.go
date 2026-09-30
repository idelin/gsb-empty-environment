package sstdict

import (
	"fmt"
	"sort"
	"sync"
)

// Store holds a stack of immutable segments (newest first) produced by
// batch dictionary updates. Segments are never mutated: a batch update
// appends a new segment, and Merge compacts all live segments into one.
// Old snapshots keep their pinned segment list and stay readable; a
// superseded segment becomes reclaimable only when no open snapshot
// references it (refcount reaches zero).
type Store struct {
	mu         sync.RWMutex
	segments   []*Segment // newest first
	nextID     uint64
	refs       map[uint64]int // segment id -> open snapshot references
	superseded map[uint64]int // segment id -> byte size, awaiting reclamation
	reclaimed  uint64
	reclaimBts uint64
}

func NewStore() *Store {
	return &Store{refs: make(map[uint64]int), superseded: make(map[uint64]int)}
}

// AddBatch builds a new immutable segment from words (any order) and
// makes it the newest segment. Duplicate words inside one batch are
// rejected (ErrDuplicate); the same word appearing in different batches
// collapses to a single entry on merge (newest batch wins, which is
// identical for a plain word set).
func (s *Store) AddBatch(words []string, blockSize int) error {
	sorted := make([]string, len(words))
	copy(sorted, words)
	sort.Strings(sorted)
	b := NewBuilder(blockSize)
	for _, w := range sorted {
		if err := b.Add(w); err != nil {
			return err
		}
	}
	seg, err := b.Build()
	if err != nil {
		return err
	}
	s.mu.Lock()
	seg.id = s.nextID
	s.nextID++
	s.segments = append([]*Segment{seg}, s.segments...)
	s.mu.Unlock()
	return nil
}

// Snapshot pins the current segment list. The caller must Close it.
func (s *Store) Snapshot() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	segs := make([]*Segment, len(s.segments))
	copy(segs, s.segments)
	for _, seg := range segs {
		s.refs[seg.id]++
	}
	return &Snapshot{store: s, segments: segs}
}

// Merge compacts all live segments into a single new segment with the
// given block size and swaps it in atomically. Old segments stay alive
// for open snapshots and are reclaimed once unpinned.
func (s *Store) Merge(blockSize int) error {
	s.mu.RLock()
	segs := make([]*Segment, len(s.segments))
	copy(segs, s.segments)
	s.mu.RUnlock()
	if len(segs) == 0 {
		return fmt.Errorf("sstdict: nothing to merge")
	}
	if len(segs) == 1 {
		return nil
	}
	merged, err := mergeSegments(segs, blockSize)
	if err != nil {
		return err
	}
	s.mu.Lock()
	merged.id = s.nextID
	s.nextID++
	old := s.segments
	s.segments = []*Segment{merged}
	for _, seg := range old {
		if s.refs[seg.id] == 0 {
			// Not live and not pinned: reclaimable immediately.
			s.reclaimed++
			s.reclaimBts += uint64(len(seg.data))
		} else {
			// Pinned by a snapshot: reclaimable once it closes.
			s.superseded[seg.id] = len(seg.data)
		}
	}
	s.mu.Unlock()
	return nil
}

// mergeSegments k-way merges segments (newest first) into one segment.
// Equal words collapse to a single occurrence (newest batch wins).
func mergeSegments(segs []*Segment, blockSize int) (*Segment, error) {
	its := make([]*Iterator, len(segs))
	heads := make([]string, len(segs))
	ok := make([]bool, len(segs))
	for i, seg := range segs {
		its[i] = seg.Iterator()
		heads[i], ok[i] = its[i].Next()
	}
	b := NewBuilder(blockSize)
	var prev string
	hasPrev := false
	for {
		min := ""
		any := false
		for i := range its {
			if ok[i] && (!any || heads[i] < min) {
				min = heads[i]
				any = true
			}
		}
		if !any {
			break
		}
		for i := range its {
			if ok[i] && heads[i] == min {
				heads[i], ok[i] = its[i].Next()
			}
		}
		if hasPrev && min == prev {
			continue
		}
		if err := b.Add(min); err != nil {
			return nil, err
		}
		prev = min
		hasPrev = true
	}
	for _, it := range its {
		if err := it.Err(); err != nil {
			return nil, err
		}
	}
	if !hasPrev {
		return nil, fmt.Errorf("sstdict: merge of empty segments")
	}
	return b.Build()
}

func (s *Store) release(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[id]--
	if s.refs[id] > 0 {
		return
	}
	delete(s.refs, id)
	size, wasSuperseded := s.superseded[id]
	if !wasSuperseded {
		return // still live: no reclamation
	}
	// Superseded by a merge AND no snapshot pins it: reclaimable.
	delete(s.superseded, id)
	s.reclaimed++
	s.reclaimBts += uint64(size)
}

// StoreStats reports segment lifecycle counters.
type StoreStats struct {
	LiveSegments      int
	OpenSnapshotsRefs int
	ReclaimedSegments uint64
	ReclaimedBytes    uint64
}

func (s *Store) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	refs := 0
	for _, n := range s.refs {
		refs += n
	}
	return StoreStats{
		LiveSegments:      len(s.segments),
		OpenSnapshotsRefs: refs,
		ReclaimedSegments: s.reclaimed,
		ReclaimedBytes:    s.reclaimBts,
	}
}

// Snapshot is a consistent, pinned view of the store's segments.
type Snapshot struct {
	store    *Store
	segments []*Segment
	closed   bool
}

// Close releases the snapshot's pins. Superseded segments whose refcount
// drops to zero become reclaimable.
func (sn *Snapshot) Close() {
	if sn.closed {
		return
	}
	sn.closed = true
	for _, seg := range sn.segments {
		sn.store.release(seg.id)
	}
}

// Len returns the number of distinct words visible in this snapshot.
func (sn *Snapshot) Len() (uint64, error) {
	if len(sn.segments) == 1 {
		return sn.segments[0].NumWords(), nil
	}
	var n uint64
	var prev string
	hasPrev := false
	err := sn.mergeWalk(func(w string) bool {
		if hasPrev && w == prev {
			return true
		}
		prev = w
		hasPrev = true
		n++
		return true
	})
	return n, err
}

// Get returns the k-th distinct word (0-based) in merged order. With a
// single live segment it decodes at most blockSize entries; with several
// segments it falls back to a merge walk.
func (sn *Snapshot) Get(k uint64, st *Stats) (string, error) {
	if len(sn.segments) == 0 {
		return "", fmt.Errorf("%w: empty snapshot", ErrRange)
	}
	if len(sn.segments) == 1 {
		return sn.segments[0].Get(k, st)
	}
	var idx uint64
	var prev string
	hasPrev := false
	var result string
	found := false
	err := sn.mergeWalk(func(w string) bool {
		if hasPrev && w == prev {
			return true
		}
		prev = w
		hasPrev = true
		if idx == k {
			result = w
			found = true
			return false
		}
		idx++
		return true
	})
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: %d", ErrRange, k)
	}
	if st != nil {
		st.observeRandomRead(0) // merge walk; not block-bounded
	}
	return result, nil
}

// PrefixRange returns all distinct words starting with prefix, merged
// across the snapshot's segments.
func (sn *Snapshot) PrefixRange(prefix string, st *Stats) ([]string, error) {
	if len(sn.segments) == 1 {
		return sn.segments[0].PrefixRange(prefix, st)
	}
	parts := make([][]string, len(sn.segments))
	for i, seg := range sn.segments {
		p, err := seg.PrefixRange(prefix, st)
		if err != nil {
			return nil, err
		}
		parts[i] = p
	}
	return mergeSorted(parts), nil
}

func mergeSorted(parts [][]string) []string {
	var out []string
	idx := make([]int, len(parts))
	var prev string
	hasPrev := false
	for {
		min := ""
		any := false
		for i, p := range parts {
			if idx[i] < len(p) && (!any || p[idx[i]] < min) {
				min = p[idx[i]]
				any = true
			}
		}
		if !any {
			return out
		}
		for i, p := range parts {
			if idx[i] < len(p) && p[idx[i]] == min {
				idx[i]++
			}
		}
		if hasPrev && min == prev {
			continue
		}
		prev = min
		hasPrev = true
		out = append(out, min)
	}
}

// mergeWalk streams the distinct merged word sequence across segments.
func (sn *Snapshot) mergeWalk(visit func(w string) bool) error {
	its := make([]*Iterator, len(sn.segments))
	heads := make([]string, len(its))
	ok := make([]bool, len(its))
	for i, seg := range sn.segments {
		its[i] = seg.Iterator()
		heads[i], ok[i] = its[i].Next()
	}
	for {
		min := ""
		any := false
		for i := range its {
			if ok[i] && (!any || heads[i] < min) {
				min = heads[i]
				any = true
			}
		}
		if !any {
			break
		}
		for i := range its {
			if ok[i] && heads[i] == min {
				heads[i], ok[i] = its[i].Next()
			}
		}
		if !visit(min) {
			break
		}
	}
	for _, it := range its {
		if err := it.Err(); err != nil {
			return err
		}
	}
	return nil
}
