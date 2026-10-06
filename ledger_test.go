package ledger

import (
	"strings"
	"testing"
)

func voucher(id string, lines ...Line) Voucher {
	return Voucher{ID: id, Lines: lines}
}

func reversal(id, target string, lines ...Line) Voucher {
	return Voucher{ID: id, Reverses: target, Lines: lines}
}

func mustSubmit(t *testing.T, l *Ledger, b Batch) Result {
	t.Helper()
	res, err := l.Submit(b)
	if err != nil {
		t.Fatalf("Submit(%s) failed: %v", b.RequestID, err)
	}
	return res
}

func mustFail(t *testing.T, l *Ledger, b Batch, want error) {
	t.Helper()
	_, err := l.Submit(b)
	if err == nil {
		t.Fatalf("Submit(%s) succeeded, want %v", b.RequestID, want)
	}
	if !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("Submit(%s) error = %v, want %v", b.RequestID, err, want)
	}
}

func mustVerify(t *testing.T, l *Ledger) {
	t.Helper()
	if err := l.Verify(); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestBalancedVoucherCommits(t *testing.T) {
	l := New()
	res := mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 10000}, Line{"revenue", -10000}),
	}})
	if len(res.Applied) != 1 || res.Applied[0] != "v1" {
		t.Fatalf("Applied = %v", res.Applied)
	}
	if l.Balance("cash") != 10000 || l.Balance("revenue") != -10000 {
		t.Fatalf("balances = %v", l.Balances())
	}
	mustVerify(t, l)
}

func TestUnbalancedVoucherRejected(t *testing.T) {
	l := New()
	mustFail(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 10000}, Line{"revenue", -9999}),
	}}, ErrUnbalanced)
	if len(l.Balances()) != 0 {
		t.Fatalf("rejected batch left balances: %v", l.Balances())
	}
	mustVerify(t, l)
}

func TestBatchIsAtomic(t *testing.T) {
	l := New()
	mustFail(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 500}, Line{"revenue", -500}),
		voucher("v2", Line{"cash", 500}), // unbalanced
	}}, ErrUnbalanced)
	if l.Balance("cash") != 0 || len(l.Balances()) != 0 {
		t.Fatalf("partial commit leaked: %v", l.Balances())
	}
	mustVerify(t, l)
}

func TestDuplicateVoucherIDRejected(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 100}, Line{"revenue", -100}),
	}})
	mustFail(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 200}, Line{"revenue", -200}),
	}}, ErrDuplicateVoucher)
	if l.Balance("cash") != 100 {
		t.Fatalf("cash = %d", l.Balance("cash"))
	}
	mustVerify(t, l)
}

func TestIdempotentReplaySameContent(t *testing.T) {
	l := New()
	b := Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 10000}, Line{"revenue", -10000}),
	}}
	first := mustSubmit(t, l, b)
	second := mustSubmit(t, l, b)
	if len(second.Applied) != 1 || second.Applied[0] != first.Applied[0] {
		t.Fatalf("replay result = %+v", second)
	}
	if l.Balance("cash") != 10000 {
		t.Fatalf("duplicate request re-recorded, cash = %d", l.Balance("cash"))
	}
	mustVerify(t, l)
}

func TestSameRequestIDDifferentContentRejected(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 100}, Line{"revenue", -100}),
	}})
	_, err := l.Submit(Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v2", Line{"cash", 100}, Line{"revenue", -100}),
	}})
	if err != ErrRequestConflict {
		t.Fatalf("err = %v, want ErrRequestConflict", err)
	}
	if l.Balance("cash") != 100 {
		t.Fatalf("conflicting request applied, cash = %d", l.Balance("cash"))
	}
	mustVerify(t, l)
}

