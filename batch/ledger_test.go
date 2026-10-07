package batch

import (
	"reflect"
	"strings"
	"testing"
)

func mustInbound(t *testing.T, l *Ledger, qty int) (string, string) {
	t.Helper()
	b, op, err := l.Inbound(qty)
	if err != nil {
		t.Fatalf("Inbound(%d): %v", qty, err)
	}
	return b, op
}

func mustRemaining(t *testing.T, l *Ledger, id string, want int) {
	t.Helper()
	got, ok := l.Remaining(id)
	if !ok {
		t.Fatalf("batch %s should be live", id)
	}
	if got != want {
		t.Fatalf("batch %s remaining = %d, want %d", id, got, want)
	}
}

func mustVerify(t *testing.T, l *Ledger) {
	t.Helper()
	if err := l.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestPartialSplit(t *testing.T) {
	l := NewLedger()
	b1, _ := mustInbound(t, l, 100)

	kids, opID, err := l.Split(b1, []int{30, 40})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(kids) != 2 {
		t.Fatalf("expected 2 children, got %d", len(kids))
	}
	mustRemaining(t, l, b1, 30) // unspent remainder stays on the source
	mustRemaining(t, l, kids[0], 30)
	mustRemaining(t, l, kids[1], 40)

	// Source graph: split op consumes b1 and produces both children.
	op, ok := l.Op(opID)
	if !ok || op.Kind != OpSplit {
		t.Fatalf("op %s missing or wrong kind", opID)
	}
	if !reflect.DeepEqual(op.Inputs, []Portion{{BatchID: b1, Qty: 70}}) {
		t.Fatalf("split inputs = %+v", op.Inputs)
	}
	if got := l.Consumers(b1); !reflect.DeepEqual(got, []string{opID}) {
		t.Fatalf("consumers of %s = %v", b1, got)
	}

	// Split the remainder away.
	kids2, _, err := l.Split(b1, []int{30})
	if err != nil {
		t.Fatalf("second Split: %v", err)
	}
	mustRemaining(t, l, b1, 0)
	mustRemaining(t, l, kids2[0], 30)
	mustVerify(t, l)
}

func TestMergeSharing(t *testing.T) {
	l := NewLedger()
	b1, _ := mustInbound(t, l, 100)
	b2, _ := mustInbound(t, l, 50)

	// Two merges share b1 as a source, each taking a partial portion.
	m1, op1, err := l.Merge([]Portion{{BatchID: b1, Qty: 20}, {BatchID: b2, Qty: 10}})
	if err != nil {
		t.Fatalf("Merge 1: %v", err)
	}
	m2, _, err := l.Merge([]Portion{{BatchID: b1, Qty: 30}})
	if err != nil {
		t.Fatalf("Merge 2: %v", err)
	}
	mustRemaining(t, l, b1, 50)
	mustRemaining(t, l, b2, 40)
	mustRemaining(t, l, m1, 30)
	mustRemaining(t, l, m2, 30)

	// Undoing the first merge restores only its own portions; the shared
	// source keeps the second merge's deduction.
	if err := l.Undo(op1); err != nil {
		t.Fatalf("Undo merge 1: %v", err)
	}
	mustRemaining(t, l, b1, 70)
	mustRemaining(t, l, b2, 50)
	mustRemaining(t, l, m2, 30)
	if _, ok := l.Remaining(m1); ok {
		t.Fatalf("merged batch %s should be revoked after undo", m1)
	}
	mustVerify(t, l)
}

func TestAtomicFailure(t *testing.T) {
	l := NewLedger()
	b1, _ := mustInbound(t, l, 100)
	b2, _ := mustInbound(t, l, 5)

	// Merge fails on the second source; the first source must be untouched.
	_, _, err := l.Merge([]Portion{{BatchID: b1, Qty: 50}, {BatchID: b2, Qty: 10}})
	ins, ok := err.(*InsufficientError)
	if !ok {
		t.Fatalf("expected *InsufficientError, got %v", err)
	}
	if ins.BatchID != b2 || ins.Want != 10 || ins.Have != 5 {
		t.Fatalf("unexpected InsufficientError: %+v", ins)
	}
	mustRemaining(t, l, b1, 100)
	mustRemaining(t, l, b2, 5)

	// Split whose parts exceed the remainder fails without side effects.
	if _, _, err := l.Split(b1, []int{60, 50}); err == nil {
		t.Fatal("oversized split should fail")
	}
	mustRemaining(t, l, b1, 100)

	// Non-positive and duplicate inputs are rejected before any mutation.
	if _, _, err := l.Split(b1, []int{10, 0}); err != ErrNonPositiveQty {
		t.Fatalf("zero part: %v", err)
	}
	if _, _, err := l.Merge([]Portion{{BatchID: b1, Qty: 10}, {BatchID: b1, Qty: 5}}); err != ErrDuplicateSource {
		t.Fatalf("duplicate source: %v", err)
	}
	if _, err := l.Outbound(b1, 0); err != ErrNonPositiveQty {
		t.Fatalf("zero outbound: %v", err)
	}
	if _, err := l.Outbound(b1, 101); err == nil {
		t.Fatal("oversized outbound should fail")
	}
	mustRemaining(t, l, b1, 100)
	mustRemaining(t, l, b2, 5)
	mustVerify(t, l)
}

func TestUndoAncestorBlockedByConsumedDescendant(t *testing.T) {
	l := NewLedger()
	b1, opIn := mustInbound(t, l, 100)
	kids, opSplit, err := l.Split(b1, []int{60, 40})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	b2 := kids[0]
	opOut, err := l.Outbound(b2, 20)
	if err != nil {
		t.Fatalf("Outbound: %v", err)
	}
	// Fresh stock elsewhere makes the ledger-wide total reach 100 again:
	// 40 (b2) + 40 (b3) + 20 (b4). Undo must still be rejected, because
	// lineage, not totals, decides.
	b4, _ := mustInbound(t, l, 20)
	_ = b4

	err = l.Undo(opIn)
	ue, ok := err.(*UndoError)
	if !ok {
		t.Fatalf("expected *UndoError, got %v", err)
	}
	wantChain := []string{opIn, b1, opSplit, b2, opOut}
	if !reflect.DeepEqual(ue.Chain, wantChain) {
		t.Fatalf("chain = %v, want %v", ue.Chain, wantChain)
	}

	// The split itself is blocked too: b2 was partially consumed even
	// though b2+b3 together still hold enough.
	err = l.Undo(opSplit)
	ue, ok = err.(*UndoError)
	if !ok {
		t.Fatalf("expected *UndoError, got %v", err)
	}
	if !reflect.DeepEqual(ue.Chain, []string{opSplit, b2, opOut}) {
		t.Fatalf("chain = %v", ue.Chain)
	}
	if !strings.Contains(ue.Error(), opOut) {
		t.Fatalf("error message should mention the blocking op: %v", ue)
	}
	mustVerify(t, l)
}

func TestUndoOrderDescendantsFirst(t *testing.T) {
	l := NewLedger()
	b1, opIn := mustInbound(t, l, 100)
	kids, opSplit, err := l.Split(b1, []int{60, 40})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	merged, opMerge, err := l.Merge([]Portion{{BatchID: kids[0], Qty: 60}, {BatchID: kids[1], Qty: 40}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Ancestors are blocked while the merge depends on them.
	if _, ok := l.Undo(opIn).(*UndoError); !ok {
		t.Fatal("undo inbound should be blocked by descendant merge")
	}
	if _, ok := l.Undo(opSplit).(*UndoError); !ok {
		t.Fatal("undo split should be blocked by descendant merge")
	}

	// Undo descendants first, then ancestors become legal.
	if err := l.Undo(opMerge); err != nil {
		t.Fatalf("Undo merge: %v", err)
	}
	if _, ok := l.Remaining(merged); ok {
		t.Fatal("merged batch should be revoked")
	}
	if err := l.Undo(opSplit); err != nil {
		t.Fatalf("Undo split: %v", err)
	}
	mustRemaining(t, l, b1, 100)
	if err := l.Undo(opIn); err != nil {
		t.Fatalf("Undo inbound: %v", err)
	}
	if _, ok := l.Remaining(b1); ok {
		t.Fatal("inbound batch should be revoked")
	}

	// Double undo and unknown op are errors.
	if err := l.Undo(opIn); err != ErrAlreadyUndone {
		t.Fatalf("double undo: %v", err)
	}
	if err := l.Undo("OP999"); err != ErrUnknownOp {
		t.Fatalf("unknown op: %v", err)
	}
	mustVerify(t, l)
}

func TestUndoOutboundRestores(t *testing.T) {
	l := NewLedger()
	b1, _ := mustInbound(t, l, 50)
	opOut, err := l.Outbound(b1, 20)
	if err != nil {
		t.Fatalf("Outbound: %v", err)
	}
	mustRemaining(t, l, b1, 30)
	if err := l.Undo(opOut); err != nil {
		t.Fatalf("Undo outbound: %v", err)
	}
	mustRemaining(t, l, b1, 50)
	mustVerify(t, l)
}

func TestVerifyAfterMixedFlow(t *testing.T) {
	l := NewLedger()
	b1, _ := mustInbound(t, l, 200)
	b2, _ := mustInbound(t, l, 80)
	kids, _, err := l.Split(b1, []int{70, 50})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	m, opMerge, err := l.Merge([]Portion{
		{BatchID: kids[0], Qty: 40},
		{BatchID: kids[1], Qty: 50},
		{BatchID: b2, Qty: 30},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := l.Outbound(m, 60); err != nil {
		t.Fatalf("Outbound: %v", err)
	}
	mustRemaining(t, l, b1, 80)
	mustRemaining(t, l, b2, 50)
	mustRemaining(t, l, kids[0], 30)
	mustRemaining(t, l, m, 60)
	mustVerify(t, l)

	// Undoing the merge is blocked by the outbound on its output.
	if _, ok := l.Undo(opMerge).(*UndoError); !ok {
		t.Fatal("undo merge should be blocked by outbound on its output")
	}
	mustVerify(t, l)
}
