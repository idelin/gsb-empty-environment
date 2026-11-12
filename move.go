package netsec

// MoveTxn is a pending subtree relocation. It is created by Forest.BeginMove
// and ends with exactly one Commit or Rollback call.
type MoveTxn struct {
	forest *Forest
	node   NodeID
	target NodeID
	backup *graph
	closed bool
}

// BeginMove starts a transaction that will detach subtree root id and attach
// it under newParent. An empty newParent turns the subtree into a forest root.
// The forest is not modified until Commit succeeds.
func (f *Forest) BeginMove(id, newParent NodeID) (*MoveTxn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.graph.nodes[id]; !ok {
		return nil, ErrNodeNotFound
	}
	if newParent != "" {
		if _, ok := f.graph.nodes[newParent]; !ok {
			return nil, ErrNodeNotFound
		}
		if newParent == id || f.graph.isDescendantLocked(id, newParent) {
			return nil, ErrCycle
		}
	}

	return &MoveTxn{
		forest: f,
		node:   id,
		target: newParent,
		backup: f.graph.cloneLocked(),
	}, nil
}

// Node returns the subtree root governed by the transaction.
func (t *MoveTxn) Node() NodeID { return t.node }

// NewParent returns the target parent captured by the transaction.
func (t *MoveTxn) NewParent() NodeID { return t.target }

// Commit applies the relocation. Only caches inside the moved subtree are
// re-resolved, so inherited values from the old location can never survive as
// explicit tags. A second Commit returns ErrTxnClosed.
func (t *MoveTxn) Commit() error {
	if t.closed {
		return ErrTxnClosed
	}
	f := t.forest
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.graph.nodes[t.node]; !ok {
		return ErrNodeNotFound
	}
	if t.target != "" {
		if _, ok := f.graph.nodes[t.target]; !ok {
			return ErrNodeNotFound
		}
		if t.target == t.node || f.graph.isDescendantLocked(t.node, t.target) {
			return ErrCycle
		}
	}

	n := f.graph.nodes[t.node]
	oldParent := n.parent
	if oldParent == t.target {
		t.closed = true
		t.backup = nil
		return nil
	}

	f.graph.removeChildLocked(oldParent, t.node)
	n.parent = t.target
	f.graph.addChildLocked(t.target, t.node)
	f.generation++

	// Re-resolve the relocated closure against the new ancestor chain. Explicit
	// tags are untouched; only inherited caches are rebuilt.
	f.graph.recomputeClosureLocked(t.node)

	t.closed = true
	t.backup = nil
	return nil
}

// Rollback discards the transaction and restores the exact graph state
// captured by BeginMove, including every provenance path.
func (t *MoveTxn) Rollback() error {
	if t.closed {
		return ErrTxnClosed
	}
	f := t.forest
	f.mu.Lock()
	defer f.mu.Unlock()

	f.graph = t.backup
	t.closed = true
	t.backup = nil
	return nil
}
