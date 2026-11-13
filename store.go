package netsec

import "sync"

// ContentStore owns the immutable unit blobs. A blob is live while it is
// referenced by a published manifest or by a staged publication. Blobs with no
// references are orphans and may be reclaimed.
type ContentStore struct {
	mu      sync.Mutex
	entries map[Digest]*contentEntry
}

type contentEntry struct {
	unit *UnitVersion
	refs int
}

// NewContentStore creates an empty content store.
func NewContentStore() *ContentStore {
	return &ContentStore{entries: make(map[Digest]*contentEntry)}
}

func (s *ContentStore) stage(u *UnitVersion) Digest {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := u.ContentRoot()
	e := s.entries[root]
	if e == nil {
		e = &contentEntry{unit: u}
		s.entries[root] = e
	}
	e.refs++
	return root
}

func (s *ContentStore) retain(u *UnitVersion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := u.ContentRoot()
	e := s.entries[root]
	if e == nil {
		e = &contentEntry{unit: u}
		s.entries[root] = e
	}
	e.refs++
}

func (s *ContentStore) release(root Digest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[root]; e != nil && e.refs > 0 {
		e.refs--
	}
}

// Len returns the number of retained blobs, including unreclaimed orphans.
func (s *ContentStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Has reports whether a blob with the given content root is present.
func (s *ContentStore) Has(root Digest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[root]
	return ok
}

// Refs returns the reference count for a blob.
func (s *ContentStore) Refs(root Digest) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[root]; e != nil {
		return e.refs
	}
	return 0
}

// Orphans returns the content roots of all unreferenced blobs.
func (s *ContentStore) Orphans() []Digest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Digest, 0)
	for root, e := range s.entries {
		if e.refs == 0 {
			out = append(out, root)
		}
	}
	return out
}

// ReclaimOrphans removes all unreferenced blobs and returns the number removed.
func (s *ContentStore) ReclaimOrphans() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for root, e := range s.entries {
		if e.refs == 0 {
			delete(s.entries, root)
			removed++
		}
	}
	return removed
}

// Recover reconstructs the store after a crash. Only content reachable from
// live survives; every staged or otherwise unreferenced blob is reclaimed.
func (s *ContentStore) Recover(live *Manifest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		e.refs = 0
	}
	if live != nil {
		for _, u := range live.units {
			root := u.ContentRoot()
			e := s.entries[root]
			if e == nil {
				e = &contentEntry{unit: u}
				s.entries[root] = e
			}
			e.refs++
		}
	}
	for root, e := range s.entries {
		if e.refs == 0 {
			delete(s.entries, root)
		}
	}
}
