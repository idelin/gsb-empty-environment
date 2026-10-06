package batchrun

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Unix(1000, 0)

func at(sec int64) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func TestAcquireSplitsIntoChunks(t *testing.T) {
	tr := NewTracker(10, 4, time.Minute)
	var got []Range
	for i := 0; i < 3; i++ {
		l, ok := tr.Acquire("w", at(0))
		if !ok {
			t.Fatalf("acquire %d failed", i)
		}
		got = append(got, l.Range)
	}
	want := []Range{{0, 4}, {4, 8}, {8, 10}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chunks = %v, want %v", got, want)
	}
	if _, ok := tr.Acquire("w", at(0)); ok {
		t.Fatal("expected no more work while everything is leased")
	}
}

func TestPrefixConfirmThenFailRedispatchesOnlyUnconfirmed(t *testing.T) {
	tr := NewTracker(10, 10, time.Minute)
	l, _ := tr.Acquire("w1", at(0))

	// Worker confirms the prefix [0, 4) then fails.
	if err := tr.Confirm(l.ID, "w1", 4, at(1)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := tr.Release(l.ID, "w1", at(1)); err != nil {
		t.Fatalf("release: %v", err)
	}

	l2, ok := tr.Acquire("w2", at(2))
	if !ok {
		t.Fatal("expected redispatch of unconfirmed tail")
	}
	if l2.Range != (Range{4, 10}) {
		t.Fatalf("redispatched range = %v, want [4,10)", l2.Range)
	}
	if c, total := tr.Progress(); c != 4 || total != 10 {
		t.Fatalf("progress = %d/%d, want 4/10", c, total)
	}
}

func TestOutOfOrderReceiptsMergeInUnion(t *testing.T) {
	tr := NewTracker(12, 4, time.Minute)
	l1, _ := tr.Acquire("a", at(0)) // [0,4)
	l2, _ := tr.Acquire("b", at(0)) // [4,8)
	l3, _ := tr.Acquire("c", at(0)) // [8,12)

	// Receipts arrive out of order.
	for _, op := range []struct {
		id   uint64
		w    string
		upto int64
	}{{l3.ID, "c", 12}, {l1.ID, "a", 4}, {l2.ID, "b", 8}} {
		if err := tr.Confirm(op.id, op.w, op.upto, at(1)); err != nil {
			t.Fatalf("confirm %v: %v", op, err)
		}
	}
	if got := tr.Confirmed(); !reflect.DeepEqual(got, []Range{{0, 12}}) {
		t.Fatalf("confirmed union = %v, want [[0,12)]", got)
	}
	if !tr.Complete() {
		t.Fatal("expected complete after all receipts merged")
	}
}

func TestOverlappingConfirmationsAreIdempotent(t *testing.T) {
	tr := NewTracker(10, 6, time.Minute)
	l1, _ := tr.Acquire("w1", at(0)) // [0,6)
	if err := tr.Confirm(l1.ID, "w1", 6, at(1)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Duplicate delivery of the same confirmation: no-op, no error.
	if err := tr.Confirm(l1.ID, "w1", 6, at(2)); err != nil {
		t.Fatalf("duplicate confirm should be idempotent, got %v", err)
	}
	// A later lease overlapping already-confirmed work confirms it again.
	l2, _ := tr.Acquire("w2", at(2)) // [6,10)
	if err := tr.Confirm(l2.ID, "w2", 10, at(3)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if c, _ := tr.Progress(); c != 10 {
		t.Fatalf("confirmed = %d, want 10 (no double counting)", c)
	}
	if !tr.Complete() {
		t.Fatal("expected complete")
	}
}

func TestOverlapAcrossLeaseHandover(t *testing.T) {
	tr := NewTracker(10, 10, time.Minute)
	l1, _ := tr.Acquire("w1", at(0)) // [0,10)
	// w1 confirms [0,7), then its lease expires before the tail is done.
	if err := tr.Confirm(l1.ID, "w1", 7, at(1)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if n := tr.Expire(at(61)); n != 1 {
		t.Fatalf("expired %d leases, want 1", n)
	}
	// w2 picks up only the unconfirmed tail [7,10).
	l2, ok := tr.Acquire("w2", at(61))
	if !ok || l2.Range != (Range{7, 10}) {
		t.Fatalf("reacquired = %+v ok=%v, want [7,10)", l2, ok)
	}
	// Late receipt from the old lease is rejected: it was reclaimed.
	if err := tr.Confirm(l1.ID, "w1", 9, at(62)); err != ErrUnknownLease {
		t.Fatalf("stale confirm err = %v, want ErrUnknownLease", err)
	}
	if err := tr.Confirm(l2.ID, "w2", 10, at(63)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !tr.Complete() {
		t.Fatal("expected complete after handover")
	}
}

func TestOutOfBoundsConfirmationRejected(t *testing.T) {
	tr := NewTracker(10, 4, time.Minute)
	l, _ := tr.Acquire("w", at(0)) // [0,4)

	if err := tr.Confirm(l.ID, "w", 5, at(1)); err != ErrOutOfBounds {
		t.Fatalf("err = %v, want ErrOutOfBounds", err)
	}
	if err := tr.Confirm(l.ID, "w", -1, at(1)); err != ErrOutOfBounds {
		t.Fatalf("err = %v, want ErrOutOfBounds", err)
	}
	// State untouched by the rejected receipts.
	if c, _ := tr.Progress(); c != 0 {
		t.Fatalf("confirmed = %d, want 0", c)
	}
	if got := tr.Outstanding(); !reflect.DeepEqual(got, []Range{{0, 4}}) {
		t.Fatalf("outstanding = %v, want [[0,4)]", got)
	}
}

func TestWrongWorkerRejected(t *testing.T) {
	tr := NewTracker(10, 4, time.Minute)
	l, _ := tr.Acquire("w1", at(0))
	if err := tr.Confirm(l.ID, "w2", 4, at(1)); err != ErrNotHolder {
		t.Fatalf("err = %v, want ErrNotHolder", err)
	}
	if err := tr.Release(l.ID, "w2", at(1)); err != ErrNotHolder {
		t.Fatalf("err = %v, want ErrNotHolder", err)
	}
}

func TestCompleteRequiresFullCoverage(t *testing.T) {
	tr := NewTracker(9, 3, time.Minute)
	leases := make([]Lease, 0, 3)
	for i := 0; i < 3; i++ {
		l, _ := tr.Acquire("w", at(0))
		leases = append(leases, l)
	}
	for i, l := range leases {
		if tr.Complete() {
			t.Fatalf("complete after %d confirmations, want incomplete", i)
		}
		if err := tr.Confirm(l.ID, "w", l.Range.Hi, at(1)); err != nil {
			t.Fatalf("confirm: %v", err)
		}
	}
	if !tr.Complete() {
		t.Fatal("expected complete once the union covers [0,9)")
	}
}

func TestIntervalSetAddMergesAdjacentAndOverlapping(t *testing.T) {
	var s intervalSet
	for _, r := range []Range{{8, 10}, {2, 4}, {4, 6}, {0, 2}, {6, 8}, {3, 7}} {
		s.add(r)
	}
	if !reflect.DeepEqual(s.ivs, []Range{{0, 10}}) {
		t.Fatalf("union = %v, want [[0,10)]", s.ivs)
	}
	if !s.covers(Range{0, 10}) || s.covers(Range{0, 11}) {
		t.Fatal("covers mismatch")
	}
	if n := s.covered(); n != 10 {
		t.Fatalf("covered = %d, want 10", n)
	}
	if gaps := s.gaps(Range{0, 12}); !reflect.DeepEqual(gaps, []Range{{10, 12}}) {
		t.Fatalf("gaps = %v, want [[10,12)]", gaps)
	}
}
