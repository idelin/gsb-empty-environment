package ledger

import (
	"sync"
	"testing"
)

func mustApply(t *testing.T, e *Engine, ops ...Op) []Receipt {
	t.Helper()
	rcs, err := e.Apply(ops...)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	return rcs
}

func mustBalance(t *testing.T, e *Engine, account, currency string) Balance {
	t.Helper()
	b, err := e.BalanceOf(account, currency)
	if err != nil {
		t.Fatalf("BalanceOf failed: %v", err)
	}
	return b
}

func TestFreezeSeparatesBuckets(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e, Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 1000, ReceiptID: "r-fund"})

	rcs := mustApply(t, e, Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 400, ReceiptID: "r-freeze"})
	if rcs[0].Version != 1 {
		t.Fatalf("freeze version = %d, want 1", rcs[0].Version)
	}
	b := mustBalance(t, e, "a", "USD")
	if b.Available != 600 || b.Frozen != 400 || b.Settled != 0 {
		t.Fatalf("balance = %+v, want {600 400 0}", b)
	}
	if err := e.VerifyConservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}

func TestPartialSettleThenReleaseRemainder(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 1000, ReceiptID: "r1"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 500, ReceiptID: "r2"},
	)

	// Partial settle of 200 out of 500.
	rcs := mustApply(t, e, Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 200, Version: 1, ReceiptID: "r3"})
	if rcs[0].Version != 2 {
		t.Fatalf("settle version = %d, want 2", rcs[0].Version)
	}
	b := mustBalance(t, e, "a", "USD")
	if b.Available != 500 || b.Frozen != 300 || b.Settled != 200 {
		t.Fatalf("balance = %+v, want {500 300 200}", b)
	}

	// Release frees only the remaining 300; the settled 200 stays settled.
	rcs = mustApply(t, e, Op{Kind: OpRelease, Account: "a", ReqID: "req-1", Currency: "USD", Version: 2, ReceiptID: "r4"})
	if rcs[0].Amount != 300 {
		t.Fatalf("released = %d, want 300", rcs[0].Amount)
	}
	b = mustBalance(t, e, "a", "USD")
	if b.Available != 800 || b.Frozen != 0 || b.Settled != 200 {
		t.Fatalf("balance = %+v, want {800 0 200}", b)
	}

	// Closed freeze rejects further operations.
	if _, err := e.Apply(Op{Kind: OpRelease, Account: "a", ReqID: "req-1", Currency: "USD", Version: 3, ReceiptID: "r5"}); err != ErrFreezeClosed {
		t.Fatalf("release on closed freeze: err = %v, want ErrFreezeClosed", err)
	}
	if err := e.VerifyConservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}

func TestSettleExceedingRemainderRejected(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 100, ReceiptID: "r1"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 100, ReceiptID: "r2"},
		Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 60, Version: 1, ReceiptID: "r3"},
	)
	if _, err := e.Apply(Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 50, Version: 2, ReceiptID: "r4"}); err != ErrAmountExceeds {
		t.Fatalf("err = %v, want ErrAmountExceeds", err)
	}
}

func TestVersionArbitrationConcurrentSettle(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 1000, ReceiptID: "r1"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 1000, ReceiptID: "r2"},
	)

	// 16 goroutines race to settle at version 1; exactly one commit wins.
	const racers = 16
	var wg sync.WaitGroup
	results := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			op := Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 100, Version: 1}
			_, results[i] = e.Apply(op)
		}(i)
	}
	wg.Wait()

	wins, conflicts := 0, 0
	for _, err := range results {
		switch err {
		case nil:
			wins++
		case ErrVersionConflict:
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || conflicts != racers-1 {
		t.Fatalf("wins=%d conflicts=%d, want 1 and %d", wins, conflicts, racers-1)
	}
	b := mustBalance(t, e, "a", "USD")
	if b.Settled != 100 || b.Frozen != 900 {
		t.Fatalf("balance = %+v, want frozen=900 settled=100", b)
	}
	if err := e.VerifyConservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}

func TestBatchAtomicRollback(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	e.OpenAccount("b")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 100, ReceiptID: "r1"},
		Op{Kind: OpFund, Account: "b", Currency: "USD", Amount: 50, ReceiptID: "r2"},
	)
	journalBefore := len(e.Journal())

	// Second freeze exceeds b's funds: the whole batch must roll back.
	_, err := e.Apply(
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-a", Currency: "USD", Amount: 80, ReceiptID: "r3"},
		Op{Kind: OpFreeze, Account: "b", ReqID: "req-b", Currency: "USD", Amount: 500, ReceiptID: "r4"},
	)
	if err != ErrInsufficientFunds {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
	ba := mustBalance(t, e, "a", "USD")
	if ba.Available != 100 || ba.Frozen != 0 {
		t.Fatalf("a balance = %+v, batch was not rolled back", ba)
	}
	if _, err := e.FreezeOf("a", "req-a"); err != ErrFreezeNotFound {
		t.Fatalf("rolled-back freeze should not exist, err = %v", err)
	}
	if len(e.Journal()) != journalBefore {
		t.Fatalf("journal grew after failed batch")
	}
}

