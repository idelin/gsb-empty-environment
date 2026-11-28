package ssidb

import "sort"

// KeyValue is one entry returned by Scan.
type KeyValue struct {
	Key   string
	Value string
}

func (t *Txn) checkUsable() error {
	switch t.status {
	case statusAborted:
		if t.abortErr != nil {
			return t.abortErr
		}
		return ErrAborted
	case statusCommitted:
		return ErrTxnClosed
	}
	return nil
}

// visible reports whether a version committed at commitSeq is in t's snapshot.
func (t *Txn) visible(commitSeq uint64) bool {
	return commitSeq <= t.snapshotSeq
}

// Get returns the value of key as of the transaction snapshot and registers
// the tuple-level SIREAD lock. ok is false when the key is absent/deleted.
func (t *Txn) Get(key string) (value string, ok bool, err error) {
	e := t.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if err = t.checkUsable(); err != nil {
		return "", false, err
	}

	// Read-triggered rw edges and the SIREAD predicate lock must be registered
	// before any short-circuit on the transaction's own buffered write,
	// otherwise concurrent writers would escape dependency tracking.
	e.observeReadLocked(t, key)
	// Read-side rw edges against every active writer are registered
	// unconditionally for non-safe transactions; only SIREAD lock bookkeeping
	// is skipped for safe snapshots.
	for writer := range e.intents[key] {
		if writer != t {
			e.addRWEdgeLocked(t, writer)
		}
	}
	if !t.safe {
		e.addTupleLock(t, key)
	}
	if err = t.checkUsable(); err != nil {
		return "", false, err
	}

	// Own buffered write always takes precedence for the returned value.
	if v, has := t.writes[key]; has {
		if v.deleted {
			return "", false, nil
		}
		return v.value, true, nil
	}

	for _, v := range e.store[key] {
		if t.visible(v.commitSeq) {
			if v.deleted {
				return "", false, nil
			}
			return v.value, true, nil
		}
	}
	return "", false, nil
}

// Put sets key to value under the transaction's write set. It never blocks on
// SIREAD locks: predicate conflicts only add rw-antidependency edges. A
// concurrent write to the same key is a first-updater-wins conflict.
func (t *Txn) Put(key, value string) error {
	return t.write(key, version{value: value})
}

// Delete removes key from the transaction's snapshot.
func (t *Txn) Delete(key string) error {
	return t.write(key, version{deleted: true})
}

func (t *Txn) write(key string, v version) error {
	e := t.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := t.checkUsable(); err != nil {
		return err
	}
	if t.readOnly {
		return ErrReadOnly
	}

	// First-updater-wins: a concurrent active writer owns the key.
	var holder *Txn
	for writer := range e.intents[key] {
		if writer != t {
			holder = writer
			break
		}
	}
	if holder != nil {
		// The losing writer also read (or intends to write) a key that another
		// active transaction wrote: record the rw-antidependency before the
		// write/write abort so graph bookkeeping stays complete.
		e.addRWEdgeLocked(t, holder)
		e.abortLocked(t, ErrWriteWriteConflict)
		return ErrWriteWriteConflict
	}
	// The latest committed version must be in this snapshot.
	if versions := e.store[key]; len(versions) > 0 && !t.visible(versions[0].commitSeq) {
		e.abortLocked(t, ErrWriteWriteConflict)
		return ErrWriteWriteConflict
	}

	// Trigger path (a): the write hits SIREAD locks held by other transactions.
	for _, reader := range e.readerConflict(t, key) {
		e.addRWEdgeLocked(reader, t)
	}
	if err := t.checkUsable(); err != nil {
		return err
	}

	if _, ok := e.intents[key]; !ok {
		e.intents[key] = map[*Txn]struct{}{}
	}
	e.intents[key][t] = struct{}{}
	t.writes[key] = v
	return nil
}

