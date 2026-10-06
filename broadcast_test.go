package bcast

import (
	"reflect"
	"testing"
)

func mustPublish(t *testing.T, q *Queue, payload interface{}) uint64 {
	t.Helper()
	seq, err := q.Publish(payload)
	if err != nil {
		t.Fatalf("Publish(%v): %v", payload, err)
	}
	return seq
}

// Slow consumer holds the message; capacity is only released once its
// receive-set ledger entry is cleared (here by leaving, which acks unread).
func TestSlowConsumerBlocksReclaim(t *testing.T) {
	q := New(2)
	fast := q.Subscribe()
	slow := q.Subscribe()

	s1 := mustPublish(t, q, "m1")
	if ids, _ := q.Pending(s1); !reflect.DeepEqual(ids, []uint64{fast.id, slow.id}) {
		t.Fatalf("pending after publish = %v", ids)
	}

	// Fast consumer receives and acks; message must survive for the slow one.
	if seq, v, ok := fast.Recv(); !ok || seq != s1 || v != "m1" {
		t.Fatalf("fast Recv = %d,%v,%v", seq, v, ok)
	}
	fast.Ack(s1)
	if got := q.Reclaimed(); len(got) != 0 {
		t.Fatalf("reclaimed too early: %v", got)
	}
	if ids, _ := q.Pending(s1); !reflect.DeepEqual(ids, []uint64{slow.id}) {
		t.Fatalf("pending after fast ack = %v", ids)
	}

	mustPublish(t, q, "m2")
	if _, err := q.Publish("m3"); err != ErrFull {
		t.Fatalf("publish at capacity: err = %v", err)
	}

	// Slow consumer leaves: its unread items count as acked, freeing a slot.
	slow.Unsubscribe()
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{s1}) {
		t.Fatalf("reclaimed = %v", got)
	}
	if q.Used() != 1 {
		t.Fatalf("used = %d, want 1", q.Used())
	}
	mustPublish(t, q, "m3")
}

// A subscriber that leaves and rejoins must not see messages committed
// before its (re)join, and its departure drains its ledger entries.
func TestExitRejoinNoBackfill(t *testing.T) {
	q := New(4)
	a := q.Subscribe()
	s1 := mustPublish(t, q, "before-leave")

	// Rejoin while s1 is still live: the new registration must not see it.
	a.Unsubscribe()
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{s1}) {
		t.Fatalf("reclaimed = %v", got)
	}
	a2 := q.Subscribe()
	if _, _, ok := a2.Recv(); ok {
		t.Fatal("rejoined subscriber saw a message committed before its join")
	}

	s2 := mustPublish(t, q, "after-join")
	seq, v, ok := a2.Recv()
	if !ok || seq != s2 || v != "after-join" {
		t.Fatalf("Recv = %d,%v,%v; want %d,after-join,true", seq, v, ok, s2)
	}
	a2.Ack(seq)
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{s1, s2}) {
		t.Fatalf("reclaimed = %v", got)
	}
}

// A second subscriber joining later only owes acks for messages committed
// after its join; the first subscriber alone cannot reclaim the old message
// while the ledger still lists it... and vice versa for the new one.
func TestJoinDoesNotExtendOldReceiveSet(t *testing.T) {
	q := New(4)
	a := q.Subscribe()
	s1 := mustPublish(t, q, "old")

	b := q.Subscribe() // joins after s1 committed
	if _, _, ok := b.Recv(); ok {
		t.Fatal("late joiner received old message")
	}
	if ids, _ := q.Pending(s1); !reflect.DeepEqual(ids, []uint64{a.id}) {
		t.Fatalf("pending(s1) = %v, want only first subscriber", ids)
	}

	s2 := mustPublish(t, q, "new")
	if ids, _ := q.Pending(s2); !reflect.DeepEqual(ids, []uint64{a.id, b.id}) {
		t.Fatalf("pending(s2) = %v", ids)
	}

	// a acks both; s1 reclaimed, s2 still owed by b.
	a.Ack(s1)
	a.Ack(s2)
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{s1}) {
		t.Fatalf("reclaimed = %v", got)
	}
	seq, v, ok := b.Recv()
	if !ok || seq != s2 || v != "new" {
		t.Fatalf("b Recv = %d,%v,%v", seq, v, ok)
	}
}

