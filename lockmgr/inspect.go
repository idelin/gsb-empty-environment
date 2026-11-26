package lockmgr

// Snapshot returns the current wait queue of resource r in scheduling order.
func (m *Manager) Snapshot(r Resource) []WaiterInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.resources[r.key()]
	if !ok {
		return nil
	}
	out := make([]WaiterInfo, 0, len(st.queue))
	for _, w := range st.queue {
		out = append(out, WaiterInfo{TxnID: w.txn.id, Want: w.want, Upgrade: w.upgrade})
	}
	return out
}

// WaitForEdges returns a snapshot of the complete waiting-for graph, one edge
// per conflicting held lock (multiple shared holders yield multiple edges).
func (m *Manager) WaitForEdges() []Edge {
	m.mu.Lock()
	defer m.mu.Unlock()
	edges, _ := m.buildWaitingFor()
	return append([]Edge(nil), edges...)
}

// DeadlockCycles returns every currently present minimal deadlock cycle.
func (m *Manager) DeadlockCycles() [][]uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, adj := m.buildWaitingFor()
	return findAllCycles(adj)
}

// Resolutions returns a copy of all deadlock arbitration outcomes so far.
func (m *Manager) Resolutions() []Resolution {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Resolution, len(m.resolutions))
	copy(out, m.resolutions)
	return out
}

// HeldLock returns the mode currently held by t on r, or None when the
// transaction holds no lock on that resource.
func (m *Manager) HeldLock(t *Transaction, r Resource) LockMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gl := t.locks[r.key()]; gl != nil {
		return gl.mode
	}
	return None
}

// Pending reports whether t currently has a blocked lock request.
func (m *Manager) Pending(t *Transaction) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return t.pending != nil
}

// TxnCount returns the number of active transactions.
func (m *Manager) TxnCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.txns)
}