// Scan returns all live keys in [from, to] as of the transaction snapshot.
// Empty from/to mean unbounded. Every visited key gets a tuple SIREAD lock and
// every visited index leaf page gets a page lock, so a later insert into the
// scanned range is detected even when the key did not previously exist.
func (t *Txn) Scan(from, to string) ([]KeyValue, error) {
	e := t.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := t.checkUsable(); err != nil {
		return nil, err
	}

	inRange := func(key string) bool {
		if from != "" && key < from {
			return false
		}
		if to != "" && key > to {
			return false
		}
		return true
	}

	pages := map[uint64]struct{}{}
	touchedAny := false
	values := map[string]string{}
	live := map[string]bool{}

	examine := func(key string) {
		if !inRange(key) {
			return
		}
		touchedAny = true
		e.observeReadLocked(t, key)
		if !t.safe {
			e.addTupleLock(t, key)
			pages[e.pageOf(key)] = struct{}{}
		}
		if v, has := t.writes[key]; has {
			if !v.deleted {
				values[key] = v.value
				live[key] = true
			}
			return
		}
		for _, v := range e.store[key] {
			if t.visible(v.commitSeq) {
				if !v.deleted {
					values[key] = v.value
					live[key] = true
				}
				return
			}
		}
	}

	keys := make([]string, 0, len(e.store))
	for key := range e.store {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		examine(key)
	}

	// Active write intents may target brand-new keys not yet in the store:
	// they must participate in the range predicate too.
	for key, writers := range e.intents {
		if _, seen := e.store[key]; !seen {
			examine(key)
		}
		// Every in-range active writer (including on pre-existing keys) is an
		// invisible write relative to this range snapshot: record the edge.
		for writer := range writers {
			if writer != t && inRange(key) {
				e.addRWEdgeLocked(t, writer)
			}
		}
	}

	if !t.safe {
		if touchedAny {
			for page := range pages {
				e.addPageLock(t, page)
			}
		} else {
			// An empty range cannot name pages conservatively, so take a
			// relation lock (PostgreSQL promotes empty-range scans likewise).
			e.addTableLock(t)
		}
	}
	if err := t.checkUsable(); err != nil {
		return nil, err
	}

	var out []KeyValue
	for key := range live {
		out = append(out, KeyValue{Key: key, Value: values[key]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Commit installs buffered versions and finalizes the transaction. It may
// return ErrSerialization when the final dangerous-structure check aborts the
// transaction instead of committing. Committed transactions are never aborted.
func (t *Txn) Commit() error {
	e := t.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := t.checkUsable(); err != nil {
		return err
	}

	// Defensive late check before granting a commit point.
	if e.precommitDangerLocked(t) {
		e.finishLocked(t)
		return t.abortErr
	}

	if !t.readOnly {
		// Re-check write/write conflicts against commits that landed after the
		// original write, then install every buffered version atomically.
		for key := range t.writes {
			if versions := e.store[key]; len(versions) > 0 && !t.visible(versions[0].commitSeq) {
				e.abortLocked(t, ErrWriteWriteConflict)
				e.finishLocked(t)
				return ErrWriteWriteConflict
			}
		}
		seq := e.next
		e.next++
		t.commitID = seq
		for key, v := range t.writes {
			v.commitSeq = seq
			e.store[key] = append([]version{v}, e.store[key]...)
			if writers := e.intents[key]; writers != nil {
				delete(writers, t)
				if len(writers) == 0 {
					delete(e.intents, key)
				}
			}
		}
		t.snapshotSeq = seq
	}

	t.status = statusCommitted
	e.finishLocked(t)
	return nil
}

// Rollback ends the transaction without installing any version.
func (t *Txn) Rollback() error {
	e := t.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.status == statusCommitted {
		return ErrTxnClosed
	}
	if t.status == statusAborted {
		e.finishLocked(t)
		return nil
	}
	t.abortErr = ErrAborted
	e.abortLocked(t, ErrAborted)
	e.finishLocked(t)
	return nil
}

// finishLocked stamps the end sequence, frees intents of aborted writers,
// promotes now-safe deferrable readers, wakes them, and garbage-collects
// transactions whose whole lifetime predates every remaining active one.
func (e *Engine) finishLocked(t *Txn) {
	t.end = e.next
	e.next++
	for key, writers := range e.intents {
		delete(writers, t)
		if len(writers) == 0 {
			delete(e.intents, key)
		}
	}

	e.markPossibleSafeReadersLocked()
	e.cv.Broadcast()
	e.gcLocked()
}

// gcLocked removes only finished transactions that overlap no active
// transaction. A finished transaction is reclaimed only once its end sequence
// precedes the start id of every remaining active transaction, matching
// PostgreSQL's SxactGlobalXmin rule. Everything still overlapping stays --
// including the SIREAD locks and rw edges of committed transactions -- so a
// dangerous structure exposed late by a concurrent transaction cannot be
// missed.
func (e *Engine) gcLocked() {
	var oldestActiveID uint64
	hasActive := false
	for _, other := range e.txns {
		if other.status == statusActive {
			if !hasActive || other.id < oldestActiveID {
				oldestActiveID = other.id
				hasActive = true
			}
		}
	}
	if !hasActive {
		for _, other := range e.txns {
			e.forgetLocked(other)
		}
		e.txns = map[uint64]*Txn{}
		return
	}
	for id, other := range e.txns {
		if other.status != statusActive && other.end <= oldestActiveID {
			e.forgetLocked(other)
			delete(e.txns, id)
		}
	}
}

// forgetLocked detaches t from the graph and from all SIREAD indexes.
func (e *Engine) forgetLocked(t *Txn) {
	for neighbour := range t.inEdges {
		delete(neighbour.outEdges, t)
	}
	for neighbour := range t.outEdges {
		delete(neighbour.inEdges, t)
	}
	t.inEdges = map[*Txn]struct{}{}
	t.outEdges = map[*Txn]struct{}{}
	e.releaseLocks(t)
}

// observeReadLocked registers rw edges for invisible newer versions of key and
// for an active write intent: trigger path (b), a reader discovering writes it
// cannot see.
func (e *Engine) observeReadLocked(reader *Txn, key string) {
	if reader.safe {
		return
	}
	for _, v := range e.store[key] {
		if reader.visible(v.commitSeq) {
			break // older entries are visible too
		}
		if writer := e.txnByCommitSeqLocked(v.commitSeq); writer != nil {
			e.addRWEdgeLocked(reader, writer)
		}
	}
	// An in-progress write is invisible to the snapshot and must produce an
	// rw edge unconditionally: this is the read-side trigger that complements
	// the write-side SIREAD hit, including when the reader already holds a
	// tuple predicate on the same key (write-skew on that very key).
	for writer := range e.intents[key] {
		e.addRWEdgeLocked(reader, writer)
	}
}

func (e *Engine) txnByCommitSeqLocked(commitSeq uint64) *Txn {
	for _, t := range e.txns {
		if t.status == statusCommitted && t.commitID == commitSeq {
			return t
		}
	}
	return nil
}
