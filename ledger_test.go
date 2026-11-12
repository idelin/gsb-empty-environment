package netsec

import (
	"errors"
	"sync"
	"testing"
)

func digestOf(s string) [32]byte {
	return Digest([]byte(s))
}

func mustPublish(t *testing.T, l *Ledger, planID, receiptID string, cost int64, result string) *Receipt {
	t.Helper()
	r, err := l.Publish(PublishInput{
		PlanID:       planID,
		ReceiptID:    receiptID,
		Cost:         cost,
		ResultDigest: digestOf(result),
	})
	if err != nil {
		t.Fatalf("Publish(%s): %v", receiptID, err)
	}
	return r
}

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func TestFixedSequenceLifecycle(t *testing.T) {
	l := NewLedger(100)
	root, err := l.NewPlan("root", 30, 3)
	if err != nil {
		t.Fatal(err)
	}
	if root.Kind != Fixed || root.State != Reserved {
		t.Fatalf("unexpected root %+v", root)
	}

	r1 := mustPublish(t, l, "root", "r1", 5, "result-1")
	r2 := mustPublish(t, l, "root", "r2", 7, "result-2")
	if r2.ParentDigest != r1.ResultDigest {
		t.Fatal("step 2 must bind to step 1 result digest")
	}
	if r1.ParentDigest != ZeroDigest {
		t.Fatal("root first receipt must bind ZeroDigest")
	}

	mid, err := l.Plan("root")
	if err != nil {
		t.Fatal(err)
	}
	if mid.State != Active || mid.Published != 2 || mid.Spent != 12 {
		t.Fatalf("unexpected mid plan %+v", mid)
	}

	r3 := mustPublish(t, l, "root", "r3", 9, "result-3")
	done, _ := l.Plan("root")
	if done.State != Completed {
		t.Fatalf("fixed plan must complete after declared steps, got %s", done.State)
	}
	if len(r3.Path) != 1 || r3.Path[0].PlanID != "root" || r3.Path[0].ReceiptID != "r3" {
		t.Fatalf("unexpected path %+v", r3.Path)
	}

	_, err = l.Publish(PublishInput{PlanID: "root", ReceiptID: "r4", Cost: 1, ResultDigest: digestOf("x")})
	wantErrIs(t, err, ErrStepCount)

	_, err = l.OpenBranch("b", "root", "r1", 5)
	wantErrIs(t, err, ErrFixedBranch)

	if l.TotalSpent() != 21 {
		t.Fatalf("total spent = %d, want 21", l.TotalSpent())
	}
}

// Forked retries share the parent reservation. A failed branch that never
// published releases its reservation, while a published branch that is
// later canceled forfeits its unspent budget. Total realized cost stays
// bounded and each receipt is attributed to its own path.
func TestForkRetryCostBoundAndAttribution(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 40, 0); err != nil {
		t.Fatal(err)
	}
	r1 := mustPublish(t, l, "root", "r1", 10, "root-1")

	bad, err := l.OpenBranch("bad", "root", "r1", 12)
	if err != nil {
		t.Fatal(err)
	}
	good, err := l.OpenBranch("good", "root", "r1", 15)
	if err != nil {
		t.Fatal(err)
	}

	rootAfterFork, _ := l.Plan("root")
	// root reserve 40, spent 10, locked by branches 12+15 => available 3.
	if rootAfterFork.Available != 3 {
		t.Fatalf("root available = %d, want 3", rootAfterFork.Available)
	}
	if bad.State != Reserved || good.State != Reserved {
		t.Fatal("new branches must start Reserved")
	}

	// Unpublished branch releases everything back to the parent.
	if err := l.Release("bad"); err != nil {
		t.Fatal(err)
	}
	rootAfterRelease, _ := l.Plan("root")
	if rootAfterRelease.Available != 15 {
		t.Fatalf("root available after release = %d, want 15", rootAfterRelease.Available)
	}

	// The good branch publishes, then cancels; unspent budget is forfeited.
	g1 := mustPublish(t, l, "good", "g1", 6, "good-1")
	if g1.ParentDigest != r1.ResultDigest || g1.ParentPlan != "root" || g1.ParentReceipt != "r1" {
		t.Fatalf("branch first receipt must bind parent result: %+v", g1)
	}
	if len(g1.Path) != 2 || g1.Path[0].ReceiptID != "r1" || g1.Path[1].ReceiptID != "g1" {
		t.Fatalf("branch path must extend parent lineage: %+v", g1.Path)
	}
	if err := l.Cancel("good"); err != nil {
		t.Fatal(err)
	}
	canceled, _ := l.Plan("good")
	if canceled.State != Canceled || canceled.Available != 0 || canceled.Spent != 6 {
		t.Fatalf("unexpected canceled branch %+v", canceled)
	}

	// Cancellation does not refund the parent; only the released branch did.
	rootFinal, _ := l.Plan("root")
	if rootFinal.Available != 15 {
		t.Fatalf("root available after cancel = %d, want 15 (no refund)", rootFinal.Available)
	}

	if l.TotalSpent() != 16 {
		t.Fatalf("total spent = %d, want 16", l.TotalSpent())
	}
	if l.RootReserved() > l.Budget() {
		t.Fatal("root reservation must stay within global budget")
	}

	if err := l.Release("good"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("release after publish must fail, got %v", err)
	}
}

