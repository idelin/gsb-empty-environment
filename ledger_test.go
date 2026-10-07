package ledger

import (
	"strings"
	"testing"
)

func isErr(err, sentinel error) bool {
	return err != nil && strings.Contains(err.Error(), sentinel.Error())
}

func voucher(id string, entries ...Entry) Voucher {
	return Voucher{ID: id, Entries: entries}
}

func reversal(id, of string, orig Voucher) Voucher {
	entries := make([]Entry, len(orig.Entries))
	for i, e := range orig.Entries {
		entries[i] = Entry{Account: e.Account, Debit: e.Credit, Credit: e.Debit}
	}
	return Voucher{ID: id, Entries: entries, ReversalOf: of}
}

// sale is a balanced 100.00 CNY sale: debit cash, credit revenue.
func sale(id string) Voucher {
	return voucher(id,
		Entry{Account: "cash", Debit: 10000},
		Entry{Account: "revenue", Credit: 10000},
	)
}

func mustSubmit(t *testing.T, l *Ledger, b Batch) {
	t.Helper()
	if err := l.Submit(b); err != nil {
		t.Fatalf("Submit(%s) failed: %v", b.ID, err)
	}
}

func TestBalancedPostingAndConservation(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{sale("v1")}})
	if got := l.Balance("cash"); got != 10000 {
		t.Fatalf("cash balance = %d, want 10000", got)
	}
	if got := l.Balance("revenue"); got != -10000 {
		t.Fatalf("revenue balance = %d, want -10000", got)
	}
	if err := l.Conservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}

func TestUnbalancedVoucherRejected(t *testing.T) {
	l := New()
	bad := voucher("v1",
		Entry{Account: "cash", Debit: 10000},
		Entry{Account: "revenue", Credit: 9999},
	)
	err := l.Submit(Batch{ID: "b1", Vouchers: []Voucher{bad}})
	if !isErr(err, ErrUnbalanced) {
		t.Fatalf("err = %v, want ErrUnbalanced", err)
	}
	if l.Balance("cash") != 0 || l.Balance("revenue") != 0 {
		t.Fatal("rejected batch changed balances")
	}
}

func TestBatchAtomicAllOrNothing(t *testing.T) {
	l := New()
	good := sale("v1")
	bad := voucher("v2",
		Entry{Account: "cash", Debit: 500},
		Entry{Account: "revenue", Credit: 400},
	)
	err := l.Submit(Batch{ID: "b1", Vouchers: []Voucher{good, bad}})
	if err == nil {
		t.Fatal("expected error")
	}
	if l.Balance("cash") != 0 {
		t.Fatalf("partial commit leaked: cash = %d", l.Balance("cash"))
	}
	if len(l.vouchers) != 0 {
		t.Fatalf("partial commit leaked %d vouchers", len(l.vouchers))
	}
}

func TestDuplicateBatchIDIdempotent(t *testing.T) {
	l := New()
	b := Batch{ID: "b1", Vouchers: []Voucher{sale("v1")}}
	mustSubmit(t, l, b)
	mustSubmit(t, l, b) // same id, same content: no-op
	if got := l.Balance("cash"); got != 10000 {
		t.Fatalf("duplicate request re-recorded: cash = %d", got)
	}
}

func TestSameBatchIDDifferentContentRejected(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{sale("v1")}})
	other := voucher("v2",
		Entry{Account: "cash", Debit: 700},
		Entry{Account: "revenue", Credit: 700},
	)
	err := l.Submit(Batch{ID: "b1", Vouchers: []Voucher{other}})
	if !isErr(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestSameVoucherIDDifferentContentRejected(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{sale("v1")}})
	altered := voucher("v1",
		Entry{Account: "cash", Debit: 20000},
		Entry{Account: "revenue", Credit: 20000},
	)
	err := l.Submit(Batch{ID: "b2", Vouchers: []Voucher{altered}})
	if !isErr(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	// Identical voucher id + content in a new batch is an idempotent skip.
	mustSubmit(t, l, Batch{ID: "b3", Vouchers: []Voucher{sale("v1")}})
	if got := l.Balance("cash"); got != 10000 {
		t.Fatalf("duplicate voucher re-recorded: cash = %d", got)
	}
}

