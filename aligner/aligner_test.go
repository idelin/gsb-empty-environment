package aligner

import (
	"fmt"
	"testing"
)

func mustIngest(t *testing.T, a *Aligner, ev Event) {
	t.Helper()
	if err := a.Ingest(ev); err != nil {
		t.Fatalf("Ingest(%+v) failed: %v", ev, err)
	}
}

// Acceptance boundary: a transfer between shard1 and shard2 must block any
// external view until BOTH the debit and the credit leg have arrived.
func TestTransferBlocksViewUntilClosed(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	a.RegisterTxn("txn-transfer-1", "shard1", "shard2")

	// Debit leg commits on shard1 at its local ts 10.
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "txn-transfer-1", LocalTS: 10, Kind: Debit, Account: "alice", Amount: 100})
	// shard1 keeps committing unrelated stuff; shard2 heartbeats far along
	// its own clock, but the credit leg itself is still in flight.
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "txn-local-1", LocalTS: 11, Kind: Debit, Account: "carol", Amount: 5})
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "txn-local-1", LocalTS: 12, Kind: Credit, Account: "dave", Amount: 5})
	if err := a.Heartbeat("shard2", 50); err != nil {
		t.Fatal(err)
	}

	if a.Ready() {
		t.Fatal("view must not be ready while the credit leg is missing")
	}
	if _, ok := a.SafeTS(); ok {
		t.Fatal("SafeTS must not be usable while a transfer is open")
	}
	if _, err := a.Snapshot(); err != ErrNotClosed {
		t.Fatalf("expected ErrNotClosed, got %v", err)
	}

	// Credit leg finally arrives on shard2 (commit ts 55 > heartbeat 50).
	mustIngest(t, a, Event{ShardID: "shard2", TxnID: "txn-transfer-1", LocalTS: 55, Kind: Credit, Account: "bob", Amount: 100})

	if !a.Ready() {
		t.Fatal("view must be ready once both legs arrived")
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if !snap.Balanced() {
		t.Fatal("snapshot is not balanced: money appeared or vanished")
	}
	var debit, credit bool
	for _, e := range snap.Events {
		if e.TxnID == "txn-transfer-1" {
			if e.Kind == Debit {
				debit = true
			} else {
				credit = true
			}
		}
	}
	if !debit || !credit {
		t.Fatalf("snapshot must contain both legs, debit=%v credit=%v", debit, credit)
	}
}

// Money must never vanish: at no point may a snapshot contain the debit
// without the credit, no matter how far the other shard's watermark runs.
func TestSnapshotNeverContainsPartialTransfer(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	a.RegisterTxn("txn-x", "shard1", "shard2")

	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "txn-x", LocalTS: 3, Kind: Debit, Account: "alice", Amount: 42})
	for ts := int64(4); ts <= 20; ts++ {
		if err := a.Heartbeat("shard2", ts); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Snapshot(); err != ErrNotClosed {
			t.Fatalf("at shard2 watermark %d: expected ErrNotClosed, got %v", ts, err)
		}
	}
	mustIngest(t, a, Event{ShardID: "shard2", TxnID: "txn-x", LocalTS: 21, Kind: Credit, Account: "bob", Amount: 42})
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Balanced() {
		t.Fatal("unbalanced snapshot")
	}
	// T_Safe is the minimum shard watermark: shard1 is still at 3.
	if snap.TS != 3 {
		t.Fatalf("expected T_Safe=3, got %d", snap.TS)
	}
	// Both legs are in the view even though shard2's leg carries a larger
	// local ts: visibility is transaction-atomic across clock domains.
	if len(snap.Events) != 2 {
		t.Fatalf("expected both legs in snapshot, got %d events", len(snap.Events))
	}
}

// Multiple concurrent transfers with interleaved cross-shard arrival: the
// view stays blocked until the last open transfer closes.
func TestMultipleTransfersInterleaved(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	a.RegisterTxn("t1", "shard1", "shard2")
	a.RegisterTxn("t2", "shard1", "shard2")

	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "t1", LocalTS: 10, Kind: Debit, Account: "a", Amount: 10})
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "t2", LocalTS: 11, Kind: Debit, Account: "c", Amount: 20})
	mustIngest(t, a, Event{ShardID: "shard2", TxnID: "t1", LocalTS: 5, Kind: Credit, Account: "b", Amount: 10})

	// t1 closed, t2 still open: the whole view stays blocked.
	if a.Ready() {
		t.Fatal("t2 is open: view must stay blocked")
	}
	if _, err := a.Snapshot(); err != ErrNotClosed {
		t.Fatalf("expected ErrNotClosed, got %v", err)
	}

	mustIngest(t, a, Event{ShardID: "shard2", TxnID: "t2", LocalTS: 6, Kind: Credit, Account: "d", Amount: 20})
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Balanced() {
		t.Fatal("unbalanced snapshot")
	}
	if len(snap.Events) != 4 {
		t.Fatalf("expected 4 events in snapshot, got %d", len(snap.Events))
	}
}