func TestBatchCrossAccountConservesPerCurrency(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	e.OpenAccount("b")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 300, ReceiptID: "r1"},
		Op{Kind: OpFund, Account: "a", Currency: "EUR", Amount: 200, ReceiptID: "r2"},
		Op{Kind: OpFund, Account: "b", Currency: "USD", Amount: 100, ReceiptID: "r3"},
	)
	mustApply(t, e,
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-usd", Currency: "USD", Amount: 300, ReceiptID: "r4"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-eur", Currency: "EUR", Amount: 200, ReceiptID: "r5"},
		Op{Kind: OpFreeze, Account: "b", ReqID: "req-b", Currency: "USD", Amount: 100, ReceiptID: "r6"},
		Op{Kind: OpSettle, Account: "a", ReqID: "req-usd", Currency: "USD", Amount: 120, Version: 1, ReceiptID: "r7"},
		Op{Kind: OpRelease, Account: "a", ReqID: "req-eur", Currency: "EUR", Version: 1, ReceiptID: "r8"},
	)
	if err := e.VerifyConservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}

func TestConservationDetectsCorruption(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e, Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 100, ReceiptID: "r1"})

	e.mu.Lock()
	e.st.accounts["a"].Balances["USD"].Available += 1 // simulate corruption
	e.mu.Unlock()

	if err := e.VerifyConservation(); err == nil {
		t.Fatalf("expected conservation violation")
	}
}

func TestDuplicateFreezeRequestID(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 100, ReceiptID: "r1"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 60, ReceiptID: "r2"},
	)
	// Same request id again: idempotent, funds are not frozen twice.
	rcs := mustApply(t, e, Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 60, ReceiptID: "r3"})
	if !rcs[0].Duplicate {
		t.Fatalf("expected duplicate receipt")
	}
	b := mustBalance(t, e, "a", "USD")
	if b.Frozen != 60 || b.Available != 40 {
		t.Fatalf("balance = %+v, want frozen=60 available=40", b)
	}
}

func TestReplayDetectsDuplicateReceipts(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	e.OpenAccount("b")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 500, ReceiptID: "r1"},
		Op{Kind: OpFund, Account: "b", Currency: "USD", Amount: 200, ReceiptID: "r2"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 300, ReceiptID: "r3"},
		Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 100, Version: 1, ReceiptID: "r4"},
		Op{Kind: OpRelease, Account: "a", ReqID: "req-1", Currency: "USD", Version: 2, ReceiptID: "r5"},
	)
	journal := e.Journal()
	if len(journal) != 5 {
		t.Fatalf("journal len = %d, want 5", len(journal))
	}
	want := mustBalance(t, e, "a", "USD")

	// Fault: replay the full journal into the same engine. Every entry
	// must be recognised as a duplicate receipt and nothing moves.
	dups, err := e.Replay(journal)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(dups) != 5 {
		t.Fatalf("duplicates = %v, want 5", dups)
	}
	if got := mustBalance(t, e, "a", "USD"); got != want {
		t.Fatalf("balance changed after replay: %+v -> %+v", want, got)
	}

	// Crash-recovery: replay a prefix into a fresh engine, "crash",
	// then replay the full journal. Prefix entries are duplicates,
	// the tail applies exactly once, and state converges.
	fresh := NewEngine()
	fresh.OpenAccount("a")
	fresh.OpenAccount("b")
	if _, err := fresh.Replay(journal[:3]); err != nil {
		t.Fatalf("prefix replay: %v", err)
	}
	dups, err = fresh.Replay(journal)
	if err != nil {
		t.Fatalf("full replay: %v", err)
	}
	if len(dups) != 3 {
		t.Fatalf("duplicates = %v, want 3", dups)
	}
	if got := mustBalance(t, fresh, "a", "USD"); got != want {
		t.Fatalf("recovered balance = %+v, want %+v", got, want)
	}
	if err := fresh.VerifyConservation(); err != nil {
		t.Fatalf("conservation after replay: %v", err)
	}
}

func TestReplayRejectsConflictThatIsNotDuplicate(t *testing.T) {
	e := NewEngine()
	e.OpenAccount("a")
	mustApply(t, e,
		Op{Kind: OpFund, Account: "a", Currency: "USD", Amount: 100, ReceiptID: "r1"},
		Op{Kind: OpFreeze, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 100, ReceiptID: "r2"},
		Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 100, Version: 1, ReceiptID: "r3"},
	)
	// A settle with a new receipt id but a stale version is a genuine
	// conflict, not a duplicate, and must fail loudly.
	stale := Entry{ReceiptID: "r9", Op: Op{Kind: OpSettle, Account: "a", ReqID: "req-1", Currency: "USD", Amount: 10, Version: 1}}
	if _, err := e.Replay([]Entry{stale}); err == nil {
		t.Fatalf("expected replay conflict error")
	}
}