func TestReversalMirrorsOriginal(t *testing.T) {
	l := New()
	orig := sale("v1")
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{orig}})
	mustSubmit(t, l, Batch{ID: "b2", Vouchers: []Voucher{reversal("r1", "v1", orig)}})
	if l.Balance("cash") != 0 || l.Balance("revenue") != 0 {
		t.Fatal("reversal did not zero out balances")
	}
	if !l.Reversed("v1") {
		t.Fatal("v1 not marked reversed")
	}
	if err := l.Conservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}

func TestReversalMustMirrorLineByLine(t *testing.T) {
	l := New()
	orig := sale("v1")
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{orig}})

	// Balanced, but amounts differ from the original.
	bad := voucher("r1",
		Entry{Account: "cash", Credit: 9999},
		Entry{Account: "revenue", Debit: 9999},
	)
	bad.ReversalOf = "v1"
	if err := l.Submit(Batch{ID: "b2", Vouchers: []Voucher{bad}}); !isErr(err, ErrReversalMismatch) {
		t.Fatalf("err = %v, want ErrReversalMismatch", err)
	}

	// Same totals but different accounts is not a mirror.
	wrongAcct := voucher("r2",
		Entry{Account: "revenue", Debit: 10000},
		Entry{Account: "cash", Credit: 10000},
	)
	wrongAcct.ReversalOf = "v1"
	if err := l.Submit(Batch{ID: "b3", Vouchers: []Voucher{wrongAcct}}); !isErr(err, ErrReversalMismatch) {
		t.Fatalf("err = %v, want ErrReversalMismatch", err)
	}
	if l.Reversed("v1") {
		t.Fatal("failed reversal marked original reversed")
	}
}

func TestReversalArrivesFirstAndIsHeldPending(t *testing.T) {
	l := New()
	orig := sale("v1")
	// Reversal batch arrives before the original voucher.
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{reversal("r1", "v1", orig)}})
	if l.PendingReversals("v1") != 1 {
		t.Fatalf("pending = %d, want 1", l.PendingReversals("v1"))
	}
	if l.Balance("cash") != 0 {
		t.Fatal("pending reversal must not affect balances")
	}
	// Original arrives later, in a different batch: pending reversal settles.
	mustSubmit(t, l, Batch{ID: "b2", Vouchers: []Voucher{orig}})
	if l.PendingReversals("v1") != 0 {
		t.Fatal("pending reversal not settled")
	}
	if !l.Reversed("v1") {
		t.Fatal("v1 not reversed after settle")
	}
	if l.Balance("cash") != 0 || l.Balance("revenue") != 0 {
		t.Fatal("settled reversal did not zero out balances")
	}
	if err := l.VerifyReplay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestDoubleReversalRejected(t *testing.T) {
	l := New()
	orig := sale("v1")
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{orig}})
	mustSubmit(t, l, Batch{ID: "b2", Vouchers: []Voucher{reversal("r1", "v1", orig)}})
	err := l.Submit(Batch{ID: "b3", Vouchers: []Voucher{reversal("r2", "v1", orig)}})
	if !isErr(err, ErrAlreadyReversed) {
		t.Fatalf("err = %v, want ErrAlreadyReversed", err)
	}
}

