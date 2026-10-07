package gcstore

import (
	"bytes"
	"os"
	"sort"
)

// GCState tracks an in-progress garbage collection across its four
// phases: mark, copy, persist (the cut point) and sweep. Splitting GC
// into phases makes the persistence cut point explicit: a segment may
// only be deleted (sweep) after every live block it held has been
// copied and the new mapping has been durably persisted.
type GCState struct {
	// Live is the set of blocks reachable from all snapshot roots.
	Live map[Hash]bool
	// Candidates are the sealed segments eligible for collection.
	Candidates map[uint64]bool
	// NewSeg is the segment written during the copy phase (0 if none).
	NewSeg uint64
	// Copied maps each relocated block to its new location.
	Copied map[Hash]Location
	// CopiedBytes is the total size of copied records.
	CopiedBytes int64
}

// GCMark computes the live set by traversing the block graph from
// every snapshot root, and selects all sealed segments as collection
// candidates.
func (s *Store) GCMark() (*GCState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[Hash]bool)
	stack := make([]Hash, 0, len(s.snapshots))
	for _, root := range s.snapshots {
		stack = append(stack, root)
	}
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if live[h] {
			continue
		}
		loc, ok := s.mapping[h]
		if !ok {
			return nil, ErrCorrupt
		}
		live[h] = true
		b, err := s.readAtLocked(loc)
		if err != nil {
			return nil, err
		}
		stack = append(stack, b.Refs...)
	}
	candidates := make(map[uint64]bool)
	ids, err := s.segmentIDsLocked()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if s.active != nil && id == s.activeID {
			continue
		}
		candidates[id] = true
	}
	return &GCState{Live: live, Candidates: candidates, Copied: make(map[Hash]Location)}, nil
}

// GCCopy relocates every live block held by a candidate segment into a
// single new segment. Each block hash is copied at most once, so
// duplicate copies of the same content collapse to one. If the store
// would exceed its space budget during the copy, the phase aborts,
// removes the partial segment and returns ErrNoSpace, leaving the
// mapping and old segments untouched.
func (s *Store) GCCopy(st *GCState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSeg
	path := s.segPath(id)
	used, err := s.dirBytesLocked()
	if err != nil {
		return err
	}
	var f *os.File
	var off int64
	cleanup := func() {
		if f != nil {
			f.Close()
		}
		os.Remove(path)
	}

	hashes := make([]Hash, 0, len(st.Live))
	for h := range st.Live {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i][:], hashes[j][:]) < 0
	})

	for _, h := range hashes {
		loc := s.mapping[h]
		if !st.Candidates[loc.Seg] {
			continue // already resides in a kept segment
		}
		if _, done := st.Copied[h]; done {
			continue // dedup: keep a single copy
		}
		b, err := s.readAtLocked(loc)
		if err != nil {
			cleanup()
			return err
		}
		rec := encodeRecord(b)
		if s.maxBytes > 0 && used+int64(len(rec)) > s.maxBytes {
			cleanup()
			return ErrNoSpace
		}
		if f == nil {
			nf, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			f = nf
		}
		if _, err := f.Write(rec); err != nil {
			cleanup()
			return err
		}
		st.Copied[h] = Location{Seg: id, Off: off, Len: uint32(len(rec))}
		off += int64(len(rec))
		used += int64(len(rec))
		st.CopiedBytes += int64(len(rec))
	}
	if f != nil {
		if err := f.Sync(); err != nil {
			cleanup()
			return err
		}
		if err := f.Close(); err != nil {
			cleanup()
			return err
		}
		st.NewSeg = id
	}
	return nil
}

// GCPersist is the cut point: it applies the relocated locations to
// the mapping, drops entries for dead blocks held by candidate
// segments, and atomically persists the result. Only after this phase
// succeeds may candidate segments be deleted.
func (s *Store) GCPersist(st *GCState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, loc := range st.Copied {
		s.mapping[h] = loc
	}
	for h, loc := range s.mapping {
		if !st.Live[h] && st.Candidates[loc.Seg] {
			delete(s.mapping, h)
		}
	}
	if st.NewSeg >= s.nextSeg {
		s.nextSeg = st.NewSeg + 1
	}
	return s.persistMappingLocked()
}

// GCSweep deletes the candidate segments. It must only be called after
// GCPersist has succeeded.
func (s *Store) GCSweep(st *GCState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range st.Candidates {
		if err := os.Remove(s.segPath(id)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// GC runs a full collection: mark, copy, persist, sweep. If the copy
// phase runs out of space, GC returns ErrNoSpace and the store is left
// exactly as before.
func (s *Store) GC() error {
	st, err := s.GCMark()
	if err != nil {
		return err
	}
	if err := s.GCCopy(st); err != nil {
		return err
	}
	if err := s.GCPersist(st); err != nil {
		return err
	}
	return s.GCSweep(st)
}
