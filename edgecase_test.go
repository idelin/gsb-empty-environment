package pgoutput

import "testing"

// TestStreamAbortInsideOpenSegment covers an abort arriving while a stream
// segment is still open (allowed by later protocol revisions).
func TestStreamAbortInsideOpenSegment(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	// No Stream Stop: top-level abort resets the active segment state.
	feed(t, a, wireStreamAbort(100, 100, 10, 18, fixedClock(1)))

	if a.activeXID != 0 || a.activeSubXID != 0 {
		t.Fatalf("active segment not reset: xid=%d subxid=%d", a.activeXID, a.activeSubXID)
	}
	if _, ok := a.streams[100]; ok {
		t.Fatal("aborted buffer still present")
	}

	// Next segment must be a fresh first segment.
	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireStreamStop())
	feed(t, a, wireStreamCommit(100, 20, 36, fixedClock(2)))
}

// TestSubabortWithoutStreamedChanges is a no-op when nothing attributed to
// the aborted subtransaction was ever streamed.
func TestSubabortWithoutStreamedChanges(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireStreamStart(100, true, 0))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	feed(t, a, wireStreamStop())
	feed(t, a, wireStreamAbort(100, 109, 10, 18, fixedClock(1)))

	res := feed(t, a, wireStreamCommit(100, 20, 36, fixedClock(2)))
	if len(res.Transactions[0].Changes) != 1 {
		t.Fatalf("top-level change must survive unknown-subxid abort")
	}
}

// TestOriginAndMessageRouting exercises the Origin message and transactional
// messages inside an ordinary non-streamed transaction.
func TestOriginAndMessageRouting(t *testing.T) {
	a := streamAssemblerFixture(t)

	feed(t, a, wireBegin(1, 100, fixedClock(1)))
	origin := ParseOrDie(t, (&wireBuilder{}).lsn(96).cstr("node-a").build('O'))
	res, err := a.Handle(origin)
	if err != nil {
		t.Fatalf("Handle origin: %v", err)
	}
	if len(res.Transactions) != 0 {
		t.Fatal("origin must not publish a transaction")
	}
	feed(t, a, wireLogicalMessage(true, 104, "mark", []byte("v1")))
	feed(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))
	res = feed(t, a, wireCommit(100, 120, fixedClock(1)))

	txn := res.Transactions[0]
	if txn.Origin != "node-a" {
		t.Fatalf("origin not recorded: %q", txn.Origin)
	}
	if len(txn.Messages) != 1 || txn.Messages[0].Prefix != "mark" {
		t.Fatalf("transactional message lost: %+v", txn.Messages)
	}
	if txn.Changes[0].Relation.Name != "orders" {
		t.Fatalf("relation pointer not resolved: %+v", txn.Changes[0].Relation)
	}
}

// TestProtocolStateErrors verifies malformed sequencing is rejected instead
// of silently corrupting the assembly state.
func TestProtocolStateErrors(t *testing.T) {
	a := streamAssemblerFixture(t)

	// Stream Stop without an open segment.
	feedExpectErr(t, a, wireStreamStop())

	// Stream Commit for an unknown XID.
	feedExpectErr(t, a, wireStreamCommit(404, 1, 2, fixedClock(1)))

	// Stream Abort for an unknown XID.
	feedExpectErr(t, a, wireStreamAbort(404, 404, 1, 2, fixedClock(1)))

	// Nested / overlapping stream segments.
	feed(t, a, wireStreamStart(100, true, 0))
	feedExpectErr(t, a, wireStreamStart(200, true, 0))

	// Duplicate first segment for the same XID.
	feed(t, a, wireStreamStop())
	feedExpectErr(t, a, wireStreamStart(100, true, 0))

	// Row change outside any transaction.
	feed(t, a, wireStreamAbort(100, 100, 1, 2, fixedClock(1)))
	feedExpectErr(t, a, wireInsert(1, []TupleColumn{textCol("1"), textCol("10")}))

	// Origin outside a transaction.
	origin := ParseOrDie(t, (&wireBuilder{}).lsn(1).cstr("x").build('O'))
	if _, err := a.Handle(origin); err == nil {
		t.Fatal("expected error for Origin outside a transaction")
	}

	// Two Begins in a row.
	feed(t, a, wireBegin(1, 10, fixedClock(1)))
	feedExpectErr(t, a, wireBegin(2, 20, fixedClock(1)))
}

// TestTupleWidthMismatch ensures malformed tuples are rejected against the
// cached relation definition.
func TestTupleWidthMismatch(t *testing.T) {
	a := streamAssemblerFixture(t)
	feed(t, a, wireBegin(1, 10, fixedClock(1)))
	feedExpectErr(t, a, wireInsert(1, []TupleColumn{textCol("only-one-column")}))
	feedExpectErr(t, a, wireUpdate(1, 'N', nil,
		[]TupleColumn{textCol("1"), textCol("2"), textCol("3"), textCol("4")}))
	feedExpectErr(t, a, wireDelete(1, 'O', []TupleColumn{textCol("1")}))
}

// TestTruncateChange verifies truncate resolves relations and reaches the
// transaction as an OpTruncate change.
func TestTruncateChange(t *testing.T) {
	a := streamAssemblerFixture(t)
	feed(t, a, wireBegin(1, 10, fixedClock(1)))
	feed(t, a, wireTruncate(1, 1, 2))
	res := feed(t, a, wireCommit(10, 26, fixedClock(1)))

	changes := res.Transactions[0].Changes
	if len(changes) != 2 || changes[0].Op != OpTruncate || changes[1].Op != OpTruncate {
		t.Fatalf("truncate changes wrong: %+v", changes)
	}
	if changes[0].Relation.ID != 1 || changes[1].Relation.ID != 2 {
		t.Fatalf("truncate relation resolution wrong: %+v", changes)
	}
}
