// Package ssi implements Serializable Snapshot Isolation (SSI) on top of
// an MVCC key-value store.
//
// Isolation is built in two layers:
//
//  1. Snapshot Isolation: every transaction reads from a consistent snapshot
//     taken at Begin, and write-write conflicts are resolved by
//     first-committer-wins.
//  2. SSI (Cahill et al., SIGMOD 2008, as used by PostgreSQL): reads place
//     SIREAD locks that are kept after the reader commits. Whenever a
//     transaction T_r has read a key that a concurrent transaction T_w
//     overwrites, an rw-antidependency edge T_r ->rw-> T_w is recorded.
//     A cycle in the serialization graph always contains a "dangerous
//     structure" T1 ->rw-> T2 ->rw-> T3 where T3 commits first. The store
//     detects such structures and aborts a transaction (the pivot T2 while
//     it is still active, otherwise the committer) before the anomaly
//     (e.g. write skew) can be committed.
package ssi

import (
	"errors"
	"fmt"
	"sync"
)

// ErrSerializationFailure is returned to a transaction that was aborted
// because it sits on a dangerous rw-antidependency structure. The
// transaction may be retried.
var ErrSerializationFailure = errors.New("ssi: could not serialize access due to read/write dependencies among transactions")

// ErrWriteConflict is returned when two concurrent transactions write the
// same key (first committer wins).
var ErrWriteConflict = errors.New("ssi: could not serialize access due to concurrent update")

// ErrTxnNotActive is returned when an operation is attempted on a
// transaction that already committed.
var ErrTxnNotActive = errors.New("ssi: transaction is no longer active")

type txnState int

const (
	stateActive txnState = iota
	stateCommitted
	stateAborted
)

// version is one MVCC version of a key.
type version struct {
	value   int64
	beginTS uint64 // commit timestamp of the creating transaction
	endTS   uint64 // commit timestamp of the overwriting transaction; 0 = +inf
	writer  *Txn
}

// Store is an MVCC key-value store providing serializable transactions.
type Store struct {
	mu       sync.Mutex
	clock    uint64
	nextID   uint64
	versions map[string][]*version // newest version first
	siread   map[string]map[*Txn]struct{}
	pending  map[string]map[*Txn]struct{} // uncommitted writes per key
	active   map[*Txn]struct{}
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{
		versions: make(map[string][]*version),
		siread:   make(map[string]map[*Txn]struct{}),
		pending:  make(map[string]map[*Txn]struct{}),
		active:   make(map[*Txn]struct{}),
	}
}

// Set initialises a key outside of any transaction (timestamp 0).
func (s *Store) Set(key string, value int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[key] = append([]*version{{value: value}}, s.versions[key]...)
}

// Txn is a single snapshot-isolation transaction.
type Txn struct {
	store    *Store
	id       uint64
	startTS  uint64
	commitTS uint64
	state    txnState
	rwIn     map[*Txn]struct{} // X ->rw-> me
	rwOut    map[*Txn]struct{} // me ->rw-> X
	writes   map[string]int64
}

// Begin starts a new transaction with a snapshot of the store.
func (s *Store) Begin() *Txn {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock++
	s.nextID++
	t := &Txn{
		store:   s,
		id:      s.nextID,
		startTS: s.clock,
		state:   stateActive,
		rwIn:    make(map[*Txn]struct{}),
		rwOut:   make(map[*Txn]struct{}),
		writes:  make(map[string]int64),
	}
	s.active[t] = struct{}{}
	return t
}

// ID returns the transaction identifier.
func (t *Txn) ID() uint64 { return t.id }

// Get reads key from the transaction's snapshot, placing a SIREAD lock.
func (t *Txn) Get(key string) (int64, bool, error) {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := t.checkActiveLocked(); err != nil {
		return 0, false, err
	}
	if v, ok := t.writes[key]; ok { // read your own writes
		return v, true, nil
	}
	s.sireadLockLocked(key, t)
	// A concurrent transaction has an uncommitted write on this key:
	// we read an older version, so record T ->rw-> writer.
	for w := range s.pending[key] {
		if w != t {
			s.addEdgeLocked(t, w)
		}
	}
	vers := s.versions[key]
	for _, v := range vers {
		if v.beginTS <= t.startTS && (v.endTS == 0 || v.endTS > t.startTS) {
			// If a concurrent transaction committed a newer version of
			// this key after our snapshot, record T ->rw-> writer.
			if newest := vers[0]; newest.writer != nil && newest.writer != t &&
				newest.writer.commitTS > t.startTS {
				s.addEdgeLocked(t, newest.writer)
			}
			return v.value, true, nil
		}
	}
	return 0, false, nil
}

// Put buffers a write of key; it becomes visible at Commit.
func (t *Txn) Put(key string, value int64) error {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := t.checkActiveLocked(); err != nil {
		return err
	}
	// Any (possibly already committed) overlapping transaction that read
	// this key gets an rw-antidependency edge to us: reader ->rw-> T.
	for r := range s.siread[key] {
		if r != t && (r.state == stateActive || r.commitTS > t.startTS) {
			s.addEdgeLocked(r, t)
		}
	}
	m := s.pending[key]
	if m == nil {
		m = make(map[*Txn]struct{})
		s.pending[key] = m
	}
	m[t] = struct{}{}
	t.writes[key] = value
	return nil
}

