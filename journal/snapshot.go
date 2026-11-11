package journal

// Snapshot is an immutable, point-in-time view of the index. It is built by
// deep-copying index state while the processor lock is held, so queries run
// against it without holding the lock and can never observe an in-flight
// transaction (notably a rename between its old and new entry).
type Snapshot struct {
	objects map[FileID]ObjectView
	entries map[LinkKey]FileID
	stats   Stats
}

// ObjectView is the immutable projection of an object at snapshot time.
type ObjectView struct {
	ID        FileID
	FirstSeen int64
	Links     []LinkKey
}

func newSnapshot(ix *Index) *Snapshot {
	snap := &Snapshot{
		objects: make(map[FileID]ObjectView, len(ix.Objects)),
		entries: make(map[LinkKey]FileID, len(ix.Entries)),
		stats:   ix.Stats,
	}
	for id, obj := range ix.Objects {
		links := make([]LinkKey, len(obj.Links))
		copy(links, obj.Links)
		snap.objects[id] = ObjectView{ID: obj.ID, FirstSeen: obj.FirstSeen, Links: links}
	}
	for key, id := range ix.Entries {
		snap.entries[key] = id
	}
	return snap
}

// LookupEntry returns the object owning a directory entry.
func (s *Snapshot) LookupEntry(key LinkKey) (FileID, bool) {
	id, ok := s.entries[key]
	return id, ok
}

// LookupObject returns an object by its full (ref, seq) identity.
func (s *Snapshot) LookupObject(id FileID) (ObjectView, bool) {
	view, ok := s.objects[id]
	return view, ok
}

// Links returns a copy of the hard-link set of an object.
func (s *Snapshot) Links(id FileID) ([]LinkKey, bool) {
	view, ok := s.objects[id]
	if !ok {
		return nil, false
	}
	out := make([]LinkKey, len(view.Links))
	copy(out, view.Links)
	return out, true
}

// ObjectCount reports how many live objects the snapshot contains.
func (s *Snapshot) ObjectCount() int { return len(s.objects) }

// EntryCount reports how many directory entries (hard links) exist.
func (s *Snapshot) EntryCount() int { return len(s.entries) }

// Stats returns the counters as of the snapshot.
func (s *Snapshot) Stats() Stats { return s.stats }

// Entries returns a copy of the full directory-entry map, useful for
// invariant checks.
func (s *Snapshot) Entries() map[LinkKey]FileID {
	out := make(map[LinkKey]FileID, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}