// A transaction that is registered but has not produced any leg yet casts
// no shadow: it cannot appear in any view, so it must not block one.
func TestUnstartedTxnDoesNotBlock(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	a.RegisterTxn("future", "shard1", "shard2")

	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "local", LocalTS: 5, Kind: Debit, Account: "a", Amount: 1})
	if err := a.Heartbeat("shard2", 5); err != nil {
		t.Fatal(err)
	}
	if !a.Ready() {
		t.Fatal("a txn with zero arrived legs must not block the view")
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.TS != 5 {
		t.Fatalf("expected T_Safe=5, got %d", snap.TS)
	}
	for _, e := range snap.Events {
		if e.TxnID == "future" {
			t.Fatal("unstarted txn leaked into snapshot")
		}
	}
}

// Once a transfer's first leg lands, the view is blocked until the
// counterpart arrives — even if the first leg sits far above the current
// watermark floor.
func TestPartialTxnBlocksRegardlessOfPosition(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	a.RegisterTxn("big", "shard1", "shard2")

	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "local", LocalTS: 5, Kind: Debit, Account: "a", Amount: 1})
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "local", LocalTS: 6, Kind: Credit, Account: "b", Amount: 1})
	if err := a.Heartbeat("shard2", 5); err != nil {
		t.Fatal(err)
	}
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "big", LocalTS: 100, Kind: Debit, Account: "x", Amount: 9})
	if a.Ready() {
		t.Fatal("partial transfer must block the view")
	}
	if _, err := a.Snapshot(); err != ErrNotClosed {
		t.Fatalf("expected ErrNotClosed, got %v", err)
	}
	mustIngest(t, a, Event{ShardID: "shard2", TxnID: "big", LocalTS: 6, Kind: Credit, Account: "y", Amount: 9})
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Balanced() {
		t.Fatal("unbalanced snapshot")
	}
}

// View stays blocked until every shard has reported at least once.
func TestBlockedUntilAllShardsReport(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "l", LocalTS: 1, Kind: Debit, Account: "a", Amount: 1})
	if a.Ready() {
		t.Fatal("shard2 never reported: must not be ready")
	}
	if _, err := a.Snapshot(); err != ErrNotClosed {
		t.Fatalf("expected ErrNotClosed, got %v", err)
	}
	if err := a.Heartbeat("shard2", 1); err != nil {
		t.Fatal(err)
	}
	if !a.Ready() {
		t.Fatal("all shards reported, no open txns: must be ready")
	}
}

// CDC streams are commit-ordered per shard; regressions are rejected.
func TestTSRegressionRejected(t *testing.T) {
	a := NewAligner("shard1")
	mustIngest(t, a, Event{ShardID: "shard1", TxnID: "a", LocalTS: 10, Kind: Debit, Account: "a", Amount: 1})
	if err := a.Ingest(Event{ShardID: "shard1", TxnID: "b", LocalTS: 9, Kind: Credit, Account: "b", Amount: 1}); err == nil {
		t.Fatal("expected ts regression error")
	}
	if err := a.Heartbeat("shard1", 5); err == nil {
		t.Fatal("expected heartbeat regression error")
	}
	if err := a.Ingest(Event{ShardID: "nope", TxnID: "c", LocalTS: 1}); err == nil {
		t.Fatal("expected unknown shard error")
	}
}

// Conservation invariant across a sustained workload: every snapshot that
// the aligner publishes is balanced.
func TestConservationAcrossWorkload(t *testing.T) {
	a := NewAligner("shard1", "shard2")
	check := func() {
		snap, err := a.Snapshot()
		if err == ErrNotClosed {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if !snap.Balanced() {
			t.Fatalf("unbalanced snapshot at T_Safe=%d: %+v", snap.TS, snap.Events)
		}
	}
	for i := 1; i <= 50; i++ {
		txnID := fmt.Sprintf("txn-%d", i)
		a.RegisterTxn(txnID, "shard1", "shard2")
		mustIngest(t, a, Event{ShardID: "shard1", TxnID: txnID, LocalTS: int64(i), Kind: Debit, Account: "src", Amount: int64(i)})
		check()
		mustIngest(t, a, Event{ShardID: "shard2", TxnID: txnID, LocalTS: int64(i), Kind: Credit, Account: "dst", Amount: int64(i)})
		check()
	}
	if !a.Ready() {
		t.Fatal("all txns closed: must be ready")
	}
}