// Re-publishing the same receipt ID is idempotent: no double charge, the
// stored receipt (including seq and digest) is returned unchanged.
func TestDuplicateReceiptIdempotent(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 50, 0); err != nil {
		t.Fatal(err)
	}
	first := mustPublish(t, l, "root", "r1", 8, "result")
	again, err := l.Publish(PublishInput{
		PlanID:       "root",
		ReceiptID:    "r1",
		Cost:         999, // ignored on retry
		ResultDigest: digestOf("different-attempt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Seq != first.Seq || again.Cost != 8 || again.ResultDigest != first.ResultDigest {
		t.Fatalf("retry must return the stored receipt: first=%+v again=%+v", first, again)
	}
	if l.TotalSpent() != 8 {
		t.Fatalf("retry inflated spend to %d", l.TotalSpent())
	}
	plan, _ := l.Plan("root")
	if plan.Published != 1 || plan.Spent != 8 {
		t.Fatalf("retry changed plan state: %+v", plan)
	}

	// Same result digest under a new receipt id is a replay and rejected.
	_, err = l.Publish(PublishInput{PlanID: "root", ReceiptID: "r2", Cost: 2, ResultDigest: digestOf("result")})
	wantErrIs(t, err, ErrDuplicateReceipt)
}

// Branch reservations can never exceed the parent's available budget, and
// charges can never exceed what the plan can still spend.
func TestBudgetUpperBound(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 60, 0); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, l, "root", "r1", 20, "root-1")

	_, err := l.OpenBranch("too-big", "root", "r1", 41)
	wantErrIs(t, err, ErrBudgetExceeded)

	if _, err := l.OpenBranch("child", "root", "r1", 25); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, l, "child", "c1", 10, "child-1")

	_, err = l.Publish(PublishInput{PlanID: "child", ReceiptID: "c2", Cost: 16, ResultDigest: digestOf("child-2")})
	wantErrIs(t, err, ErrCostExceeded)

	_, err = l.OpenBranch("sibling", "root", "r1", 16)
	wantErrIs(t, err, ErrBudgetExceeded)

	_, err = l.NewPlan("other", 50, 0)
	wantErrIs(t, err, ErrBudgetExceeded)

	if l.TotalSpent() > l.RootReserved() || l.RootReserved() > l.Budget() {
		t.Fatalf("invariant broken: spent=%d rootReserved=%d budget=%d",
			l.TotalSpent(), l.RootReserved(), l.Budget())
	}
}

// Nested branches only consume their parent's reserved budget and the
// receipt path records the full fork lineage for attribution.
func TestNestedBranchPathAttribution(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 80, 0); err != nil {
		t.Fatal(err)
	}
	r1 := mustPublish(t, l, "root", "r1", 10, "root-1")
	if _, err := l.OpenBranch("mid", "root", "r1", 50); err != nil {
		t.Fatal(err)
	}
	m1 := mustPublish(t, l, "mid", "m1", 10, "mid-1")
	if _, err := l.OpenBranch("leaf", "mid", "m1", 30); err != nil {
		t.Fatal(err)
	}
	l1 := mustPublish(t, l, "leaf", "l1", 12, "leaf-1")

	want := []PathHop{
		{PlanID: "root", ReceiptID: "r1"},
		{PlanID: "mid", ReceiptID: "m1"},
		{PlanID: "leaf", ReceiptID: "l1"},
	}
	if len(l1.Path) != len(want) {
		t.Fatalf("path len = %d, want %d", len(l1.Path), len(want))
	}
	for i, hop := range want {
		if l1.Path[i].PlanID != hop.PlanID || l1.Path[i].ReceiptID != hop.ReceiptID {
			t.Fatalf("path hop %d = %+v, want %+v", i, l1.Path[i], hop)
		}
	}
	if l1.ParentDigest != m1.ResultDigest || m1.ParentDigest != r1.ResultDigest {
		t.Fatal("nested receipts must chain parent result digests")
	}

	midPlan, _ := l.Plan("mid")
	if midPlan.Available != 10 {
		t.Fatalf("mid available = %d, want 10", midPlan.Available)
	}

	if err := l.Finish("mid"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("finish with open branches must fail, got %v", err)
	}
	if err := l.Finish("leaf"); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish("mid"); err != nil {
		t.Fatal(err)
	}
	midDone, _ := l.Plan("mid")
	if midDone.State != Completed || midDone.Available != 0 {
		t.Fatalf("unexpected finished mid %+v", midDone)
	}
}

