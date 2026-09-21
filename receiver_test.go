package seqwin

import (
	"reflect"
	"testing"
)

func mustReceiver(t *testing.T, bits uint, window uint64) *Receiver {
	t.Helper()
	r, err := NewReceiver(mustSpace(t, bits), window)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return r
}

func deliverAll(r *Receiver, seqs ...uint64) []Verdict {
	out := make([]Verdict, len(seqs))
	for i, s := range seqs {
		out[i] = r.Deliver(s)
	}
	return out
}

func TestReceiverWrapSequence(t *testing.T) {
	// Window straddling the wrap point: frontier starts at 254.
	r, err := NewReceiverAt(mustSpace(t, 8), 8, 254)
	if err != nil {
		t.Fatalf("NewReceiverAt: %v", err)
	}
	got := deliverAll(r, 254, 255, 0, 1)
	for i, v := range got {
		if v != AcceptedNew {
			t.Fatalf("delivery %d = %v, want accepted-new", i, v)
		}
	}
	if r.Next() != 2 {
		t.Errorf("Next = %d, want 2 (wrapped past 0)", r.Next())
	}
	if cum, ok := r.Cumulative(); !ok || cum != 1 {
		t.Errorf("Cumulative = %d,%v, want 1,true", cum, ok)
	}
	if r.Acked() != 4 {
		t.Errorf("Acked = %d, want 4", r.Acked())
	}
}

func TestDuplicateDoesNotGrowAckCount(t *testing.T) {
	r := mustReceiver(t, 8, 8)
	deliverAll(r, 5, 5, 5)
	if r.Acked() != 1 {
		t.Errorf("Acked = %d after 3 copies of seq 5, want 1", r.Acked())
	}
	if v := r.Deliver(5); v != Duplicate {
		t.Errorf("4th copy verdict = %v, want duplicate", v)
	}
	if r.Acked() != 1 {
		t.Errorf("Acked = %d after 4th copy, want 1", r.Acked())
	}
}

func TestRejectTooOld(t *testing.T) {
	r := mustReceiver(t, 8, 8)
	deliverAll(r, 0, 1, 2, 3, 4, 5, 6, 7) // frontier slides to 8
	if r.Next() != 8 {
		t.Fatalf("Next = %d, want 8", r.Next())
	}
	if v := r.Deliver(3); v != RejectedOld {
		t.Errorf("Deliver(3) = %v, want rejected-old", v)
	}
	if r.Acked() != 8 {
		t.Errorf("Acked = %d, want 8 (old packet must not count)", r.Acked())
	}
}

func TestRejectAmbiguousHalfDistance(t *testing.T) {
	r := mustReceiver(t, 8, 8)
	r.Deliver(0) // frontier at 1; distance to 129 is exactly 128
	if v := r.Deliver(129); v != RejectedAmbiguous {
		t.Errorf("Deliver(129) = %v, want rejected-ambiguous", v)
	}
	if r.Acked() != 1 {
		t.Errorf("Acked = %d, want 1 (ambiguous packet must not count)", r.Acked())
	}
}

func TestRejectFutureBeyondWindow(t *testing.T) {
	r := mustReceiver(t, 8, 4)
	if v := r.Deliver(10); v != RejectedFuture {
		t.Errorf("Deliver(10) = %v, want rejected-future", v)
	}
}

func TestRejectInvalidSequence(t *testing.T) {
	r := mustReceiver(t, 8, 8)
	if v := r.Deliver(256); v != RejectedInvalid {
		t.Errorf("Deliver(256) = %v, want rejected-invalid", v)
	}
}

func TestAckRangesCompact(t *testing.T) {
	r := mustReceiver(t, 8, 8)
	deliverAll(r, 0, 1, 2, 4, 5, 7) // gap at 3 and 6
	if r.Next() != 3 {
		t.Fatalf("Next = %d, want 3", r.Next())
	}
	want := []Range{{Start: 4, End: 5}, {Start: 7, End: 7}}
	if got := r.AckRanges(); !reflect.DeepEqual(got, want) {
		t.Errorf("AckRanges = %v, want %v", got, want)
	}
	// Filling the gap at 3 must extend the frontier and shrink the ranges.
	r.Deliver(3)
	if r.Next() != 6 {
		t.Errorf("Next = %d after filling gap, want 6", r.Next())
	}
	if got := r.AckRanges(); !reflect.DeepEqual(got, []Range{{Start: 7, End: 7}}) {
		t.Errorf("AckRanges = %v, want [7-7]", got)
	}
}

func TestAckRangesAcrossWrap(t *testing.T) {
	r, err := NewReceiverAt(mustSpace(t, 8), 8, 254)
	if err != nil {
		t.Fatalf("NewReceiverAt: %v", err)
	}
	deliverAll(r, 254, 255, 0, 2, 3) // frontier at 1, 2,3 buffered
	if r.Next() != 1 {
		t.Fatalf("Next = %d, want 1", r.Next())
	}
	want := []Range{{Start: 2, End: 3}}
	if got := r.AckRanges(); !reflect.DeepEqual(got, want) {
		t.Errorf("AckRanges = %v, want %v", got, want)
	}
	r.Deliver(1)
	if r.Next() != 4 {
		t.Errorf("Next = %d, want 4", r.Next())
	}
	if got := r.AckRanges(); len(got) != 0 {
		t.Errorf("AckRanges = %v, want empty", got)
	}
}

func TestOutOfOrderThenDuplicateAcrossSlide(t *testing.T) {
	r := mustReceiver(t, 8, 4)
	deliverAll(r, 1, 2, 3) // buffered, frontier still 0
	r.Deliver(0)           // slides frontier to 4
	if r.Next() != 4 {
		t.Fatalf("Next = %d, want 4", r.Next())
	}
	// 1 is now behind the window: rejected as old, not counted again.
	if v := r.Deliver(1); v != RejectedOld {
		t.Errorf("Deliver(1) after slide = %v, want rejected-old", v)
	}
	if r.Acked() != 4 {
		t.Errorf("Acked = %d, want 4", r.Acked())
	}
}
