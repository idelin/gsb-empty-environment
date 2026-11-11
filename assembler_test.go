package pgoutput

import (
	"testing"
	"time"
)

var testEpoch = time.Date(2026, time.November, 11, 0, 0, 0, 0, time.UTC)

func fixedClock(sec int) time.Time {
	return testEpoch.Add(time.Duration(sec) * time.Second)
}

func fixedTime() time.Time { return fixedClock(0) }

// streamAssemblerFixture wires a catalog with two relations used across the
// streaming scenarios.
func streamAssemblerFixture(t *testing.T) *Assembler {
	t.Helper()
	a := NewAssembler()
	feed(t, a, wireRelation(1, "public", "orders", 'd',
		RelationColumn{Key: true, Name: "id", DataType: 23, TypeModifier: -1},
		RelationColumn{Name: "amount", DataType: 23, TypeModifier: -1},
	))
	feed(t, a, wireRelation(2, "public", "events", 'd',
		RelationColumn{Key: true, Name: "id", DataType: 23, TypeModifier: -1},
		RelationColumn{Name: "payload", DataType: 25, TypeModifier: -1},
	))
	return a
}

// TestInterleavedStreamedTransactions constructs two large transactions whose
// streamed segments interleave on one connection, plus a small ordinary
// transaction between segments. Nothing may be published until each Stream
// Commit, and publication order must follow commit order on the wire.
func TestInterleavedStreamedTransactions(t *testing.T) {
	a := streamAssemblerFixture(t)

	// T1 (XID 100): first segment, then the stream is paused.
	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("2"), textCol("20")}))
	feed(t, a, wireStreamStop())

	// T2 (XID 200): complete streamed transaction committed first.
	feed(t, a, wireStreamStart(200, true, 0))
	feed(t, a, wireInsert(2, []TupleColumn{textCol("a"), textCol("ev-a")}))
	feed(t, a, wireStreamStop())
	res := feed(t, a, wireStreamCommit(200, 1000, 1016, fixedClock(1)))
	if len(res.Transactions) != 1 || res.Transactions[0].XID != 200 {
		t.Fatalf("expected T2 published first, got %+v", res.Transactions)
	}
	if got := a.SafeLSN(); got != 1016 {
		t.Fatalf("SafeLSN = %d, want 1016", got)
	}

	// Ordinary non-streamed transaction T3 while T1 is still streaming.
	feed(t, a, wireBegin(300, 2000, fixedClock(2)))
	feed(t, a, wireInsert(2, []TupleColumn{textCol("b"), textCol("ev-b")}))
	res = feed(t, a, wireCommit(2000, 2016, fixedClock(2)))
	if len(res.Transactions) != 1 || res.Transactions[0].XID != 300 {
		t.Fatalf("expected T3 published second, got %+v", res.Transactions)
	}

	// T1 continuation segment: interleaving must route rows back into T1.
	feed(t, a, wireStreamStart(100, false, 0))
	feed(t, a, wireUpdate(1, 'O',
		[]TupleColumn{textCol("1"), unchangedCol()},
		[]TupleColumn{textCol("1"), textCol("11")},
	))
	feed(t, a, wireStreamStop())

	// Keepalive ahead of T1's commit must not move SafeLSN.
	res = feed(t, a, wireKeepalive(9999, fixedClock(3), true))
	if len(res.Transactions) != 0 || len(res.Keepalives) != 1 {
		t.Fatalf("unexpected publication before T1 commit: %+v", res)
	}
	if got := a.SafeLSN(); got != 2016 {
		t.Fatalf("SafeLSN advanced past unpublished T1: %d", got)
	}
	if err := a.CheckAck(3000); err == nil {
		t.Fatal("CheckAck should reject LSN beyond unpublished transaction")
	}

	res = feed(t, a, wireStreamCommit(100, 3000, 3016, fixedClock(3)))
	if len(res.Transactions) != 1 || res.Transactions[0].XID != 100 {
		t.Fatalf("expected T1 published last, got %+v", res.Transactions)
	}
	txn := res.Transactions[0]
	if len(txn.Changes) != 3 {
		t.Fatalf("T1 should retain 3 changes across segments, got %d", len(txn.Changes))
	}
	upd := txn.Changes[2]
	if upd.Op != OpUpdate || string(upd.New[1].Value) != "11" {
		t.Fatalf("continuation change lost: %+v", upd)
	}
	if got := a.SafeLSN(); got != 3016 {
		t.Fatalf("SafeLSN = %d, want 3016", got)
	}
	if err := a.CheckAck(3016); err != nil {
		t.Fatalf("CheckAck at SafeLSN should pass: %v", err)
	}
}

