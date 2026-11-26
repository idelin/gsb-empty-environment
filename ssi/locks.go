package ssi

// keyRange is an inclusive range of keys. Empty start/end are unbounded.
type keyRange struct {
	start string
	end   string
}

func (r keyRange) contains(key string) bool {
	if r.start != "" && key < r.start {
		return false
	}
	if r.end != "" && key > r.end {
		return false
	}
	return true
}

// acquireRowLock records a row-level SIREAD lock, escalating to a table
// lock once the transaction holds more than the threshold row locks on the
// same table.
func (m *Manager) acquireRowLock(t *Txn, table, key string) {
	if _, tableLocked := t.tableLocks[table]; tableLocked {
		return
	}

	if t.rowLocks[table] == nil {
		t.rowLocks[table] = make(map[string]struct{})
	}
	if _, exists := t.rowLocks[table][key]; !exists {
		t.rowLocks[table][key] = struct{}{}
	}

	if len(t.rowLocks[table]) > m.escalationThreshold {
		m.escalateToTableLock(t, table)
		return
	}

	if m.rowLocks[table] == nil {
		m.rowLocks[table] = make(map[string]map[TxnID]struct{})
	}
	if m.rowLocks[table][key] == nil {
		m.rowLocks[table][key] = make(map[TxnID]struct{})
	}
	m.rowLocks[table][key][t.id] = struct{}{}
}

func (m *Manager) escalateToTableLock(t *Txn, table string) {
	for key := range t.rowLocks[table] {
		delete(m.rowLocks[table][key], t.id)
		if len(m.rowLocks[table][key]) == 0 {
			delete(m.rowLocks[table], key)
		}
	}
	delete(t.rowLocks, table)

	for _, r := range t.rangeLocks[table] {
		if holders := m.rangeLocks[table][r]; holders != nil {
			delete(holders, t.id)
			if len(holders) == 0 {
				delete(m.rangeLocks[table], r)
			}
		}
	}
	delete(t.rangeLocks, table)

	t.tableLocks[table] = struct{}{}
	if m.tableLocks[table] == nil {
		m.tableLocks[table] = make(map[TxnID]struct{})
	}
	m.tableLocks[table][t.id] = struct{}{}
}

// acquireRangeLock records an index-range SIREAD lock. It is absorbed by an
// existing range lock on the same range or a table lock held by the
// transaction.
func (m *Manager) acquireRangeLock(t *Txn, table string, r keyRange) {
	if _, tableLocked := t.tableLocks[table]; tableLocked {
		return
	}
	for _, existing := range t.rangeLocks[table] {
		if existing == r {
			return
		}
	}
	t.rangeLocks[table] = append(t.rangeLocks[table], r)

	if m.rangeLocks[table] == nil {
		m.rangeLocks[table] = make(map[keyRange]map[TxnID]struct{})
	}
	if m.rangeLocks[table][r] == nil {
		m.rangeLocks[table][r] = make(map[TxnID]struct{})
	}
	m.rangeLocks[table][r][t.id] = struct{}{}
}

// addReadWriteConflicts records edges reader -rw-> writer for every
// transaction that wrote the key but whose write is not visible to reader.
func (m *Manager) addReadWriteConflicts(reader *Txn, table, key string) {
	for writerID := range m.writerByKey[table][key] {
		writer := m.txns[writerID]
		if writer.status != StatusAborted && !m.writeVisible(reader, writer) {
			m.addEdge(reader.id, writerID)
		}
	}
}

func (m *Manager) addRangeReadWriteConflicts(reader *Txn, table string, r keyRange) {
	for key, writers := range m.writerByKey[table] {
		if !r.contains(key) {
			continue
		}
		for writerID := range writers {
			writer := m.txns[writerID]
			if writer.status != StatusAborted && !m.writeVisible(reader, writer) {
				m.addEdge(reader.id, writerID)
			}
		}
	}
}

// addWriteConflicts inspects SIREAD locks held by other transactions when
// writer writes a key, creating reader -rw-> writer edges.
func (m *Manager) addWriteConflicts(writer *Txn, table, key string) {
	for readerID := range m.tableLocks[table] {
		m.edgeForHeldLock(writer, readerID)
	}
	for r, holders := range m.rangeLocks[table] {
		if !r.contains(key) {
			continue
		}
		for readerID := range holders {
			m.edgeForHeldLock(writer, readerID)
		}
	}
	for readerID := range m.rowLocks[table][key] {
		m.edgeForHeldLock(writer, readerID)
	}
}

func (m *Manager) edgeForHeldLock(writer *Txn, readerID TxnID) {
	if readerID == writer.id {
		return
	}
	reader := m.txns[readerID]
	if reader.status == StatusAborted || reader.status == StatusCommitted {
		// A committed reader keeps its SIREAD lock specifically so that a
		// later write still produces the rw edge it needs.
		if reader.status == StatusCommitted {
			m.addEdge(readerID, writer.id)
		}
		return
	}
	m.addEdge(readerID, writer.id)
}

// writeVisible reports whether writer's writes are part of reader's
// snapshot. A write is visible only when the writer committed before the
// reader began.
func (m *Manager) writeVisible(reader, writer *Txn) bool {
	if writer.status != StatusCommitted {
		return false
	}
	return writer.commitSeq < reader.beginSeq
}

func (m *Manager) recordWrite(t *Txn, table, key string) {
	if m.writerByKey[table] == nil {
		m.writerByKey[table] = make(map[string]map[TxnID]struct{})
	}
	if m.writerByKey[table][key] == nil {
		m.writerByKey[table][key] = make(map[TxnID]struct{})
	}
	m.writerByKey[table][key][t.id] = struct{}{}
}

