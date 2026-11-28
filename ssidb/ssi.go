package ssidb

import "sort"

// addRWEdgeLocked creates reader -> writer (T1 --rw--> T2) and immediately runs
// dangerous-structure detection, catching anomalies at the earliest edge.
func (e *Engine) addRWEdgeLocked(reader, writer *Txn) {
	if reader == nil || writer == nil || reader == writer {
		return
	}
	if _, exists := reader.outEdges[writer]; exists {
		return
	}
	reader.outEdges[writer] = struct{}{}
	writer.inEdges[reader] = struct{}{}

	e.checkDangerousLocked(reader, writer)
}

// checkDangerousLocked implements PostgreSQL's predicate: an edge T1 -> T2 is
// part of a dangerous structure when some pivot carries both an incoming edge
// T1 -> T2 and an outgoing edge T2 -> T3, and T3 committed before T1 and T2
// (the "out-conflict committed first" condition). The same edge closing a
// 2-cycle (T3 == T1) is a write-skew structure regardless of ordering.
func (e *Engine) checkDangerousLocked(from, to *Txn) {
	type triple struct{ t1, pivot, t3 *Txn }
	var structures []triple

	consider := func(t1, pivot, t3 *Txn) {
		if t1 == nil || pivot == nil || t3 == nil {
			return
		}
		if t1.status == statusAborted || pivot.status == statusAborted || t3.status == statusAborted {
			return
		}
		if _, ok := t1.outEdges[pivot]; !ok {
			return
		}
		if _, ok := pivot.outEdges[t3]; !ok {
			return
		}
		if t1 == t3 {
			structures = append(structures, triple{t1, pivot, t3})
			return
		}
		if t3.status != statusCommitted {
			return
		}
		// PostgreSQL's "out-conflict committed first": t3's commit must be
		// invisible to both t1 and the pivot, i.e. newer than their snapshots.
		if t3.commitID > t1.snapshotSeq && t3.commitID > pivot.snapshotSeq {
			structures = append(structures, triple{t1, pivot, t3})
		}
	}

	// The new edge is from -> to. It may act as the in-edge of a pivot
	// (pivot == to) or as the out-edge of a pivot (pivot == from); inspect
	// every neighbourhood combination so a structure closing on either side
	// of the new edge is detected.
	for t1 := range to.inEdges {
		for t3 := range to.outEdges {
			consider(t1, to, t3)
		}
	}
	// Pivot == from: the new edge is its in-edge, pair it with each out-edge.
	for t3 := range from.outEdges {
		consider(from, from, t3)
	}
	if len(structures) == 0 {
		return
	}
	sort.Slice(structures, func(i, j int) bool {
		a, b := structures[i], structures[j]
		if a.t1.id != b.t1.id {
			return a.t1.id < b.t1.id
		}
		if a.pivot.id != b.pivot.id {
			return a.pivot.id < b.pivot.id
		}
		return a.t3.id < b.t3.id
	})

	for _, s := range structures {
		if victim := e.pickVictimLocked(s.t1, s.pivot, s.t3); victim != nil && victim.status == statusActive {
			e.abortLocked(victim, ErrSerialization)
		}
	}
}

// pickVictimLocked never selects a committed transaction. An uncommitted
// pivot is preferred (PostgreSQL aborts the pivot whenever possible); if the
// pivot already committed, the still-active reader T1 is aborted; an active
// T3 is the conservative fallback.
func (e *Engine) pickVictimLocked(t1, pivot, t3 *Txn) *Txn {
	if pivot.status == statusActive {
		return pivot
	}
	if t1.status == statusActive {
		return t1
	}
	if t3.status == statusActive {
		return t3
	}
	return nil
}

// abortLocked marks t as aborted with reason, removes its write intents and
// wakes waiters. Its SIREAD locks and graph edges are intentionally retained
// until garbage collection.
func (e *Engine) abortLocked(t *Txn, reason error) {
	if t.status != statusActive {
		return
	}
	t.status = statusAborted
	t.abortErr = reason
	for key, writers := range e.intents {
		delete(writers, t)
		if len(writers) == 0 {
			delete(e.intents, key)
		}
	}
}

// precommitDangerLocked is a defensive re-evaluation run when a transaction
// tries to commit: if any dangerous structure still exists involving t and
// every participant except t is already committed, t is aborted so a committed
// transaction is never chosen as the victim.
func (e *Engine) precommitDangerLocked(t *Txn) bool {
	// t as a pivot with an in-edge and an out-edge.
	for t1 := range t.inEdges {
		if t1.status == statusAborted {
			continue
		}
		for t3 := range t.outEdges {
			if t3.status == statusAborted {
				continue
			}
			if t1 == t3 || (t3.status == statusCommitted && t3.commitID > t1.snapshotSeq && t3.commitID > t.snapshotSeq) {
				e.abortLocked(t, ErrSerialization)
				return true
			}
		}
	}
	// t as the reader t1 of a committed pivot.
	for pivot := range t.outEdges {
		if pivot.status != statusCommitted {
			continue
		}
		for t3 := range pivot.outEdges {
			if t3.status == statusAborted {
				continue
			}
			if t == t3 || (t3.status == statusCommitted && t3.commitID > t.snapshotSeq && t3.commitID > pivot.snapshotSeq) {
				e.abortLocked(t, ErrSerialization)
				return true
			}
		}
	}
	return false
}