// TestV2StreamAbortAndRestream covers the protocol-v2 rule: a subtransaction
// abort invalidates the entire streamed top-level transaction, the buffer is
// discarded, and the server re-streams everything with FirstSegment set.
func TestV2StreamAbortAndRestream(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("2"), textCol("20")}))
	feed(t, a, wireStreamStop())

	// On protocol v2 a subtransaction abort inside a streamed transaction
	// invalidates the whole top-level transaction; the server aborts it and
	// later re-streams everything with Stream Start(first segment).
	feed(t, a, wireStreamAbort(100, 100, 400, 408, fixedClock(1)))

	if _, buffered := a.streams[100]; buffered {
		t.Fatal("aborted stream buffer should be gone")
	}
	if got := a.SafeLSN(); got != 0 {
		t.Fatalf("SafeLSN = %d, want 0 after abort", got)
	}

	// Re-stream from scratch.
	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("99")}))
	feed(t, a, wireStreamStop())
	res := feed(t, a, wireStreamCommit(100, 500, 516, fixedClock(2)))
	if len(res.Transactions) != 1 {
		t.Fatalf("expected re-streamed transaction, got %+v", res.Transactions)
	}
	if len(res.Transactions[0].Changes) != 1 {
		t.Fatalf("re-stream must contain only new changes, got %d", len(res.Transactions[0].Changes))
	}
	if string(res.Transactions[0].Changes[0].New[1].Value) != "99" {
		t.Fatalf("stale change survived re-stream: %+v", res.Transactions[0].Changes[0])
	}

	// A continuation without an existing buffer must be rejected.
	feedExpectErr(t, a, wireStreamStart(101, false, 0))
}

// TestSubtransactionRollback uses v4-style subxid stream framing inside one
// streamed transaction and verifies only the aborted subtransaction's changes
// are removed while surviving changes keep their wire order.
func TestSubtransactionRollback(t *testing.T) {
	a := streamAssemblerFixture(t)

	// Top-level segment.
	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	feed(t, a, wireStreamStop())

	// Subtransaction 101 segment: two inserts, one of which is rolled back.
	feed(t, a, wireStreamStart(100, false, 101))
	feed(t, a, wireInsert(2, []TupleColumn{textCol("s1"), textCol("sub-1")}))
	feed(t, a, wireInsert(2, []TupleColumn{textCol("s2"), textCol("sub-2")}))
	feed(t, a, wireStreamStop())

	// Another subtransaction 102 segment.
	feed(t, a, wireStreamStart(100, false, 102))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("2"), textCol("20")}))
	feed(t, a, wireStreamStop())

	// Abort subxid 101 while top-level and 102 survive.
	feed(t, a, wireStreamAbort(100, 101, 600, 608, fixedClock(1)))

	buf := a.streams[100]
	if len(buf.changes) != 2 {
		t.Fatalf("expected 2 surviving changes, got %d", len(buf.changes))
	}
	for _, ch := range buf.changes {
		if ch.SubXID == 101 {
			t.Fatalf("rolled-back subxid change survived: %+v", ch)
		}
	}

	res := feed(t, a, wireStreamCommit(100, 700, 716, fixedClock(2)))
	if len(res.Transactions) != 1 || len(res.Transactions[0].Changes) != 2 {
		t.Fatalf("unexpected committed changes: %+v", res.Transactions)
	}
	// Surviving order: top-level insert first, then subxid 102.
	if res.Transactions[0].Changes[0].SubXID != 0 ||
		res.Transactions[0].Changes[1].SubXID != 102 {
		t.Fatalf("wire order not preserved: %+v", res.Transactions[0].Changes)
	}
}