// Cancel after Commit is a no-op that still returns the committed sequence
// number; the message remains visible to its receive set.
func TestCancelAfterCommitReturnsSeq(t *testing.T) {
	q := New(2)
	s := q.Subscribe()

	p, err := q.Begin("committed")
	if err != nil {
		t.Fatal(err)
	}
	seq := p.Commit()
	if got, ok := p.Cancel(); !ok || got != seq {
		t.Fatalf("Cancel after Commit = %d,%v; want %d,true", got, ok, seq)
	}
	if gotSeq, v, ok := s.Recv(); !ok || gotSeq != seq || v != "committed" {
		t.Fatalf("Recv = %d,%v,%v", gotSeq, v, ok)
	}
	if q.Used() != 1 {
		t.Fatalf("used = %d, want 1 (cancel after commit must not free)", q.Used())
	}
}

// Cancel before Commit discards the reservation: nothing becomes visible and
// the capacity slot is released.
func TestCancelBeforeCommitFreesSlot(t *testing.T) {
	q := New(1)
	s := q.Subscribe()

	p, err := q.Begin("dropped")
	if err != nil {
		t.Fatal(err)
	}
	if seq, ok := p.Cancel(); ok || seq != 0 {
		t.Fatalf("Cancel before Commit = %d,%v; want 0,false", seq, ok)
	}
	if q.Used() != 0 {
		t.Fatalf("used = %d, want 0", q.Used())
	}
	if _, _, ok := s.Recv(); ok {
		t.Fatal("canceled publication became visible")
	}
	// Slot was released: a new publish succeeds.
	mustPublish(t, q, "replacement")
}

// Interleaving: capacity is held by a slow consumer across several messages;
// a mix of acks and one unsubscribe releases exactly the drained slots.
func TestCapacityReleaseInterleaving(t *testing.T) {
	q := New(3)
	a := q.Subscribe()
	b := q.Subscribe()

	s1 := mustPublish(t, q, "m1")
	s2 := mustPublish(t, q, "m2")
	s3 := mustPublish(t, q, "m3")
	if _, err := q.Publish("m4"); err != ErrFull {
		t.Fatalf("err = %v, want ErrFull", err)
	}

	// a acks m1 only; b is slow everywhere. m1 still owed by b.
	a.Ack(s1)
	if q.Used() != 3 {
		t.Fatalf("used = %d, want 3", q.Used())
	}

	// b leaves: unread m1,m2,m3 count as acked. m1 fully drains; m2,m3
	// remain owed by a.
	b.Unsubscribe()
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{s1}) {
		t.Fatalf("reclaimed = %v", got)
	}
	if q.Used() != 2 {
		t.Fatalf("used = %d, want 2", q.Used())
	}

	s4 := mustPublish(t, q, "m4") // fits now; receive set is {a} only
	if ids, _ := q.Pending(s4); !reflect.DeepEqual(ids, []uint64{a.id}) {
		t.Fatalf("pending(s4) = %v", ids)
	}

	a.Ack(s2)
	a.Ack(s3)
	a.Ack(s4)
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{s1, s2, s3, s4}) {
		t.Fatalf("reclaimed = %v", got)
	}
	if q.Used() != 0 {
		t.Fatalf("used = %d, want 0", q.Used())
	}
}

// A message committed with no subscribers has an empty receive set and is
// reclaimed immediately, consuming a sequence number but no capacity.
func TestCommitWithNoSubscribersReclaimsImmediately(t *testing.T) {
	q := New(1)
	seq := mustPublish(t, q, "nobody")
	if q.Used() != 0 {
		t.Fatalf("used = %d, want 0", q.Used())
	}
	if got := q.Reclaimed(); !reflect.DeepEqual(got, []uint64{seq}) {
		t.Fatalf("reclaimed = %v", got)
	}
	s := q.Subscribe()
	if _, _, ok := s.Recv(); ok {
		t.Fatal("subscriber saw message committed before its join")
	}
}