// Branching requires a published parent result and respects plan state.
func TestBranchGuards(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 50, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.OpenBranch("b", "root", "missing", 10); !errors.Is(err, ErrNoParentResult) {
		t.Fatalf("branch without parent result must fail, got %v", err)
	}
	mustPublish(t, l, "root", "r1", 10, "root-1")
	if _, err := l.OpenBranch("b", "ghost", "r1", 10); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("branch from unknown parent must fail, got %v", err)
	}
	if _, err := l.OpenBranch("b", "root", "r1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Publish(PublishInput{PlanID: "b", ReceiptID: "x", Cost: 0, ResultDigest: digestOf("z")}); !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("non-positive cost must fail, got %v", err)
	}
	if err := l.Cancel("b"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cancel unpublished plan must fail, got %v", err)
	}
}

// Concurrent duplicate publishes and competing branches must keep the cost
// bound and idempotency guarantees intact.
func TestConcurrentDuplicateAndForks(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 90, 0); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, l, "root", "r1", 10, "root-1")

	var wg sync.WaitGroup
	// Many goroutines retry the same receipt id: exactly one charge wins.
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = l.Publish(PublishInput{
				PlanID:       "root",
				ReceiptID:    "dup",
				Cost:         7,
				ResultDigest: digestOf("dup-result"),
			})
		}()
	}
	// Many goroutines race to reserve disjoint branches; only those within
	// the parent's available 80 may succeed.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = l.OpenBranch(
				"branch-"+itoa(n), "root", "r1", 10,
			)
		}(i)
	}
	wg.Wait()

	if got := l.TotalSpent(); got != 17 {
		t.Fatalf("total spent = %d, want 17 (idempotent retry)", got)
	}
	dup, err := l.Receipt("dup")
	if err != nil {
		t.Fatal(err)
	}
	if dup.Cost != 7 || dup.Seq != 2 {
		t.Fatalf("unexpected dup receipt %+v", dup)
	}

	// Every accepted branch reservation is funded by the parent and the
	// global bound is never violated.
	var branchReserve int64
	for _, p := range l.Plans() {
		if p.ParentID == "root" && p.State != Released {
			branchReserve += p.Reserve
		}
	}
	root, _ := l.Plan("root")
	// root: reserve 90, spent 17, the rest is either branch-locked or free.
	if root.Spent+branchReserve > root.Reserve {
		t.Fatalf("over-forked: spent %d + branches %d > reserve %d",
			root.Spent, branchReserve, root.Reserve)
	}
	if l.RootReserved() > l.Budget() {
		t.Fatal("global budget exceeded under concurrency")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestVerifyReceiptChain(t *testing.T) {
	l := NewLedger(100)
	if _, err := l.NewPlan("root", 80, 0); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, l, "root", "r1", 10, "root-1")
	if _, err := l.OpenBranch("mid", "root", "r1", 50); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, l, "mid", "m1", 8, "mid-1")
	mustPublish(t, l, "mid", "m2", 8, "mid-2")

	for _, id := range []string{"r1", "m1", "m2"} {
		if err := l.VerifyReceipt(id); err != nil {
			t.Fatalf("VerifyReceipt(%s): %v", id, err)
		}
	}
	if err := l.VerifyReceipt("missing"); !errors.Is(err, ErrUnknownReceipt) {
		t.Fatalf("verify unknown receipt must fail, got %v", err)
	}

	// Tamper with a returned snapshot; the ledger's stored record must stay
	// valid and a corrupt external copy is detectable by re-fetching.
	tampered, _ := l.Receipt("m2")
	tampered.ParentDigest = digestOf("forged")
	fresh, _ := l.Receipt("m2")
	if fresh.ParentDigest == tampered.ParentDigest {
		t.Fatal("ledger must return defensive copies, stored receipt was mutated")
	}
	if err := l.VerifyReceipt("m2"); err != nil {
		t.Fatalf("stored receipt should remain valid: %v", err)
	}
}
