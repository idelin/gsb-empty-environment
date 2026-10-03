// Package aligner implements a Global Read-View aligner for a sharded
// cluster. Each shard commits with its own local timestamp and emits an
// ordered CDC stream. The aligner coordinates those streams and publishes a
// globally causally-closed safe timestamp T_Safe: external query views are
// blocked while any cross-shard transaction is only partially arrived, so a
// snapshot never shows a debit without its matching credit (money never
// vanishes).
//
// Because each shard's commit timestamps live in an independent clock
// domain, the aligner never truncates individual events by timestamp.
// Visibility is transaction-atomic: a transaction is either wholly inside
// the view or wholly outside it.
package aligner

import (
	"errors"
	"fmt"
	"sync"
)

// Kind is the ledger leg type of a CDC event.
type Kind int

const (
	Debit Kind = iota
	Credit
)

func (k Kind) String() string {
	if k == Debit {
		return "DEBIT"
	}
	return "CREDIT"
}

// Event is one committed change record from a shard's CDC stream.
type Event struct {
	ShardID string // originating shard
	TxnID   string // global transaction id (shared by all legs of a transfer)
	LocalTS int64  // shard-local commit timestamp
	Kind    Kind
	Account string
	Amount  int64 // always positive; direction is given by Kind
}

// Snapshot is an immutable, causally-closed global read view at T_Safe.
type Snapshot struct {
	TS     int64   // T_Safe: the minimum shard watermark at publication
	Events []Event // every leg of every fully-closed transaction
}

// Balanced reports whether every transfer in the snapshot is conserved:
// total debited equals total credited per transaction. A consistent global
// view must always satisfy this.
func (s Snapshot) Balanced() bool {
	delta := make(map[string]int64)
	for _, e := range s.Events {
		if e.Kind == Debit {
			delta[e.TxnID] -= e.Amount
		} else {
			delta[e.TxnID] += e.Amount
		}
	}
	for _, d := range delta {
		if d != 0 {
			return false
		}
	}
	return true
}

// ErrNotClosed is returned by Snapshot while any cross-shard transaction is
// only partially arrived: the read horizon is not yet causally closed.
var ErrNotClosed = errors.New("aligner: read view not yet causally closed; snapshot blocked")

type txnState struct {
	expected map[string]bool // participant shards of the transaction
	arrived  map[string]Event
}

func (t *txnState) closed() bool {
	for shard := range t.expected {
		if _, ok := t.arrived[shard]; !ok {
			return false
		}
	}
	return true
}

// Aligner coordinates per-shard CDC streams into one global read view.
type Aligner struct {
	mu         sync.Mutex
	watermarks map[string]int64 // shard -> highest contiguous local commit ts
	seen       map[string]bool  // shards that have reported at least once
	txns       map[string]*txnState
	events     []Event // every ingested event, for snapshot materialization
}

// NewAligner creates an aligner for the given shard set.
func NewAligner(shardIDs ...string) *Aligner {
	a := &Aligner{
		watermarks: make(map[string]int64),
		seen:       make(map[string]bool),
		txns:       make(map[string]*txnState),
	}
	for _, id := range shardIDs {
		a.watermarks[id] = 0
	}
	return a
}

// RegisterTxn declares a cross-shard transaction and its participant shards.
// It must be called before or together with the first leg's ingestion.
func (a *Aligner) RegisterTxn(txnID string, participants ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.registerTxnLocked(txnID, participants)
}

func (a *Aligner) registerTxnLocked(txnID string, participants []string) {
	st, ok := a.txns[txnID]
	if !ok {
		st = &txnState{expected: make(map[string]bool), arrived: make(map[string]Event)}
		a.txns[txnID] = st
	}
	for _, p := range participants {
		st.expected[p] = true
	}
}

// Ingest consumes one CDC event. Events from a single shard must arrive in
// non-decreasing local-timestamp order (CDC streams are commit-ordered).
func (a *Aligner) Ingest(ev Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.watermarks[ev.ShardID]; !ok {
		return fmt.Errorf("aligner: unknown shard %q", ev.ShardID)
	}
	if ev.LocalTS < a.watermarks[ev.ShardID] {
		return fmt.Errorf("aligner: shard %q ts regression: %d < watermark %d",
			ev.ShardID, ev.LocalTS, a.watermarks[ev.ShardID])
	}
	// Auto-register standalone (single-shard) transactions.
	if _, ok := a.txns[ev.TxnID]; !ok {
		a.registerTxnLocked(ev.TxnID, []string{ev.ShardID})
	}
	st := a.txns[ev.TxnID]
	st.expected[ev.ShardID] = true
	st.arrived[ev.ShardID] = ev
	a.events = append(a.events, ev)
	a.watermarks[ev.ShardID] = ev.LocalTS
	a.seen[ev.ShardID] = true
	return nil
}

// Heartbeat advances a shard's watermark without a data event, modeling the
// periodic "no more commits before ts" marker real CDC streams emit.
func (a *Aligner) Heartbeat(shardID string, ts int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.watermarks[shardID]; !ok {
		return fmt.Errorf("aligner: unknown shard %q", shardID)
	}
	if ts < a.watermarks[shardID] {
		return fmt.Errorf("aligner: shard %q heartbeat regression: %d < watermark %d",
			shardID, ts, a.watermarks[shardID])
	}
	a.watermarks[shardID] = ts
	a.seen[shardID] = true
	return nil
}

// readyLocked reports whether an external view may be generated: every
// shard has reported and no transaction is only partially arrived.
func (a *Aligner) readyLocked() bool {
	for shard := range a.watermarks {
		if !a.seen[shard] {
			return false
		}
	}
	for _, st := range a.txns {
		if len(st.arrived) > 0 && !st.closed() {
			return false
		}
	}
	return true
}

// safeTSLocked is the minimum shard watermark: the horizon up to which
// every shard's stream is known-complete.
func (a *Aligner) safeTSLocked() int64 {
	first := true
	var min int64
	for _, wm := range a.watermarks {
		if first || wm < min {
			min = wm
			first = false
		}
	}
	return min
}

// Ready reports whether an external query view may be generated right now.
func (a *Aligner) Ready() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.readyLocked()
}

// SafeTS returns T_Safe and whether it is currently usable.
func (a *Aligner) SafeTS() (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.readyLocked() {
		return 0, false
	}
	return a.safeTSLocked(), true
}

// Snapshot materializes the consistent global read view. It returns
// ErrNotClosed while any transaction is partially arrived, guaranteeing a
// view never contains a debit without its credit. Visibility is
// transaction-atomic: every leg of every closed transaction is included,
// regardless of how the shards' local timestamp domains compare.
func (a *Aligner) Snapshot() (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.readyLocked() {
		return Snapshot{}, ErrNotClosed
	}
	snap := Snapshot{TS: a.safeTSLocked()}
	for _, ev := range a.events {
		if a.txns[ev.TxnID].closed() {
			snap.Events = append(snap.Events, ev)
		}
	}
	return snap, nil
}
