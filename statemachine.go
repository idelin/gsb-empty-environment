// Package sm implements a multi-partition deterministic transactional
// key-value state machine on top of a local deterministic log.
//
// Properties:
//   - Two-phase prepare/commit events, replayable from the log.
//   - Out-of-order network delivery is absorbed by a bounded reorder
//     buffer keyed by LSN, so every replica applies the same order.
//   - Concurrent transactions are applied via a deterministic topological
//     sort of their conflict dependency graph.
//   - Pending write-write conflicts are decided by a fixed rule and an
//     auditable Evidence record is emitted.
//   - Memory is bounded: pending-txn limit, reorder-window limit, and log
//     compaction at snapshot slice points.
//   - Crash recovery: restore from a snapshot, then replay the log suffix.
package sm

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

var (
	// ErrPendingLimit is returned when the bounded pending-transaction
	// table is full (backpressure instead of unbounded growth).
	ErrPendingLimit = errors.New("sm: pending transaction limit reached")
	// ErrReorderWindowFull is returned when out-of-order packets exceed
	// the bounded reorder window.
	ErrReorderWindowFull = errors.New("sm: reorder window full")
)

// Config controls the bounds of the state machine.
type Config struct {
	NumPartitions int // number of key partitions
	MaxPending    int // max in-flight (prepared/committed-unapplied) txns
	ReorderWindow int // max buffered out-of-order log events
}

func (c Config) withDefaults() Config {
	if c.NumPartitions <= 0 {
		c.NumPartitions = 2
	}
	if c.MaxPending <= 0 {
		c.MaxPending = 1024
	}
	if c.ReorderWindow <= 0 {
		c.ReorderWindow = 4096
	}
	return c
}

type txnState uint8

const (
	txnPrepared txnState = iota + 1
	txnCommitted
)

type txn struct {
	id         string
	writes     map[string]string
	reads      []string
	state      txnState
	prepareLSN uint64
	commitLSN  uint64
}

// less is the deterministic precedence order between committed txns.
func less(a, b *txn) bool {
	if a.commitLSN != b.commitLSN {
		return a.commitLSN < b.commitLSN
	}
	return a.id < b.id
}

// writeReadConflict reports whether a's writes intersect b's write/read set.
func writeReadConflict(a, b *txn) bool {
	for k := range a.writes {
		if _, ok := b.writes[k]; ok {
			return true
		}
		for _, r := range b.reads {
			if r == k {
				return true
			}
		}
	}
	return false
}

// conflicts reports whether the two transactions have a data dependency.
func conflicts(a, b *txn) bool {
	return writeReadConflict(a, b) || writeReadConflict(b, a)
}

// StateMachine is the multi-partition deterministic state machine.
type StateMachine struct {
	cfg        Config
	parts      []map[string]string
	log        []Event // compacted deterministic log (audit/replay source)
	pending    map[string]*txn
	reorderBuf map[uint64]Event
	expectLSN  uint64
	evidence   []Evidence
}

// New creates an empty state machine expecting log LSNs starting at 1.
func New(cfg Config) *StateMachine {
	cfg = cfg.withDefaults()
	parts := make([]map[string]string, cfg.NumPartitions)
	for i := range parts {
		parts[i] = make(map[string]string)
	}
	return &StateMachine{
		cfg:        cfg,
		parts:      parts,
		pending:    make(map[string]*txn),
		reorderBuf: make(map[uint64]Event),
		expectLSN:  1,
	}
}

// PartitionOf maps a key to its partition via a deterministic hash.
func (s *StateMachine) PartitionOf(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() % uint32(len(s.parts)))
}

// Get returns the value of a key and whether it exists.
func (s *StateMachine) Get(key string) (string, bool) {
	v, ok := s.parts[s.PartitionOf(key)][key]
	return v, ok
}

// Deliver feeds one log event into the machine. Events may arrive in any
// order (out-of-order network packets); they are applied strictly in LSN
// order. Re-delivery of an already-applied LSN is a no-op.
func (s *StateMachine) Deliver(e Event) error {
	if e.LSN < s.expectLSN {
		return nil // already applied: idempotent re-delivery
	}
	if _, dup := s.reorderBuf[e.LSN]; dup {
		return fmt.Errorf("sm: duplicate delivery of LSN %d", e.LSN)
	}
	if len(s.reorderBuf) >= s.cfg.ReorderWindow {
		return ErrReorderWindowFull
	}
	s.reorderBuf[e.LSN] = e
	for {
		next, ok := s.reorderBuf[s.expectLSN]
		if !ok {
			return nil
		}
		delete(s.reorderBuf, s.expectLSN)
		if err := s.apply(next); err != nil {
			return err
		}
		s.expectLSN++
	}
}

