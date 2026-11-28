// Package ssidb is an importable, in-memory MVCC storage engine core that
// implements PostgreSQL-style Serializable Snapshot Isolation (SSI) on top of
// Snapshot Isolation.
//
// The four PostgreSQL SSI mechanisms are represented explicitly:
//
//  1. Non-blocking SIREAD predicate locks at tuple, index-leaf-page and
//     relation granularity, with automatic tuple -> page -> table escalation
//     under per-transaction budgets (conservative superset promotion).
//  2. Directed rw-antidependency edges reader -> writer, created both when a
//     write hits an existing SIREAD lock and when a read observes an invisible
//     newer version / an in-progress write intent.
//  3. Dangerous-structure detection (a pivot with an incoming and an outgoing
//     rw edge; the two may form a 2-cycle). Predicate locks and conflict
//     edges of committed transactions survive commit and are reclaimed only
//     once no transaction that was concurrent with them remains.
//  4. Precise victim selection (never abort a committed transaction; prefer
//     an uncommitted pivot, otherwise the still-active reader) plus a
//     READ ONLY DEFERRABLE / RO-Safe implementation.
package ssidb

import (
	"sort"
	"sync"
)

// Config tunes lock-escalation budgets and the index-page count. Zero values
// are replaced by PostgreSQL-like defaults. Small budgets make escalation
// deterministically testable without millions of keys.
type Config struct {
	// MaxTupleLocks is the number of tuple SIREAD locks one transaction may
	// hold before they coalesce into page locks.
	MaxTupleLocks int
	// MaxPageLocks is the number of page SIREAD locks one transaction may hold
	// before promotion to a relation lock.
	MaxPageLocks int
	// PagesPerTable is the number of index leaf pages (hash buckets).
	PagesPerTable int
}

