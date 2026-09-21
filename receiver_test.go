package seqwin

import (
	"errors"
	"reflect"
	"testing"
)

func mustRecv(t *testing.T, r *Receiver, seq uint32, wantAccepted bool) {
	t.Helper()
	accepted, err := r.Receive(seq)
	if err != nil || accepted != wantAccepted {
		t.Fatalf("Receive(%d) = %v, %v; want %v, nil", seq, accepted, err, wantAccepted)
	}
}

// Sequence numbers 254, 255, 0, 1 are accepted in order across the wrap.
func TestWraparoundAccept(t *testing.T) {
	r, err := NewReceiver(space8, 254, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []uint32{254, 255, 0, 1} {
		mustRecv(t, r, s, true)
	}
	if r.Next() != 2 {
		t.Fatalf("Next() = %d; want 2", r.Next())
	}
	if r.Count() != 4 {
		t.Fatalf("Count() = %d; want 4", r.Count())
	}
	want := []Range{{Start: 254, End: 1}}
	if got := r.AckRanges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AckRanges() = %v; want %v", got, want)
	}
}

// Packets behind the cumulative ack point are rejected as too old;
// packets at or beyond window distance are rejected as too far ahead.
func TestRejectTooOldAndTooFar(t *testing.T) {
	r, _ := NewReceiver(space8, 0, 8)
	for _, s := range []uint32{0, 1, 2} {
		mustRecv(t, r, s, true)
	}
	if _, err := r.Receive(250); !errors.Is(err, ErrTooOld) {
		t.Fatalf("Receive(250) err = %v; want ErrTooOld", err)
	}
	if _, err := r.Receive(2); !errors.Is(err, ErrTooOld) {
		t.Fatalf("Receive(2) err = %v; want ErrTooOld", err)
	}
	if _, err := r.Receive(3 + 8); !errors.Is(err, ErrTooFarAhead) {
		t.Fatalf("Receive(11) err = %v; want ErrTooFarAhead", err)
	}
	if r.Count() != 3 {
		t.Fatalf("Count() = %d; want 3 (rejected packets not counted)", r.Count())
	}
}

// Duplicate packets are idempotent: not an error, but never counted twice.
func TestDuplicateNotCounted(t *testing.T) {
	r, _ := NewReceiver(space8, 0, 8)
	mustRecv(t, r, 0, true)
	mustRecv(t, r, 2, true)  // gap at 1
	mustRecv(t, r, 2, false) // duplicate
	mustRecv(t, r, 2, false) // duplicate again
	if r.Count() != 2 {
		t.Fatalf("Count() = %d; want 2", r.Count())
	}
	mustRecv(t, r, 1, true) // fills the gap, absorbs 2
	// A retransmitted cumulative packet is behind the ack point: too old.
	if _, err := r.Receive(1); !errors.Is(err, ErrTooOld) {
		t.Fatalf("Receive(1) err = %v; want ErrTooOld", err)
	}
	if r.Count() != 3 {
		t.Fatalf("Count() = %d; want 3", r.Count())
	}
	if r.Next() != 3 {
		t.Fatalf("Next() = %d; want 3", r.Next())
	}
}

// Gaps are reported as compact inclusive ranges and coalesce as they fill.
func TestGapRanges(t *testing.T) {
	r, _ := NewReceiver(space8, 0, 8)
	for _, s := range []uint32{0, 1, 3, 4, 6} {
		mustRecv(t, r, s, true)
	}
	want := []Range{{0, 1}, {3, 4}, {6, 6}}
	if got := r.AckRanges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AckRanges() = %v; want %v", got, want)
	}
	mustRecv(t, r, 2, true)
	want = []Range{{0, 4}, {6, 6}}
	if got := r.AckRanges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AckRanges() = %v; want %v", got, want)
	}
	mustRecv(t, r, 5, true)
	want = []Range{{0, 6}}
	if got := r.AckRanges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AckRanges() = %v; want %v", got, want)
	}
}

// A window larger than half the sequence space is refused, and a packet at
// exactly half-space distance cannot be classified.
func TestWindowBoundedByHalfSpace(t *testing.T) {
	if _, err := NewReceiver(space8, 0, 129); err == nil {
		t.Fatal("NewReceiver with window 129: want error")
	}
	r, _ := NewReceiver(space8, 0, 128)
	if _, err := r.Receive(128); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Receive(128) err = %v; want ErrAmbiguous", err)
	}
}