func TestReversalNegatesOriginal(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 2500}, Line{"revenue", -2500}),
	}})
	res := mustSubmit(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -2500}, Line{"revenue", 2500}),
	}})
	if len(res.Applied) != 1 {
		t.Fatalf("Applied = %v", res.Applied)
	}
	if l.Balance("cash") != 0 || l.Balance("revenue") != 0 {
		t.Fatalf("reversal did not zero balances: %v", l.Balances())
	}
	if !l.Reversed("v1") {
		t.Fatal("v1 not marked reversed")
	}
	mustVerify(t, l)
}

func TestReversalMustNegateLineByLine(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 1000}, Line{"revenue", -1000}),
	}})
	// Balanced overall but not the line-by-line negation.
	mustFail(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -600}, Line{"revenue", 600}),
	}}, ErrReversalMismatch)
	if l.Balance("cash") != 1000 || l.Reversed("v1") {
		t.Fatalf("bad reversal leaked: %v", l.Balances())
	}
	mustVerify(t, l)
}

func TestDoubleReversalRejected(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 1000}, Line{"revenue", -1000}),
	}})
	mustSubmit(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -1000}, Line{"revenue", 1000}),
	}})
	mustFail(t, l, Batch{RequestID: "r3", Vouchers: []Voucher{
		reversal("v1-rev2", "v1", Line{"cash", -1000}, Line{"revenue", 1000}),
	}}, ErrDoubleReversal)
	if l.Balance("cash") != 0 {
		t.Fatalf("double reversal leaked: %v", l.Balances())
	}
	mustVerify(t, l)
}

func TestReversalOfReversalRejected(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 1000}, Line{"revenue", -1000}),
	}})
	mustSubmit(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -1000}, Line{"revenue", 1000}),
	}})
	mustFail(t, l, Batch{RequestID: "r3", Vouchers: []Voucher{
		reversal("v1-rev-rev", "v1-rev", Line{"cash", 1000}, Line{"revenue", -1000}),
	}}, ErrUnknownReversal)
	mustVerify(t, l)
}

func TestReversalBeforeOriginalIsParked(t *testing.T) {
	l := New()
	// Reversal arrives first: parked, nothing applied.
	res := mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -4200}, Line{"revenue", 4200}),
	}})
	if len(res.Pending) != 1 || res.Pending[0] != "v1-rev" {
		t.Fatalf("Pending = %v", res.Pending)
	}
	if l.Pending() != 1 || len(l.Balances()) != 0 {
		t.Fatalf("parked reversal touched state: %v", l.Balances())
	}
	// Original arrives later: parked reversal releases in the same commit.
	res = mustSubmit(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 4200}, Line{"revenue", -4200}),
	}})
	if len(res.Applied) != 2 {
		t.Fatalf("Applied = %v, want original + released reversal", res.Applied)
	}
	if l.Pending() != 0 {
		t.Fatalf("Pending = %d", l.Pending())
	}
	if l.Balance("cash") != 0 || l.Balance("revenue") != 0 {
		t.Fatalf("released reversal did not zero balances: %v", l.Balances())
	}
	if !l.Reversed("v1") {
		t.Fatal("v1 not marked reversed")
	}
	mustVerify(t, l)
}

func TestParkedReversalInvalidAgainstOriginal(t *testing.T) {
	l := New()
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -9999}, Line{"revenue", 9999}),
	}})
	// Original commits; the mismatched parked reversal is refused alone.
	res := mustSubmit(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 4200}, Line{"revenue", -4200}),
	}})
	if len(res.Rejected) != 1 || res.Rejected[0].VoucherID != "v1-rev" {
		t.Fatalf("Rejected = %+v", res.Rejected)
	}
	if l.Balance("cash") != 4200 || l.Reversed("v1") {
		t.Fatalf("state = %v reversed=%v", l.Balances(), l.Reversed("v1"))
	}
	mustVerify(t, l)
}