// addEdge records the directed rw-antidependency from -> to.
func (m *Manager) addEdge(from, to TxnID) {
	if from == to {
		return
	}
	src := m.txns[from]
	dst := m.txns[to]
	if src == nil || dst == nil ||
		src.status == StatusAborted || dst.status == StatusAborted ||
		dst.status == StatusCommitted {
		return
	}
	if _, exists := src.out[to]; exists {
		return
	}
	src.out[to] = struct{}{}
	dst.in[from] = struct{}{}
}

// detectDangerous enumerates two-hop paths t1 -rw-> t2 -rw-> t3. The
// structure is dangerous when t1 == t3 (two-transaction cycle) or t3 has
// committed while t1 has not yet committed (t3 is earlier in commit order).
// The pivot t2 is the preferred abort victim; the current committer is
// used when the pivot already committed.
func (m *Manager) detectDangerous(current *Txn) *Txn {
	var victim *Txn
	for _, t1 := range m.txns {
		if t1.status == StatusAborted || t1.safeSnapshot {
			continue
		}
		for t2ID := range t1.out {
			t2 := m.txns[t2ID]
			if t2.status == StatusAborted || t2.safeSnapshot {
				continue
			}
			for t3ID := range t2.out {
				t3 := m.txns[t3ID]
				if t3.status == StatusAborted || t3.safeSnapshot {
					continue
				}
				if !m.isDangerous(t1, t2, t3) {
					continue
				}
				victim = m.chooseVictim(t2, current, t1, t3)
				if victim != nil {
					return victim
				}
			}
		}
	}
	return nil
}

func (m *Manager) isDangerous(t1, t2, t3 *Txn) bool {
	if t1.id == t3.id {
		return true
	}
	if t3.status == StatusCommitted && t1.status != StatusCommitted {
		return true
	}
	if t3.status == StatusCommitted && t1.status == StatusCommitted &&
		t3.commitSeq < t1.commitSeq {
		return true
	}
	return false
}

func (m *Manager) chooseVictim(pivot, current, t1, t3 *Txn) *Txn {
	if pivot.status != StatusCommitted && pivot.status != StatusAborted {
		return pivot
	}
	// Pivot already committed: abort the still-active start of the path
	// (T1), then fall back to the current committer.
	if t1.status != StatusCommitted && t1.status != StatusAborted &&
		!t1.safeSnapshot {
		return t1
	}
	if current != nil && current.status != StatusCommitted &&
		current.status != StatusAborted && !current.safeSnapshot {
		return current
	}
	if t3.status != StatusCommitted && t3.status != StatusAborted &&
		!t3.safeSnapshot {
		return t3
	}
	return nil
}

// cleanupAborted marks the transaction aborted and removes every edge and
// lock it owns so it cannot block subsequent commits.
func (m *Manager) cleanupAborted(t *Txn, cause error) {
	t.status = StatusAborted
	t.abortCause = cause
	delete(m.active, t.id)

	for otherID := range t.out {
		delete(m.txns[otherID].in, t.id)
	}
	for otherID := range t.in {
		delete(m.txns[otherID].out, t.id)
	}
	t.out = make(map[TxnID]struct{})
	t.in = make(map[TxnID]struct{})

	m.releaseLocks(t)

	m.releaseWriteRecords(t)
	t.writes = make(map[string]map[string]string)
}

func (m *Manager) releaseWriteRecords(t *Txn) {
	for table, keys := range m.writerByKey {
		for key := range keys {
			delete(m.writerByKey[table][key], t.id)
			if len(m.writerByKey[table][key]) == 0 {
				delete(m.writerByKey[table], key)
			}
		}
		if len(m.writerByKey[table]) == 0 {
			delete(m.writerByKey, table)
		}
	}
}

func (m *Manager) releaseLocks(t *Txn) {
	for table, keys := range t.rowLocks {
		for key := range keys {
			delete(m.rowLocks[table][key], t.id)
			if len(m.rowLocks[table][key]) == 0 {
				delete(m.rowLocks[table], key)
			}
		}
		if len(m.rowLocks[table]) == 0 {
			delete(m.rowLocks, table)
		}
	}

	for table, ranges := range t.rangeLocks {
		for _, r := range ranges {
			delete(m.rangeLocks[table], r)
		}
		if len(m.rangeLocks[table]) == 0 {
			delete(m.rangeLocks, table)
		}
	}

	for table := range t.tableLocks {
		delete(m.tableLocks[table], t.id)
		if len(m.tableLocks[table]) == 0 {
			delete(m.tableLocks, table)
		}
	}

	t.rowLocks = make(map[string]map[string]struct{})
	t.rangeLocks = make(map[string][]keyRange)
	t.tableLocks = make(map[string]struct{})
}

// vacuum releases retained SIREAD locks and write records of committed
// transactions once no concurrent active transaction remains, and prunes
// superseded multi-version entries.
func (m *Manager) vacuum() {
	// Committed SIREAD locks must survive as long as any transaction that
	// could still act on them is in flight. Retention is global: while an
	// active transaction exists, every committed lock stays installed.
	if len(m.active) > 0 {
		return
	}
	for _, t := range m.txns {
		if t.status != StatusCommitted {
			continue
		}
		m.releaseLocks(t)
		m.releaseWriteRecords(t)
	}

	if len(m.active) == 0 {
		for table, keys := range m.versions {
			for key, vers := range keys {
				if len(vers) <= 1 {
					continue
				}
				m.versions[table][key] = vers[len(vers)-1:]
			}
		}
	}
}