// Replay delivers a log suffix (used for crash recovery after a snapshot).
func (s *StateMachine) Replay(events []Event) error {
	for _, e := range events {
		if err := s.Deliver(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateMachine) apply(e Event) error {
	s.log = append(s.log, e)
	switch e.Kind {
	case KindPrepare:
		return s.prepare(e)
	case KindCommit:
		return s.commit(e)
	case KindAbort:
		delete(s.pending, e.TxnID)
		s.schedule()
		return nil
	}
	return fmt.Errorf("sm: unknown event kind %d", e.Kind)
}

func (s *StateMachine) prepare(e Event) error {
	if _, ok := s.pending[e.TxnID]; ok {
		return fmt.Errorf("sm: txn %q already pending", e.TxnID)
	}
	if len(s.pending) >= s.cfg.MaxPending {
		return ErrPendingLimit
	}
	s.pending[e.TxnID] = &txn{
		id:         e.TxnID,
		writes:     e.Writes,
		reads:      e.Reads,
		state:      txnPrepared,
		prepareLSN: e.LSN,
	}
	return nil
}

func (s *StateMachine) commit(e Event) error {
	t, ok := s.pending[e.TxnID]
	if !ok {
		return fmt.Errorf("sm: commit for unknown txn %q", e.TxnID)
	}
	if t.state != txnPrepared {
		return fmt.Errorf("sm: txn %q already committed", e.TxnID)
	}
	t.state = txnCommitted
	t.commitLSN = e.LSN
	s.schedule()
	return nil
}

// schedule drains applicable committed transactions in deterministic
// topological order of their conflict dependency graph.
//
// A committed txn is blocked while a conflicting txn is still prepared
// (unresolved/pending conflict): this keeps the whole concurrent group in
// one resolution drain so the tie-break evidence is complete. Edges run
// from lower to higher (commitLSN, txnID), so the graph is a DAG and
// Kahn's algorithm with the smallest-first tie-break is deterministic.
func (s *StateMachine) schedule() {
	appliedCommitLSN := make(map[string]uint64) // txnID -> commitLSN, this drain
	lastWriter := make(map[string]string)       // key -> txnID, this drain
	for {
		t := s.nextReady()
		if t == nil {
			return
		}
		keys := make([]string, 0, len(t.writes))
		for k := range t.writes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if prev, ok := lastWriter[k]; ok && prev != t.id {
				s.evidence = append(s.evidence, Evidence{
					Key:       k,
					Rule:      RuleCommitLSNOrder,
					WinnerTxn: t.id,
					LoserTxn:  prev,
					WinnerLSN: t.commitLSN,
					LoserLSN:  appliedCommitLSN[prev],
					Value:     t.writes[k],
					Reason: fmt.Sprintf(
						"pending conflict on key %q: concurrent txns %s and %s both wrote it; "+
							"%s committed later (commit LSN %d > %d) and wins by deterministic commit-LSN order",
						k, prev, t.id, t.id, t.commitLSN, appliedCommitLSN[prev]),
				})
			}
			s.parts[s.PartitionOf(k)][k] = t.writes[k]
			lastWriter[k] = t.id
		}
		appliedCommitLSN[t.id] = t.commitLSN
		delete(s.pending, t.id)
	}
}

// nextReady picks the smallest committed txn (Kahn's algorithm) that has
// no unapplied conflicting predecessor and no pending prepared conflict.
func (s *StateMachine) nextReady() *txn {
	var ready []*txn
	for _, t := range s.pending {
		if t.state != txnCommitted {
			continue
		}
		blocked := false
		for _, u := range s.pending {
			if u.state == txnPrepared && conflicts(t, u) {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, t)
		}
	}
	if len(ready) == 0 {
		return nil
	}
	sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
	for _, cand := range ready {
		hasPred := false
		for _, other := range ready {
			if other != cand && less(other, cand) && conflicts(other, cand) {
				hasPred = true
				break
			}
		}
		if !hasPred {
			return cand
		}
	}
	return nil // unreachable: edges follow a total order, so the DAG has a source
}

// Evidence returns a copy of the auditable tie-break records emitted so far.
func (s *StateMachine) Evidence() []Evidence {
	out := make([]Evidence, len(s.evidence))
	copy(out, s.evidence)
	return out
}

// Log returns a copy of the (possibly compacted) deterministic log.
func (s *StateMachine) Log() []Event {
	out := make([]Event, len(s.log))
	copy(out, s.log)
	return out
}

// Compact drops log entries below the given LSN. Call it with a snapshot's
// ExpectLSN to bound log memory after taking a snapshot.
func (s *StateMachine) Compact(beforeLSN uint64) {
	i := 0
	for i < len(s.log) && s.log[i].LSN < beforeLSN {
		i++
	}
	s.log = append([]Event(nil), s.log[i:]...)
}

// Pending returns the number of in-flight transactions (for tests/audit).
func (s *StateMachine) Pending() int { return len(s.pending) }

// ExpectLSN returns the next log LSN the machine will apply.
func (s *StateMachine) ExpectLSN() uint64 { return s.expectLSN }

// State renders the full key-value state in a canonical, deterministic
// form (sorted by partition, then key) suitable for equality checks.
func (s *StateMachine) State() string {
	var sb strings.Builder
	for p, part := range s.parts {
		keys := make([]string, 0, len(part))
		for k := range part {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "p%d/%s=%s\n", p, k, part[k])
		}
	}
	return sb.String()
}
