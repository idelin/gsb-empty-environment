package lockmgr

import "time"

// rollbackPlan is one candidate way of breaking a deadlock cycle.
type rollbackPlan struct {
	txn     *Transaction
	abort   bool
	spIndex int // valid when !abort
	sp      *savepoint
	cost    int
}

// resolveDeadlocks detects every minimal cycle and breaks them one at a time,
// rebuilding the graph after each remedy, until no cycle remains.
func (m *Manager) resolveDeadlocks() {
	for {
		edges, adj := m.buildWaitingFor()
		cycles := findAllCycles(adj)
		if len(cycles) == 0 {
			return
		}

		// Collect every valid remedy offered by any transaction of any cycle,
		// tagged with the first cycle it can break, then take the globally
		// cheapest one.
		var best *rollbackPlan
		var bestCycle []uint64
		for _, cycle := range cycles {
			for _, id := range cycle {
				candidate := m.bestPlanFor(id, edges)
				if candidate == nil {
					continue
				}
				if best == nil || m.planLess(candidate, best) {
					best = candidate
					bestCycle = cycle
				}
			}
		}

		if best == nil {
			// Should be unreachable: full abort is always a valid remedy.
			return
		}
		m.applyPlan(best, bestCycle)
	}
}

// bestPlanFor returns the cheapest valid remedy available to txnID, preferring
// a savepoint rollback over a full abort at equal cost.
func (m *Manager) bestPlanFor(txnID uint64, edges []Edge) *rollbackPlan {
	t, ok := m.txns[txnID]
	if !ok {
		return nil
	}

	var best *rollbackPlan

	// Full abort: always valid; it releases every held lock and cancels the
	// pending request, so every edge incident to the transaction disappears.
	abortPlan := &rollbackPlan{txn: t, abort: true, cost: len(t.locks)}
	best = abortPlan

	for i := len(t.savepoints) - 1; i >= 0; i-- {
		sp := t.savepoints[i]
		valid, cost := m.evaluateSavepoint(t, sp, edges)
		if !valid {
			continue
		}
		candidate := &rollbackPlan{
			txn:     t,
			spIndex: i,
			sp:      sp,
			cost:    cost,
		}
		if m.planLess(candidate, best) {
			best = candidate
		}
	}
	return best
}

// evaluateSavepoint determines whether rolling t back to sp breaks a current
// deadlock edge and computes the rollback cost.
//
// A remedy is valid when either:
//
//  1. the rollback cancels t's own blocked request (removing its outgoing
//     edge), or
//  2. it releases / downgrades a held lock that backs an incoming edge, so
//     some waiting transaction is no longer blocked on t.
func (m *Manager) evaluateSavepoint(t *Transaction, sp *savepoint, edges []Edge) (bool, int) {
	cutoff := sp.seq

	// (1) Pending request must have been issued after the savepoint; rolling
	// back then cancels it and removes the outgoing edge.
	cancelsPending := false
	if t.pending != nil {
		if gl := t.locks[t.pending.resource.key()]; gl != nil {
			cancelsPending = gl.history[len(gl.history)-1].seq > cutoff
		} else {
			cancelsPending = true
		}
	}

	// (2) Examine each held lock's mode after the rollback.
	breaksIncoming := false
	cost := 0
	for key, gl := range t.locks {
		postMode := None
		for _, step := range gl.history {
			if step.seq <= cutoff {
				postMode = step.to
			}
		}
		if postMode == gl.mode {
			continue // this lock is untouched by the rollback
		}
		// A lock is genuinely undone only when its final mode after the
		// rollback differs from the current mode. Escalations that return to
		// the pre-savepoint mode are folded back into that original lock and
		// do not count as separate released locks.
		cost++
		// The lock is released (None) or downgraded: every incoming waiter
		// whose request is compatible with the post-rollback mode is freed.
		for _, e := range edges {
			if e.To != t.id || e.Resource.key() != key {
				continue
			}
			if postMode == None || compatible(postMode, m.waiterWant(e.From, key)) {
				breaksIncoming = true
			}
		}
	}

	if !cancelsPending && !breaksIncoming {
		return false, 0
	}
	return true, cost
}

// waiterWant returns the mode requested by from's waiter on resource key, or
// None when no such waiter exists.
func (m *Manager) waiterWant(from uint64, key string) LockMode {
	if st, ok := m.resources[key]; ok {
		for _, w := range st.queue {
			if w.txn.id == from {
				return w.want
			}
		}
	}
	return None
}

// planLess implements the deterministic total order over remedies:
// lower cost first, then savepoint rollback over abort, then the later-started
// transaction, then the larger transaction id.
func (m *Manager) planLess(a, b *rollbackPlan) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.abort != b.abort {
		return !a.abort // savepoint rollback (abort == false) wins
	}
	if a.txn.start != b.txn.start {
		return a.txn.start > b.txn.start // later start wins
	}
	return a.txn.id > b.txn.id // larger id wins
}

// applyPlan executes the chosen remedy and records the arbitration outcome.
func (m *Manager) applyPlan(plan *rollbackPlan, cycle []uint64) {
	victim := plan.txn
	var spSnapshot *Savepoint

	if plan.abort {
		m.finishTransaction(victim, ErrDeadlockRollback, true)
	} else {
		sp := victim.savepoints[plan.spIndex]
		snapshot := sp.Savepoint
		spSnapshot = &snapshot
		m.rollbackToSavepoint(victim, plan.spIndex, ErrDeadlockRollback)
	}

	m.resolutions = append(m.resolutions, Resolution{
		Cycle:     append([]uint64(nil), cycle...),
		Victim:    victim.id,
		Savepoint: spSnapshot,
		Cost:      plan.cost,
		At:        time.Now(),
	})
}