func TestDoubleReversalViaParkedQueue(t *testing.T) {
	l := New()
	// Two reversals park against the same not-yet-seen original.
	mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		reversal("rev-a", "v1", Line{"cash", -100}, Line{"revenue", 100}),
	}})
	mustSubmit(t, l, Batch{RequestID: "r2", Vouchers: []Voucher{
		reversal("rev-b", "v1", Line{"cash", -100}, Line{"revenue", 100}),
	}})
	// Original commits: first parked reversal applies, second is refused.
	res := mustSubmit(t, l, Batch{RequestID: "r3", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 100}, Line{"revenue", -100}),
	}})
	if len(res.Rejected) != 1 || res.Rejected[0].VoucherID != "rev-b" {
		t.Fatalf("Rejected = %+v, want rev-b refused", res.Rejected)
	}
	if l.Balance("cash") != 0 {
		t.Fatalf("second reversal applied: %v", l.Balances())
	}
	mustVerify(t, l)
}

func TestReversalWithinSameBatch(t *testing.T) {
	l := New()
	res := mustSubmit(t, l, Batch{RequestID: "r1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 700}, Line{"revenue", -700}),
		reversal("v1-rev", "v1", Line{"cash", -700}, Line{"revenue", 700}),
	}})
	if len(res.Applied) != 2 || l.Balance("cash") != 0 || !l.Reversed("v1") {
		t.Fatalf("Applied=%v balances=%v", res.Applied, l.Balances())
	}
	mustVerify(t, l)
}

func TestCrossBatchDependenciesAndConservation(t *testing.T) {
	l := New()
	// Batch 1: two independent vouchers.
	mustSubmit(t, l, Batch{RequestID: "b1", Vouchers: []Voucher{
		voucher("v1", Line{"cash", 1000}, Line{"revenue", -1000}),
		voucher("v2", Line{"ar", 3000}, Line{"revenue", -3000}),
	}})
	// Batch 2: reversal of a batch-1 voucher plus a parked reversal.
	mustSubmit(t, l, Batch{RequestID: "b2", Vouchers: []Voucher{
		reversal("v1-rev", "v1", Line{"cash", -1000}, Line{"revenue", 1000}),
		reversal("v3-rev", "v3", Line{"cash", -500}, Line{"revenue", 500}),
	}})
	// Batch 3: original for the parked reversal.
	mustSubmit(t, l, Batch{RequestID: "b3", Vouchers: []Voucher{
		voucher("v3", Line{"cash", 500}, Line{"revenue", -500}),
	}})
	// Conservation: every account nets out except ar/revenue from v2.
	want := map[string]Money{"cash": 0, "revenue": -3000, "ar": 3000}
	for acc, amt := range want {
		if l.Balance(acc) != amt {
			t.Fatalf("Balance(%s) = %d, want %d", acc, l.Balance(acc), amt)
		}
	}
	if !l.Reversed("v1") || !l.Reversed("v3") || l.Reversed("v2") {
		t.Fatal("reversal flags wrong")
	}
	mustVerify(t, l)
}

func TestVerifyDetectsNothingAfterMixedLoad(t *testing.T) {
	l := New()
	batches := []Batch{
		{RequestID: "r1", Vouchers: []Voucher{
			voucher("a", Line{"cash", 100}, Line{"equity", -100}),
			voucher("b", Line{"cash", 200}, Line{"equity", -200}),
		}},
		{RequestID: "r2", Vouchers: []Voucher{
			reversal("a-rev", "a", Line{"cash", -100}, Line{"equity", 100}),
			reversal("c-rev", "c", Line{"cash", -50}, Line{"equity", 50}),
		}},
		{RequestID: "r3", Vouchers: []Voucher{
			voucher("c", Line{"cash", 50}, Line{"equity", -50}),
		}},
	}
	for _, b := range batches {
		mustSubmit(t, l, b)
		mustVerify(t, l)
	}
	// Idempotent replays interleaved with real traffic stay consistent.
	for _, b := range batches {
		mustSubmit(t, l, b)
	}
	mustVerify(t, l)
	if l.Balance("cash") != 200 {
		t.Fatalf("cash = %d", l.Balance("cash"))
	}
}
