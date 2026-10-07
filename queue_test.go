package queue

import (
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newHarness(maxDeliveries int) (*ManualClock, *Queue) {
	clock := NewManualClock(epoch)
	return clock, New(clock, 30*time.Second, maxDeliveries)
}

// A lease that is still valid at restore time must survive the snapshot:
// the ack token issued before the "crash" still completes the delivery.
func TestSnapshotPreservesLiveLeaseAcrossAck(t *testing.T) {
	clock, q := newHarness(3)
	q.Enqueue("m1", []byte("hello"))

	d, err := q.Dequeue()
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	clock.Advance(10 * time.Second) // lease still valid (30s)

	snap := q.Snapshot()
	_, restored := newHarness(3)
	restored.clock = clock
	restored.Restore(snap)

	if got := restored.InFlightLen(); got != 1 {
		t.Fatalf("in-flight after restore = %d, want 1", got)
	}
	if err := restored.Ack(d.Token); err != nil {
		t.Fatalf("ack with pre-snapshot token: %v", err)
	}
	if got := restored.InFlightLen(); got != 0 {
		t.Fatalf("in-flight after ack = %d, want 0", got)
	}
	if got := restored.PendingLen(); got != 0 {
		t.Fatalf("pending after ack = %d, want 0", got)
	}
}

// An expired lease returns to pending on restore, its old token is
// rejected, and redelivery increments the real delivery count.
func TestRestoreExpiredLeaseRequeuesAndInvalidatesToken(t *testing.T) {
	clock, q := newHarness(3)
	q.Enqueue("m1", []byte("hello"))

	d, _ := q.Dequeue()
	if d.Message.Deliveries != 1 {
		t.Fatalf("deliveries = %d, want 1", d.Message.Deliveries)
	}
	clock.Advance(31 * time.Second) // lease expired

	snap := q.Snapshot()
	_, restored := newHarness(3)
	restored.clock = clock
	restored.Restore(snap)

	if got := restored.PendingLen(); got != 1 {
		t.Fatalf("pending after restore = %d, want 1", got)
	}
	if err := restored.Ack(d.Token); err != ErrInvalidToken {
		t.Fatalf("ack with stale token = %v, want ErrInvalidToken", err)
	}

	d2, err := restored.Dequeue()
	if err != nil {
		t.Fatalf("redequeue: %v", err)
	}
	if d2.Token == d.Token {
		t.Fatal("redelivery reused the old token")
	}
	if d2.Message.Deliveries != 2 {
		t.Fatalf("deliveries after redelivery = %d, want 2", d2.Message.Deliveries)
	}
}

// Restoring repeatedly from the same snapshot, and snapshotting again in
// between, must not duplicate messages or change the outcome.
func TestRepeatedRecoveryIsIdempotent(t *testing.T) {
	clock, q := newHarness(3)
	q.Enqueue("m1", []byte("a"))
	q.Enqueue("m2", []byte("b"))

	d, _ := q.Dequeue() // m1 in-flight, m2 pending
	snap := q.Snapshot()

	restore := func() *Queue {
		_, r := newHarness(3)
		r.clock = clock
		r.Restore(snap)
		return r
	}
	r1 := restore()
	r2 := restore()
	// Restore again from a snapshot taken after a restore.
	_, r3 := newHarness(3)
	r3.clock = clock
	r3.Restore(r1.Snapshot())

	for i, r := range []*Queue{r1, r2, r3} {
		if got := r.InFlightLen(); got != 1 {
			t.Fatalf("restore %d: in-flight = %d, want 1", i, got)
		}
		if got := r.PendingLen(); got != 1 {
			t.Fatalf("restore %d: pending = %d, want 1", i, got)
		}
		if err := r.Ack(d.Token); err != nil {
			t.Fatalf("restore %d: ack live token: %v", i, err)
		}
		next, err := r.Dequeue()
		if err != nil || next.Message.ID != "m2" {
			t.Fatalf("restore %d: dequeue = %v, %v; want m2", i, next.Message.ID, err)
		}
		if got := r.PendingLen() + r.InFlightLen(); got != 1 {
			t.Fatalf("restore %d: total after drain = %d, want 1 (only m2 leased)", i, got)
		}
	}
}

// The dead-letter threshold counts real deliveries, and the message lands
// in the DLQ exactly once even across repeated restores.
func TestDeadLetterThresholdAndUniqueness(t *testing.T) {
	clock, q := newHarness(2)
	q.Enqueue("m1", []byte("poison"))

	d1, _ := q.Dequeue() // delivery 1
	clock.Advance(31 * time.Second)
	d2, _ := q.Dequeue() // delivery 2, threshold reached
	if d2.Message.Deliveries != 2 {
		t.Fatalf("deliveries = %d, want 2", d2.Message.Deliveries)
	}
	clock.Advance(31 * time.Second) // second lease expires

	snap := q.Snapshot()
	restore := func() *Queue {
		_, r := newHarness(2)
		r.clock = clock
		r.Restore(snap)
		return r
	}

	for i := 0; i < 3; i++ {
		r := restore()
		dlq := r.DLQ()
		if len(dlq) != 1 || dlq[0].ID != "m1" {
			t.Fatalf("restore %d: dlq = %+v, want exactly [m1]", i, dlq)
		}
		if dlq[0].Deliveries != 2 {
			t.Fatalf("restore %d: dlq deliveries = %d, want 2", i, dlq[0].Deliveries)
		}
		if err := r.Ack(d1.Token); err != ErrInvalidToken {
			t.Fatalf("restore %d: stale ack d1 = %v, want ErrInvalidToken", i, err)
		}
		if err := r.Ack(d2.Token); err != ErrInvalidToken {
			t.Fatalf("restore %d: stale ack d2 = %v, want ErrInvalidToken", i, err)
		}
		if _, err := r.Dequeue(); err != ErrEmpty {
			t.Fatalf("restore %d: dequeue = %v, want ErrEmpty", i, err)
		}
		snap = r.Snapshot() // re-snapshot must keep DLQ unique
	}
}

// Nack returns a message without consuming a delivery; only real
// handouts move a message toward the dead-letter threshold.
func TestNackDoesNotCountTowardDeadLetter(t *testing.T) {
	clock, q := newHarness(2)
	q.Enqueue("m1", nil)

	d, _ := q.Dequeue() // delivery 1
	if err := q.Nack(d.Token); err != nil {
		t.Fatalf("nack: %v", err)
	}
	d, _ = q.Dequeue() // delivery 2
	if err := q.Nack(d.Token); err != nil {
		t.Fatalf("nack: %v", err)
	}
	d, err := q.Dequeue() // delivery 3 > threshold 2 -> dead-lettered
	if err != ErrEmpty {
		t.Fatalf("dequeue past threshold = %v, want ErrEmpty", err)
	}
	dlq := q.DLQ()
	if len(dlq) != 1 || dlq[0].Deliveries != 2 {
		t.Fatalf("dlq = %+v, want one entry with 2 deliveries", dlq)
	}
	_ = clock
}
