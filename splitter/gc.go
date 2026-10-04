package splitter

import "fmt"

// copyRef identifies one physical record copy in one target.
type copyRef struct {
	Location Location
	Key      string
	Version  uint64
}

// GC deletes record copies that are referenced neither by a committed
// pointer nor by any active snapshot. It never touches protected copies,
// so the read view is unchanged by collection.
func (s *Store) GC() (removed []copyRef, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	protected := map[copyRef]bool{}
	for _, p := range s.pointers {
		protected[copyRef{p.Location, p.Key, p.Version}] = true
	}
	snaps, err := s.loadSnapshots()
	if err != nil {
		return nil, err
	}
	for _, snap := range snaps {
		for _, p := range snap.Pointers {
			protected[copyRef{p.Location, p.Key, p.Version}] = true
		}
	}

	for _, loc := range []Location{Online, Archive} {
		t := s.target(loc)
		recs, err := t.List()
		if err != nil {
			return removed, err
		}
		for _, rec := range recs {
			ref := copyRef{loc, rec.Key, rec.Version}
			if protected[ref] {
				continue
			}
			if err := t.Delete(rec.Key, rec.Version); err != nil {
				return removed, fmt.Errorf("gc %s %s@%d: %v", loc, rec.Key, rec.Version, err)
			}
			removed = append(removed, ref)
		}
	}
	return removed, nil
}
