package lockmgr

// CreateSavepoint pushes a named savepoint onto the transaction's stack.
// An empty name is allowed.
func (m *Manager) CreateSavepoint(t *Transaction, name string) (Savepoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.active {
		return Savepoint{}, ErrTxnNotActive
	}
	m.nextSpID++
	sp := &savepoint{Savepoint: Savepoint{
		id:   m.nextSpID,
		name: name,
		seq:  m.nextSeq(),
	}}
	t.savepoints = append(t.savepoints, sp)
	return sp.Savepoint, nil
}

// ReleaseSavepoint discards a savepoint (and every savepoint above it). It
// does not release any locks.
func (m *Manager) ReleaseSavepoint(t *Transaction, sp Savepoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.active {
		return ErrTxnNotActive
	}
	idx := m.findSavepoint(t, sp.id)
	if idx < 0 {
		return ErrSavepointNotFound
	}
	t.savepoints = t.savepoints[:idx]
	return nil
}

// RollbackToSavepoint undoes every lock acquisition and upgrade performed
// after sp, cancels no request (the transaction must have no pending request)
// and pops sp together with every savepoint above it. sp itself is consumed,
// matching SQL ROLLBACK TO SAVEPOINT semantics for bookkeeping.
func (m *Manager) RollbackToSavepoint(t *Transaction, sp Savepoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.active {
		return ErrTxnNotActive
	}
	if t.pending != nil {
		return ErrTxnBusy
	}
	idx := m.findSavepoint(t, sp.id)
	if idx < 0 {
		return ErrSavepointNotFound
	}
	m.rollbackToSavepoint(t, idx, nil)
	return nil
}

func (m *Manager) findSavepoint(t *Transaction, id uint64) int {
	for i, sp := range t.savepoints {
		if sp.id == id {
			return i
		}
	}
	return -1
}

// rollbackToSavepoint performs the internal rollback to stack index idx.
// pendingReason is delivered to the transaction's blocked waiter when the
// rollback is triggered by deadlock arbitration (the caller has already
// removed the waiter from its queue); it must be nil for user rollbacks.
//
// It returns the set of resource keys whose lock state changed so callers can
// repump the affected queues.
func (m *Manager) rollbackToSavepoint(t *Transaction, idx int, pendingReason error) []string {
	sp := t.savepoints[idx]
	cutoff := sp.seq

	affected := make(map[string]bool)

	// Undo lock acquisitions / upgrades performed after the savepoint.
	for key, gl := range t.locks {
		kept := make([]modeStep, 0, len(gl.history))
		for _, step := range gl.history {
			if step.seq <= cutoff {
				kept = append(kept, step)
			}
		}
		gl.history = kept

		if len(gl.history) == 0 {
			// Acquired entirely after the savepoint: release the lock.
			if st, ok := m.resources[key]; ok {
				m.releaseHolder(st, t)
			}
			delete(t.locks, key)
			affected[key] = true
			continue
		}
		last := gl.history[len(gl.history)-1]
		if gl.mode != last.to {
			gl.mode = last.to
			affected[key] = true
		}
	}

	// Cancel the pending request created after the savepoint. Deadlock
	// arbitration only ever selects a savepoint whose sequence is below the
	// pending request, so this branch is taken for victim rollbacks.
	if t.pending != nil && pendingReason != nil {
		w := t.pending
		if st, ok := m.resources[w.resource.key()]; ok {
			m.removeWaiter(st, w)
		}
		t.pending = nil
		w.ch <- pendingReason
		affected[w.resource.key()] = true
	}

	t.savepoints = t.savepoints[:idx]

	keys := make([]string, 0, len(affected))
	for key := range affected {
		keys = append(keys, key)
	}
	m.pumpKeyed(keys)
	return keys
}