// Commit validates the transaction and, if safe, makes its writes visible.
func (t *Txn) Commit() error {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	switch t.state {
	case stateAborted:
		return ErrSerializationFailure
	case stateCommitted:
		return ErrTxnNotActive
	}

	// First-committer-wins for write-write conflicts.
	for key := range t.writes {
		if vers := s.versions[key]; len(vers) > 0 {
			if newest := vers[0]; newest.writer != nil && newest.writer != t &&
				newest.writer.state == stateCommitted && newest.beginTS > t.startTS {
				s.abortLocked(t)
				return ErrWriteConflict
			}
		}
	}

	// Dangerous-structure check with t as T3 (the first committer):
	// T1 ->rw-> T2 ->rw-> t. Abort the pivot T2 if it is still active;
	// if the pivot already committed, t must abort instead. A pivot that
	// write-write conflicts with t is left to first-committer-wins.
	for t2 := range t.rwIn {
		if t2.state == stateAborted || len(t2.rwIn) == 0 || sharesWrites(t, t2) {
			continue
		}
		if t2.state == stateActive {
			s.abortLocked(t2)
		} else { // pivot committed first: abort ourselves
			s.abortLocked(t)
			return ErrSerializationFailure
		}
	}
	// t as pivot T2 with an already committed T3: T1 ->rw-> t ->rw-> T3.
	if len(t.rwIn) > 0 {
		for t3 := range t.rwOut {
			if t3.state == stateCommitted && !sharesWrites(t, t3) {
				s.abortLocked(t)
				return ErrSerializationFailure
			}
		}
	}

	s.clock++
	t.commitTS = s.clock
	t.state = stateCommitted
	delete(s.active, t)
	for key, value := range t.writes {
		if vers := s.versions[key]; len(vers) > 0 {
			vers[0].endTS = t.commitTS
		}
		s.versions[key] = append([]*version{{
			value:   value,
			beginTS: t.commitTS,
			writer:  t,
		}}, s.versions[key]...)
	}
	s.clearPendingLocked(t)
	s.gcSireadLocked()
	return nil
}

// Rollback aborts the transaction.
func (t *Txn) Rollback() {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.state == stateActive {
		s.abortLocked(t)
	}
}

// String describes the transaction state.
func (t *Txn) String() string {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	var st string
	switch t.state {
	case stateActive:
		st = "active"
	case stateCommitted:
		st = "committed"
	case stateAborted:
		st = "aborted"
	}
	return fmt.Sprintf("T%d(start=%d commit=%d %s)", t.id, t.startTS, t.commitTS, st)
}

// Snapshot returns the newest committed value of key (used by tests and
// the demo to inspect final state).
func (s *Store) Snapshot(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vers := s.versions[key]
	if len(vers) == 0 {
		return 0, false
	}
	return vers[0].value, true
}

// sharesWrites reports whether two transactions write a common key.
func sharesWrites(a, b *Txn) bool {
	for key := range a.writes {
		if _, ok := b.writes[key]; ok {
			return true
		}
	}
	return false
}

func (t *Txn) checkActiveLocked() error {
	switch t.state {
	case stateAborted:
		return ErrSerializationFailure
	case stateCommitted:
		return ErrTxnNotActive
	}
	return nil
}

func (s *Store) abortLocked(t *Txn) {
	if t.state != stateActive {
		return
	}
	t.state = stateAborted
	delete(s.active, t)
	s.releaseSireadLocked(t)
	s.clearPendingLocked(t)
}

func (s *Store) sireadLockLocked(key string, t *Txn) {
	m := s.siread[key]
	if m == nil {
		m = make(map[*Txn]struct{})
		s.siread[key] = m
	}
	m[t] = struct{}{}
}

func (s *Store) releaseSireadLocked(t *Txn) {
	for key, m := range s.siread {
		if _, ok := m[t]; ok {
			delete(m, t)
			if len(m) == 0 {
				delete(s.siread, key)
			}
		}
	}
}

func (s *Store) clearPendingLocked(t *Txn) {
	for key, m := range s.pending {
		if _, ok := m[t]; ok {
			delete(m, t)
			if len(m) == 0 {
				delete(s.pending, key)
			}
		}
	}
}

// gcSireadLocked releases SIREAD locks of committed transactions once no
// active transaction could still overlap them.
func (s *Store) gcSireadLocked() {
	for _, m := range s.siread {
		for r := range m {
			if r.state != stateCommitted {
				continue
			}
			needed := false
			for a := range s.active {
				if a.startTS < r.commitTS {
					needed = true
					break
				}
			}
			if !needed {
				s.releaseSireadLocked(r)
			}
		}
	}
}

// addEdgeLocked records an rw-antidependency from ->rw-> to and resolves
// any dangerous structure it completes whose T3 has already committed.
func (s *Store) addEdgeLocked(from, to *Txn) {
	if from == to {
		return
	}
	from.rwOut[to] = struct{}{}
	to.rwIn[from] = struct{}{}
	// Structure from ->rw-> to ->rw-> T3 with T3 committed.
	for t3 := range to.rwOut {
		if t3.state == stateCommitted {
			s.resolveDangerLocked(from, to)
		}
	}
	// Structure T1 ->rw-> from ->rw-> to with to committed.
	if to.state == stateCommitted && len(from.rwIn) > 0 {
		s.resolveDangerLocked(nil, from)
	}
}

// resolveDangerLocked breaks a dangerous structure T1 ->rw-> T2 ->rw-> T3
// where T3 is committed: abort the pivot T2 while it is active, otherwise
// abort T1 (which is necessarily the transaction currently executing).
func (s *Store) resolveDangerLocked(t1, t2 *Txn) {
	if t2.state == stateActive {
		s.abortLocked(t2)
		return
	}
	if t1 != nil && t1.state == stateActive {
		s.abortLocked(t1)
		return
	}
	// Both already committed cannot happen: SIREAD locks are retained
	// until no overlapping transaction is active, so the structure is
	// always detected while at least one member can still be aborted.
	for t1c := range t2.rwIn {
		if t1c.state == stateActive {
			s.abortLocked(t1c)
			return
		}
	}
}
