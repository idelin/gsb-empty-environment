// Package seqstore provides multi-table reads that are consistent with
// respect to a unified commit sequence. Transactions may span tables and
// are totally ordered by a commit sequence number. Tables apply the
// replicated log independently (and possibly out of order); a query that
// requires a sequence a table has not yet applied is rejected with a
// NeedSeqError telling the caller which sequence to wait for, instead of
// returning a partial result that mixes fragments of a transaction.
package seqstore

import (
	"fmt"
	"sort"
	"sync"
)

// Write is a single key update inside a transaction.
type Write struct {
	Table string
	Key   string
	Value int64
}

// NeedSeqError reports that a table's applied log lags behind the sequence
// a query requires. The caller should wait until the table catches up to
// Required and retry, rather than accept an incomplete result.
type NeedSeqError struct {
	Table    string
	Required uint64
	Applied  uint64
}

func (e *NeedSeqError) Error() string {
	return fmt.Sprintf("table %q has applied up to seq %d, query requires seq %d",
		e.Table, e.Applied, e.Required)
}

type versionedValue struct {
	seq   uint64
	value int64
}

type tableState struct {
	name      string
	versions  map[string][]versionedValue // per key, ordered by apply time
	applied   map[uint64]bool             // commit seqs already applied to this table
	watermark uint64                      // every commit <= watermark is reflected
}

// Engine owns the unified commit log and the per-table materialized state.
type Engine struct {
	mu     sync.Mutex
	tables map[string]*tableState
	log    [][]Write // log[seq-1] holds the writes committed at seq
}

func NewEngine() *Engine {
	return &Engine{tables: make(map[string]*tableState)}
}

func (e *Engine) table(name string) *tableState {
	st, ok := e.tables[name]
	if !ok {
		st = &tableState{name: name, versions: make(map[string][]versionedValue), applied: make(map[uint64]bool)}
		e.tables[name] = st
	}
	return st
}

// Commit assigns the next unified commit sequence to the transaction and
// appends it to the log. The writes become visible to a table only after
// the table applies this sequence.
func (e *Engine) Commit(writes ...Write) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, writes)
	seq := uint64(len(e.log))
	for _, st := range e.tables {
		e.advance(st)
	}
	return seq
}

// Seq returns the latest committed sequence number.
func (e *Engine) Seq() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return uint64(len(e.log))
}

// Apply applies every write of the commit at seq that targets the named
// table. Sequences may be applied in any order; reads are gated by the
// contiguous watermark, so an early-applied future commit stays invisible
// until all preceding commits have been applied.
func (e *Engine) Apply(table string, seq uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seq == 0 || seq > uint64(len(e.log)) {
		return fmt.Errorf("seq %d out of range (log length %d)", seq, len(e.log))
	}
	st := e.table(table)
	if st.applied[seq] {
		return nil
	}
	for _, w := range e.log[seq-1] {
		if w.Table != table {
			continue
		}
		vs := st.versions[w.Key]
		i := sort.Search(len(vs), func(i int) bool { return vs[i].seq > seq })
		vs = append(vs, versionedValue{})
		copy(vs[i+1:], vs[i:])
		vs[i] = versionedValue{seq: seq, value: w.Value}
		st.versions[w.Key] = vs
	}
	st.applied[seq] = true
	e.advance(st)
	return nil
}

// advance raises the watermark past every commit that is either applied or
// does not touch this table.
func (e *Engine) advance(st *tableState) {
	for st.watermark < uint64(len(e.log)) {
		next := st.watermark + 1
		if !st.applied[next] && touches(e.log[next-1], st.name) {
			return
		}
		st.watermark = next
	}
}

func touches(writes []Write, table string) bool {
	for _, w := range writes {
		if w.Table == table {
			return true
		}
	}
	return false
}

// Watermark returns the highest sequence the table can serve: all commits
// up to and including it are reflected in the table's state.
func (e *Engine) Watermark(table string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.table(table).watermark
}

// Snapshot is a consistent multi-table view pinned at one commit sequence.
type Snapshot struct {
	e    *Engine
	seq  uint64
	seen map[string]bool
}

// Query opens a snapshot at seq spanning the given tables. If any table
// has not applied the log up to seq, Query returns a NeedSeqError (for the
// most lagging table) instead of a snapshot that would mix partial
// transactions.
func (e *Engine) Query(seq uint64, tables ...string) (*Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seq > uint64(len(e.log)) {
		return nil, fmt.Errorf("seq %d not committed yet (log length %d)", seq, len(e.log))
	}
	seen := make(map[string]bool, len(tables))
	var lag *NeedSeqError
	for _, name := range tables {
		seen[name] = true
		st := e.table(name)
		if st.watermark < seq && (lag == nil || st.watermark < lag.Applied) {
			lag = &NeedSeqError{Table: name, Required: seq, Applied: st.watermark}
		}
	}
	if lag != nil {
		return nil, lag
	}
	return &Snapshot{e: e, seq: seq, seen: seen}, nil
}

// Get reads a key within the snapshot: the value of the newest version
// committed at or before the snapshot sequence.
func (s *Snapshot) Get(table, key string) (int64, bool) {
	if !s.seen[table] {
		panic(fmt.Sprintf("table %q is not part of this snapshot", table))
	}
	s.e.mu.Lock()
	defer s.e.mu.Unlock()
	return getAt(s.e.table(table), key, s.seq)
}

func getAt(st *tableState, key string, seq uint64) (int64, bool) {
	vs := st.versions[key]
	i := sort.Search(len(vs), func(i int) bool { return vs[i].seq > seq })
	if i == 0 {
		return 0, false
	}
	return vs[i-1].value, true
}