func TestDoublePendingReversalRejected(t *testing.T) {
	l := New()
	orig := sale("v1")
	// Both reversals arrive before the original; the second must be rejected.
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{reversal("r1", "v1", orig)}})
	err := l.Submit(Batch{ID: "b2", Vouchers: []Voucher{reversal("r2", "v1", orig)}})
	if !isErr(err, ErrAlreadyReversed) {
		t.Fatalf("err = %v, want ErrAlreadyReversed", err)
	}
	mustSubmit(t, l, Batch{ID: "b3", Vouchers: []Voucher{orig}})
	if !l.Reversed("v1") || l.Rejected("r2") {
		t.Fatal("unexpected reversal state")
	}
	if err := l.VerifyReplay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestPendingReversalInvalidWhenOriginalArrives(t *testing.T) {
	l := New()
	orig := sale("v1")
	bad := voucher("r1",
		Entry{Account: "cash", Credit: 5000},
		Entry{Account: "revenue", Debit: 5000},
	)
	bad.ReversalOf = "v1"
	// Accepted as pending (original unknown), rejected when original arrives.
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{bad}})
	mustSubmit(t, l, Batch{ID: "b2", Vouchers: []Voucher{orig}})
	if !l.Rejected("r1") {
		t.Fatal("invalid pending reversal not rejected at settle")
	}
	if l.Reversed("v1") {
		t.Fatal("invalid reversal marked original reversed")
	}
	if got := l.Balance("cash"); got != 10000 {
		t.Fatalf("original not applied: cash = %d", got)
	}
	if err := l.Conservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
	if err := l.VerifyReplay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestReversalOfReversalRejected(t *testing.T) {
	l := New()
	orig := sale("v1")
	rev := reversal("r1", "v1", orig)
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{orig, rev}})
	err := l.Submit(Batch{ID: "b2", Vouchers: []Voucher{reversal("r2", "r1", rev)}})
	if !isErr(err, ErrReversalOfReversal) {
		t.Fatalf("err = %v, want ErrReversalOfReversal", err)
	}
}

func TestCrossBatchDependenciesAndReplay(t *testing.T) {
	l := New()
	s1 := sale("v1")
	s2 := voucher("v2",
		Entry{Account: "cash", Debit: 2500},
		Entry{Account: "revenue", Credit: 2500},
	)
	s3 := voucher("v3",
		Entry{Account: "receivable", Debit: 4000},
		Entry{Account: "revenue", Credit: 4000},
	)
	// b1: reversal of v2 arrives early (pending). b2: v1+v2. b3: reversal of
	// v1 plus v3. b4: duplicate of b3 (idempotent). Dependencies cross batches.
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{reversal("r2", "v2", s2)}})
	mustSubmit(t, l, Batch{ID: "b2", Vouchers: []Voucher{s1, s2}})
	mustSubmit(t, l, Batch{ID: "b3", Vouchers: []Voucher{reversal("r1", "v1", s1), s3}})
	mustSubmit(t, l, Batch{ID: "b3", Vouchers: []Voucher{reversal("r1", "v1", s1), s3}})

	if !l.Reversed("v1") || !l.Reversed("v2") || l.Reversed("v3") {
		t.Fatal("unexpected reversed set")
	}
	want := map[string]int64{"cash": 0, "revenue": -4000, "receivable": 4000}
	for acct, w := range want {
		if got := l.Balance(acct); got != w {
			t.Fatalf("balance[%s] = %d, want %d", acct, got, w)
		}
	}
	if err := l.Conservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
	if err := l.VerifyReplay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestIntegerCentsNoFloat(t *testing.T) {
	l := New()
	// 0.1 + 0.2 style amounts stay exact in integer cents.
	v := voucher("v1",
		Entry{Account: "cash", Debit: 10},
		Entry{Account: "cash", Debit: 20},
		Entry{Account: "revenue", Credit: 30},
	)
	mustSubmit(t, l, Batch{ID: "b1", Vouchers: []Voucher{v}})
	if got := l.Balance("cash"); got != 30 {
		t.Fatalf("cash = %d, want 30", got)
	}
	if err := l.Conservation(); err != nil {
		t.Fatalf("conservation: %v", err)
	}
}