// TestSubtransactionRollbackSameRow verifies that an update made by an aborted
// subtransaction does not shadow the top-level transaction's earlier insert
// after the rollback rebuilds the row index.
func TestSubtransactionRollbackSameRow(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	feed(t, a, wireStreamStop())

	feed(t, a, wireStreamStart(100, false, 101))
	feed(t, a, wireUpdate(1, 'O',
		[]TupleColumn{textCol("1"), unchangedCol()},
		[]TupleColumn{textCol("1"), textCol("11")},
	))
	feed(t, a, wireStreamStop())

	feed(t, a, wireStreamAbort(100, 101, 800, 808, fixedClock(1)))

	res := feed(t, a, wireStreamCommit(100, 900, 916, fixedClock(2)))
	changes := res.Transactions[0].Changes
	if len(changes) != 1 || changes[0].Op != OpInsert ||
		string(changes[0].New[1].Value) != "10" {
		t.Fatalf("rolled-back update leaked into commit: %+v", changes)
	}
}

// TestNonStreamedCommitOrderAndLSN verifies ordinary transactions commit in
// order and SafeLSN tracks their end LSNs monotonically.
func TestNonStreamedCommitOrderAndLSN(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireBegin(1, 100, fixedClock(1)))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("1")}))
	res := feed(t, a, wireCommit(100, 120, fixedClock(1)))
	if len(res.Transactions) != 1 || a.SafeLSN() != 120 {
		t.Fatalf("first commit not published: %+v safe=%d", res, a.SafeLSN())
	}

	feed(t, a, wireBegin(2, 200, fixedClock(2)))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("2"), textCol("2")}))
	res = feed(t, a, wireCommit(200, 220, fixedClock(2)))
	if res.Transactions[0].XID != 2 || a.SafeLSN() != 220 {
		t.Fatalf("second commit not published in order: %+v", res)
	}

	// Protocol sanity checks.
	feedExpectErr(t, a, wireCommit(1, 1, fixedClock(1)))
}

// TestTransactionalAndNonTransactionalMessages checks logical decoding
// messages end up in the right place inside and outside transactions.
func TestTransactionalAndNonTransactionalMessages(t *testing.T) {
	a := streamAssemblerFixture(t)

	res := feed(t, a, wireLogicalMessage(false, 50, "notice", []byte("hi")))
	if len(res.Messages) != 1 || res.Messages[0].Transactional {
		t.Fatalf("non-transactional message misrouted: %+v", res.Messages)
	}

	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireLogicalMessage(true, 60, "streamed", []byte("x")))
	feed(t, a, wireStreamStop())
	res = feed(t, a, wireStreamCommit(100, 100, 116, fixedClock(1)))
	if len(res.Transactions[0].Messages) != 1 ||
		res.Transactions[0].Messages[0].Prefix != "streamed" {
		t.Fatalf("transactional message not attached: %+v", res.Transactions[0].Messages)
	}
}

// TestSubabortInsertThenDeleteSameRow covers a subtransaction that inserts a
// row and deletes it before being rolled back. Neither change may survive, and
// the surviving top-level transaction must remain intact.
func TestSubabortInsertThenDeleteSameRow(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	feed(t, a, wireStreamStop())

	feed(t, a, wireStreamStart(100, false, 101))
	feed(t, a, wireInsert(2, []TupleColumn{textCol("k"), textCol("v")}))
	feed(t, a, wireDelete(2, 'K', []TupleColumn{textCol("k")}))
	feed(t, a, wireStreamStop())

	feed(t, a, wireStreamAbort(100, 101, 10, 18, fixedClock(1)))

	res := feed(t, a, wireStreamCommit(100, 20, 36, fixedClock(2)))
	changes := res.Transactions[0].Changes
	if len(changes) != 1 || changes[0].Relation.ID != 1 {
		t.Fatalf("only the top-level insert should survive, got %+v", changes)
	}
}
