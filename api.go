package netsec

import "sync"

// Forest is a concurrency-safe forest of resource nodes holding explicit
// tags, inherited tag caches and cut-off markers.
type Forest struct {
	mu         sync.RWMutex
	graph      *graph
	generation uint64
}

// NewForest creates an empty forest.
func NewForest() *Forest {
	return &Forest{graph: newGraph()}
}

// Generation returns a monotonic counter bumped on every mutating operation.
func (f *Forest) Generation() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.generation
}

// Roots returns the ids of root nodes in insertion order.
func (f *Forest) Roots() []NodeID {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return clonePath(f.graph.roots)
}

// Exists reports whether id belongs to the forest.
func (f *Forest) Exists(id NodeID) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.graph.nodes[id]
	return ok
}

// AddNode creates id as a child of parent. An empty parent creates a new
// forest root. The new node starts with no explicit tags or markers.
func (f *Forest) AddNode(id, parent NodeID) error {
	if id == "" {
		return ErrNodeNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.graph.nodes[id]; ok {
		return ErrNodeExists
	}
	if parent != "" {
		if _, ok := f.graph.nodes[parent]; !ok {
			return ErrNodeNotFound
		}
	}

	n := newNode(id)
	n.parent = parent
	f.graph.nodes[id] = n
	f.graph.addChildLocked(parent, id)
	f.generation++
	f.graph.recomputeClosureLocked(id)
	return nil
}

// Parent returns the parent of id, or an empty id when it is a root.
func (f *Forest) Parent(id NodeID) (NodeID, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	n, ok := f.graph.nodes[id]
	if !ok {
		return "", ErrNodeNotFound
	}
	return n.parent, nil
}

// Children returns the direct children of id in insertion order.
func (f *Forest) Children(id NodeID) ([]NodeID, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if id != "" {
		if _, ok := f.graph.nodes[id]; !ok {
			return nil, ErrNodeNotFound
		}
		return clonePath(f.graph.nodes[id].children), nil
	}
	return clonePath(f.graph.roots), nil
}

// Path returns the root-to-id chain including both endpoints.
func (f *Forest) Path(id NodeID) ([]NodeID, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return f.graph.pathLocked(id), nil
}

// SetTag stores an explicit key/value pair on id and re-resolves only the
// subtree rooted at id. Setting the same value again is a no-op.
func (f *Forest) SetTag(id NodeID, key, value string) error {
	if key == "" {
		return ErrNodeNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.graph.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if current, exists := n.explicit[key]; exists && current == value {
		return nil
	}
	n.explicit[key] = value
	f.generation++
	f.graph.recomputeClosureLocked(id)
	return nil
}

// DeleteTag removes the explicit value of key from id. The subtree then
// re-resolves inheritance; a cut-off marker still blocks the key.
func (f *Forest) DeleteTag(id NodeID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.graph.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if _, exists := n.explicit[key]; !exists {
		return nil
	}
	delete(n.explicit, key)
	f.generation++
	f.graph.recomputeClosureLocked(id)
	return nil
}

// BlockInheritance sets a cut-off marker for key on id. The marker prevents
// the key from ancestors penetrating id and its descendants until unblocked.
func (f *Forest) BlockInheritance(id NodeID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.graph.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if n.blocked[key] {
		return nil
	}
	n.blocked[key] = true
	f.generation++
	f.graph.recomputeClosureLocked(id)
	return nil
}

// UnblockInheritance removes the cut-off marker for key on id, allowing
// ancestor values to reach the subtree again.
func (f *Forest) UnblockInheritance(id NodeID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.graph.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if !n.blocked[key] {
		return nil
	}
	delete(n.blocked, key)
	f.generation++
	f.graph.recomputeClosureLocked(id)
	return nil
}

// IsBlocked reports whether id currently carries a cut-off marker for key.
func (f *Forest) IsBlocked(id NodeID, key string) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	n, ok := f.graph.nodes[id]
	if !ok {
		return false, ErrNodeNotFound
	}
	return n.blocked[key], nil
}

// ExplicitTags returns the tags assigned directly on id, sorted by key.
func (f *Forest) ExplicitTags(id NodeID) ([]Tag, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return f.graph.explicitLocked(id), nil
}

// InheritedTags returns defensive copies of the cached inheritance state of
// id, including entries stopped by cut-off markers, sorted by key.
func (f *Forest) InheritedTags(id NodeID) ([]Provenance, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return f.graph.inheritedLocked(id), nil
}

// EffectiveTags returns the resolved tags of id after applying explicit
// overrides and cut-off markers, sorted by key.
func (f *Forest) EffectiveTags(id NodeID) ([]EffectiveTag, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	return f.graph.effectiveLocked(id), nil
}

// VerifyProvenance re-walks the current ancestor chain of id and confirms
// every cached provenance entry still explains the live resolution.
func (f *Forest) VerifyProvenance(id NodeID) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.graph.nodes[id]; !ok {
		return false, ErrNodeNotFound
	}
	return f.graph.verifyProvenanceLocked(id), nil
}

// Snapshot captures an immutable deep copy of the forest. Later mutations do
// not affect the snapshot, and provenance keeps its original path meaning.
func (f *Forest) Snapshot() *Snapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return &Snapshot{
		graph:      f.graph.cloneLocked(),
		generation: f.generation,
	}
}
