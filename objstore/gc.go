package objstore

import "fmt"

// gcState tracks an in-progress mark phase using the tri-color abstraction:
//
//   white  - not in marked: unreachable as far as the collector knows
//   grey   - in marked and in worklist: reachable, children not yet scanned
//   black  - in marked, popped from worklist: reachable and scanned
//
// The sweeper only reclaims objects that are still white when the worklist
// is empty. The insertion write barrier (shadeLocked, called from Commit)
// greys any object that becomes referenced during marking, which is the
// standard Dijkstra barrier: it preserves the strong tri-color invariant
// (no black object points to a white object) and therefore guarantees a
// concurrent sweep never deletes a live object.
type gcState struct {
	marked   map[ID]bool // grey or black
	worklist []ID        // grey objects
}

// shadeLocked is the insertion write barrier. Callers must hold s.mu and
// only call it when s.gc != nil.
func (s *Store) shadeLocked(id ID) {
	if s.gc.marked[id] {
		return
	}
	if _, ok := s.objects[id]; !ok {
		return
	}
	s.gc.marked[id] = true
	s.gc.worklist = append(s.gc.worklist, id)
}

// GC runs one full mark-sweep cycle and returns the number of reclaimed
// objects. It is safe to run concurrently with transactions: root changes
// and new objects created during the mark phase are retained via the write
// barrier. Sweep deletions are logged to the WAL (a single fsynced record)
// before being applied, so a crash mid-sweep recovers consistently.
func (s *Store) GC() (int, error) {
	s.gcMu.Lock()
	defer s.gcMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrClosed
	}
	if s.gc != nil {
		s.mu.Unlock()
		return 0, fmt.Errorf("objstore: gc mark already in progress")
	}
	s.gc = &gcState{marked: make(map[ID]bool)}
	for _, id := range s.roots {
		s.shadeLocked(id)
	}
	s.mu.Unlock()

	for {
		s.mu.Lock()
		if len(s.gc.worklist) == 0 {
			s.mu.Unlock()
			// The worklist looked empty. Give the test hook (and any
			// racing commit) a chance to shade more objects, then
			// re-check. The final empty-check and the sweep happen in
			// one critical section, and the barrier needs s.mu, so no
			// new grey object can appear in between.
			if s.markHook != nil {
				s.markHook(true)
			}
			s.mu.Lock()
			if len(s.gc.worklist) == 0 {
				n, err := s.sweepLocked()
				s.mu.Unlock()
				return n, err
			}
			s.mu.Unlock()
			continue
		}
		id := s.gc.worklist[len(s.gc.worklist)-1]
		s.gc.worklist = s.gc.worklist[:len(s.gc.worklist)-1]
		obj := s.objects[id]
		var refs []ID
		if obj != nil {
			refs = append([]ID(nil), obj.Refs...)
		}
		s.mu.Unlock()

		if s.markHook != nil {
			s.markHook(false)
		}

		s.mu.Lock()
		for _, r := range refs {
			s.shadeLocked(r)
		}
		s.mu.Unlock()
	}
}

// sweepLocked computes the white set, persists the deletions, and applies
// them, all while holding s.mu so no commit can interleave and resurrect a
// reference to an object about to be reclaimed.
func (s *Store) sweepLocked() (int, error) {
	var dead []ID
	for id := range s.objects {
		if !s.gc.marked[id] {
			dead = append(dead, id)
		}
	}
	if len(dead) > 0 {
		if err := appendWALRecord(s.wal, &walRecord{Dels: dead}); err != nil {
			s.gc = nil
			return 0, err
		}
		for _, id := range dead {
			delete(s.objects, id)
		}
	}
	s.gc = nil
	return len(dead), nil
}
