package netsec

// Snapshot is an immutable view of a forest captured at one generation.
// Paths and provenance entries continue to describe the topology that existed
// when the snapshot was taken, even after the live forest changes.
type Snapshot struct {
	graph      *graph
	generation uint64
}

// Generation returns the forest generation captured by the snapshot.
func (s *Snapshot) Generation() uint64 { return s.generation }

// Roots returns the root ids stored in the snapshot.
func (s *Snapshot) Roots() []NodeID { return clonePath(s.graph.roots) }

// Exists reports whether id exists in the snapshot.
func (s *Snapshot) Exists(id NodeID) bool {
	_, ok := s.graph.nodes[id]
	return ok
}

// Parent returns the parent recorded for id in the snapshot.
func (s *Snapshot) Parent(id NodeID) (NodeID, error) {
	n, ok := s.graph.nodes[id]
	if !ok {
		return "", ErrNodeNotFound
	}
	return n.parent, nil
}

// Children returns the direct children recorded for id in the snapshot.
func (s *Snapshot) Children(id NodeID) ([]NodeID, error) {
	if id != "" {
		n, ok := s.graph.nodes[id]
		if !ok {
			return nil, ErrNodeNotFound
		}
		return clonePath(n.children), nil
	}
	return clonePath(s.graph.roots), nil
}

// Path returns the root-to-id chain as it existed when captured.
func (s *Snapshot) Path(id NodeID) ([]NodeID, error) {
	if _, ok := s.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return s.graph.pathLocked(id), nil
}

// ExplicitTags returns the explicitly assigned tags captured for id.
func (s *Snapshot) ExplicitTags(id NodeID) ([]Tag, error) {
	if _, ok := s.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return s.graph.explicitLocked(id), nil
}

// InheritedTags returns cached provenance entries captured for id.
func (s *Snapshot) InheritedTags(id NodeID) ([]Provenance, error) {
	if _, ok := s.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return s.graph.inheritedLocked(id), nil
}

// EffectiveTags returns the resolved tags captured for id.
func (s *Snapshot) EffectiveTags(id NodeID) ([]EffectiveTag, error) {
	if _, ok := s.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return s.graph.effectiveLocked(id), nil
}

// IsBlocked reports the cut-off marker state captured for id and key.
func (s *Snapshot) IsBlocked(id NodeID, key string) (bool, error) {
	n, ok := s.graph.nodes[id]
	if !ok {
		return false, ErrNodeNotFound
	}
	return n.blocked[key], nil
}

// VerifyProvenance checks the captured provenance against the snapshot's own
// frozen ancestor chains. Snapshot state never changes, so a verified entry
// remains verifiable forever.
func (s *Snapshot) VerifyProvenance(id NodeID) (bool, error) {
	if _, ok := s.graph.nodes[id]; !ok {
		return false, ErrNodeNotFound
	}
	return s.graph.verifyProvenanceLocked(id), nil
}