// DefaultConfig mirrors PostgreSQL's default predicate-lock budgets, scaled
// down for an in-memory engine.
func DefaultConfig() Config {
	return Config{
		MaxTupleLocks: 256,
		MaxPageLocks:  64,
		PagesPerTable: 128,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.MaxTupleLocks <= 0 {
		c.MaxTupleLocks = d.MaxTupleLocks
	}
	if c.MaxPageLocks <= 0 {
		c.MaxPageLocks = d.MaxPageLocks
	}
	if c.PagesPerTable <= 0 {
		c.PagesPerTable = d.PagesPerTable
	}
	return c
}

type txnStatus uint8

const (
	statusActive txnStatus = iota
	statusCommitted
	statusAborted
)

// version is an installed, committed multi-version entry.
type version struct {
	value     string
	deleted   bool
	commitSeq uint64
}

// Txn is an SSI transaction handle. A Txn must not be used after Commit/Rollback.
type Txn struct {
	id         uint64
	engine     *Engine
	readOnly   bool
	deferrable bool
	status     txnStatus
	abortErr   error

	// snapshotSeq is the highest commit sequence visible to this snapshot.
	snapshotSeq uint64

	// end is the monotonic sequence stamped when the transaction finishes.
	end uint64

	// commitID is the sequence stamped at the commit point; it equals the
	// commitSeq carried by every version this transaction installs.
	commitID uint64

	// safe marks a READ ONLY DEFERRABLE transaction that acquired a safe
	// snapshot: it can never belong to a dangerous structure, takes no
	// predicate locks and never needs a retry.
	safe bool

	// writes buffers this transaction's own writes until commit.
	writes map[string]version

	locks predicateLocks

	// in/out adjacency of the rw-antidependency graph, keyed by neighbour.
	inEdges  map[*Txn]struct{}
	outEdges map[*Txn]struct{}
}

// ID returns the monotonic transaction identifier.
func (t *Txn) ID() uint64 { return t.id }

// ReadOnly reports whether the transaction was started as read-only.
func (t *Txn) ReadOnly() bool { return t.readOnly }

// Safe reports whether a read-only transaction holds a RO-Safe snapshot.
func (t *Txn) Safe() bool { return t.safe }

// Engine is the concurrency-control kernel. It is safe for concurrent use.
type Engine struct {
	cfg Config

	mu sync.Mutex
	cv *sync.Cond

	// Monotonic counter shared by transaction ids and end ids: every begin
	// takes an id, every finish takes an end id. Because the counter is
	// shared, t.end < oldestActive.id means t finished before every still
	// active transaction began (no overlap), which is the SSI GC condition.
	next uint64

	// committed versions, newest first per key.
	store map[string][]version

	// intents tracks every active writer of each key. The first entry is the
	// first-updater winner used for write/write conflicts; keeping the full
	// set lets read-side rw dependency tracking observe every active writer.
	intents map[string]map[*Txn]struct{}

	// Reverse SIREAD indexes: which transactions currently lock what.
	tableReaders map[*Txn]struct{}
	pageReaders  map[uint64]map[*Txn]struct{}
	tupleReaders map[string]map[*Txn]struct{}

	// Transactions that remain relevant: active ones plus finished ones still
	// overlapping an active transaction. Edges/locks are retained here.
	txns map[uint64]*Txn
}

// NewEngine creates an SSI kernel with the given configuration.
func NewEngine(cfg Config) *Engine {
	cfg = cfg.withDefaults()
	e := &Engine{
		cfg:          cfg,
		store:        map[string][]version{},
		intents:      map[string]map[*Txn]struct{}{},
		tableReaders: map[*Txn]struct{}{},
		pageReaders:  map[uint64]map[*Txn]struct{}{},
		tupleReaders: map[string]map[*Txn]struct{}{},
		txns:         map[uint64]*Txn{},
	}
	e.cv = sync.NewCond(&e.mu)
	return e
}

// Begin starts a read-write serializable transaction.
func (e *Engine) Begin() *Txn { return e.begin(false, false) }

// BeginReadOnly starts a read-only serializable transaction.
func (e *Engine) BeginReadOnly() *Txn { return e.begin(true, false) }

// BeginReadOnlyDeferrable starts a read-only transaction that blocks until it
// can acquire a safe snapshot, mirroring PostgreSQL's
// `BEGIN READ ONLY DEFERRABLE`. It never takes SIREAD locks and can never be
// aborted with a serialization failure.
func (e *Engine) BeginReadOnlyDeferrable() *Txn {
	return e.begin(true, true)
}

func (e *Engine) begin(readOnly, deferrable bool) *Txn {
	e.mu.Lock()
	defer e.mu.Unlock()

	t := &Txn{
		id:         e.next,
		engine:     e,
		readOnly:   readOnly,
		deferrable: deferrable,
		status:     statusActive,
		locks:      newPredicateLocks(),
		writes:     map[string]version{},
		inEdges:    map[*Txn]struct{}{},
		outEdges:   map[*Txn]struct{}{},
	}
	e.next++
	t.snapshotSeq = e.lastCommitSeqLocked()
	e.txns[t.id] = t

	// A deferrable read-only transaction waits until it is the oldest active
	// transaction (PostgreSQL's SxactGlobalXmin == MyXmin condition): no
	// read-write transaction can then be older, so no rw out-edge can ever be
	// created from this snapshot.
	if deferrable {
		for !e.tryMarkSafeLocked(t) {
			e.cv.Wait()
		}
	}
	return t
}

func (e *Engine) lastCommitSeqLocked() uint64 {
	var max uint64
	for _, versions := range e.store {
		if len(versions) > 0 && versions[0].commitSeq > max {
			max = versions[0].commitSeq
		}
	}
	return max
}

// tryMarkSafeLocked implements the RO-Safe test. It returns true when t is a
// read-only transaction older than every active read-write transaction. Such a
// snapshot cannot observe an invisible write of a concurrent r/w transaction,
// hence it cannot sit on an rw edge.
func (e *Engine) tryMarkSafeLocked(t *Txn) bool {
	if t.safe || !t.readOnly || t.status != statusActive {
		return t.safe
	}
	for _, other := range e.txns {
		if other == t || other.status != statusActive {
			continue
		}
		if !other.readOnly && other.id < t.id {
			return false
		}
	}
	t.safe = true
	// Only a deferrable transaction explicitly waited for this safe point and
	// is allowed to advance its snapshot to everything committed up to now.
	// A non-deferrable read-only transaction must keep its original fixed
	// snapshot for ordinary repeatable-read semantics.
	if t.deferrable {
		t.snapshotSeq = e.lastCommitSeqLocked()
	}
	return true
}

// markPossibleSafeReadersLocked promotes deferrable read-only transactions
// that have become safe after another transaction finished.
func (e *Engine) markPossibleSafeReadersLocked() {
	var candidates []*Txn
	for _, t := range e.txns {
		if t.status == statusActive && t.readOnly && !t.safe {
			candidates = append(candidates, t)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	for _, t := range candidates {
		e.tryMarkSafeLocked(t)
	}
}

// ActiveRWCount reports how many read-write transactions are currently active.
func (e *Engine) ActiveRWCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, t := range e.txns {
		if t.status == statusActive && !t.readOnly {
			n++
		}
	}
	return n
}
