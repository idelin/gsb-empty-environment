package ssi

// Txn returns the transaction handle for id, or nil when unknown.
func (m *Manager) Txn(id TxnID) *Txn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.txns[id]
}

// ActiveCount returns the number of transactions still in flight.
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

// EdgeCount returns the number of distinct rw-antidependency edges
// currently tracked between non-aborted transactions.
func (m *Manager) EdgeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, t := range m.txns {
		if t.status == StatusAborted {
			continue
		}
		for to := range t.out {
			if m.txns[to].status != StatusAborted {
				count++
			}
		}
	}
	return count
}

// HasEdge reports whether a directed rw edge from -> to exists.
func (m *Manager) HasEdge(from, to TxnID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	src := m.txns[from]
	if src == nil || src.status == StatusAborted {
		return false
	}
	dst := m.txns[to]
	if dst == nil || dst.status == StatusAborted {
		return false
	}
	_, ok := src.out[to]
	return ok
}

// HoldsTableLock reports whether the transaction holds a table-level
// SIREAD lock on table.
func (m *Manager) HoldsTableLock(t *Txn, table string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := t.tableLocks[table]
	return ok
}

// RowLockCount returns the row-level SIREAD locks held by the transaction
// on table.
func (m *Manager) RowLockCount(t *Txn, table string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(t.rowLocks[table])
}

// RangeLockCount returns the index-range SIREAD locks held by the
// transaction on table.
func (m *Manager) RangeLockCount(t *Txn, table string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(t.rangeLocks[table])
}

// OutConflict reports whether the transaction has an outgoing rw edge.
func (m *Manager) OutConflict(t *Txn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(t.out) > 0
}

// InConflict reports whether the transaction has an incoming rw edge.
func (m *Manager) InConflict(t *Txn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(t.in) > 0
}
