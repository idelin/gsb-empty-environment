package gcstore

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GC reclaims space occupied by unreachable blocks.
//
// The phases are strictly ordered:
//
//  1. Mark: every block reachable from any snapshot root is live.
//  2. Copy: live blocks are copied into a fresh gc-* segment. Each block
//     ID is copied exactly once, even when reachable from several
//     snapshots or via several paths.
//  3. Cutpoint: the new segment is fsynced, renamed to its final name,
//     and the new mapping is atomically persisted. Old segments may only
//     be deleted after this point.
//  4. Sweep: old segments are deleted and a fresh active segment starts.
//
// If copying would exceed MaxBytes, GC aborts before the cutpoint: the
// partial gc-* segment is removed, the old mapping and segments are left
// untouched, and ErrNoSpace is returned. A crash before the cutpoint
// leaves a gc-* file that the next Open removes; a crash after the
// cutpoint is fully consistent because the persisted mapping already
// points at the new segment.
func (s *Store) GC() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	live, err := s.markLiveLocked()
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	newMapping := make(map[string]blockRef, len(live))

	if len(ids) > 0 {
		s.segSeq++
		finalName := segmentName(s.segSeq)
		gcPath := filepath.Join(s.segmentsDir(), gcName(s.segSeq))
		f, err := os.Create(gcPath)
		if err != nil {
			return err
		}
		abort := func(err error) error {
			f.Close()
			os.Remove(gcPath)
			return err
		}

		var off int64
		for _, id := range ids {
			blk, err := s.getLocked(id)
			if err != nil {
				return abort(err)
			}
			rec := encodeRecord(blk)
			if s.maxBytes > 0 && s.usageLocked()+int64(len(rec)) > s.maxBytes {
				return abort(ErrNoSpace)
			}
			if _, err := f.Write(rec); err != nil {
				return abort(err)
			}
			newMapping[id] = blockRef{Segment: finalName, Offset: off}
			off += int64(len(rec))
		}
		if err := f.Sync(); err != nil {
			return abort(err)
		}
		if err := f.Close(); err != nil {
			return abort(err)
		}

		if s.beforeMappingPersist != nil {
			if err := s.beforeMappingPersist(); err != nil {
				// Simulated crash: leave the gc-* file behind; the next
				// Open removes it and the old mapping is still in effect.
				return err
			}
		}
		if err := os.Rename(gcPath, filepath.Join(s.segmentsDir(), finalName)); err != nil {
			return err
		}
	}

	oldMapping := s.mapping
	s.mapping = newMapping
	if err := s.persistManifestLocked(); err != nil {
		s.mapping = oldMapping
		return err
	}
	// --- persistence cutpoint: from here on the new mapping is durable ---
	if s.afterMappingPersist != nil {
		if err := s.afterMappingPersist(); err != nil {
			// Simulated crash: state is already consistent; old segments
			// simply leak until a later GC.
			return err
		}
	}

	if s.active != nil {
		s.active.Close()
		s.active = nil
	}
	keep := make(map[string]struct{}, len(newMapping))
	for _, ref := range newMapping {
		keep[ref.Segment] = struct{}{}
	}
	entries, err := ioutil.ReadDir(s.segmentsDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "seg-") {
			continue
		}
		if _, ok := keep[name]; ok {
			continue
		}
		os.Remove(filepath.Join(s.segmentsDir(), name))
	}
	return s.openActiveLocked()
}

// markLiveLocked computes the set of blocks reachable from every
// snapshot root by breadth-first traversal of block links. Dangling
// links (references to unknown blocks) are tolerated.
func (s *Store) markLiveLocked() (map[string]struct{}, error) {
	live := make(map[string]struct{})
	var queue []string
	for _, roots := range s.snapshots {
		queue = append(queue, roots...)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := live[id]; ok {
			continue
		}
		blk, err := s.getLocked(id)
		if err == ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		live[id] = struct{}{}
		queue = append(queue, blk.Links...)
	}
	return live, nil
}
