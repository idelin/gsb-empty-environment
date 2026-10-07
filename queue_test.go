package msgqueue

import (
	"errors"
	"testing"
	"time"
)

const (
	testLease = 30 * time.Second
	testMax   = 3
)

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestQueue(clk *VirtualClock) *Queue {
	return New(clk, testLease, testMax)
}

func mustDeliver(t *testing.T, q *Queue) (*Message, string) {
	t.Helper()
	m, token, err := q.Deliver()
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	return m, token
}

func restore(t *testing.T, data []byte, clk *VirtualClock) *Queue {
	t.Helper()
	q, err := Restore(data, clk, testLease, testMax)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return q
}

// A lease still valid at restore time keeps its token: ack after snapshot
// and recovery must succeed exactly once.
func TestSnapshotAcrossAck(t *testing.T) {
	clk := NewVirtualClock(testStart)
	q := newTestQueue(clk)
	q.Enqueue(&Message{ID: "m1", Body: []byte("hello")})

	m, token, err := q.Deliver()
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if m.Deliveries != 1 {
		t.Fatalf("Deliveries = %d, want 1", m.Deliveries)
	}

	snap, err := q.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	clk.Advance(10 * time.Second) // still within the lease

	q2 := restore(t, snap, clk)
	if err := q2.Ack(token); err != nil {
		t.Fatalf("Ack after restore: %v", err)
	}
	if err := q2.Ack(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second Ack = %v, want ErrInvalidToken", err)
	}
	if p, i, d := q2.Stats(); p != 0 || i != 0 || d != 0 {
		t.Fatalf("Stats = (%d,%d,%d), want (0,0,0)", p, i, d)
	}
}

// A lease expired by restore time returns to pending and its old token is
// rejected; redelivery issues a fresh token and bumps the real count.
func TestExpiredLeaseAfterRestoreInvalidatesToken(t *testing.T) {
	clk := NewVirtualClock(testStart)
	q := newTestQueue(clk)
	q.Enqueue(&Message{ID: "m1"})

	_, oldToken, err := q.Deliver()
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	snap, err := q.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	clk.Advance(testLease + time.Second) // lease now expired
	q2 := restore(t, snap, clk)

	if err := q2.Ack(oldToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Ack(old token) = %v, want ErrInvalidToken", err)
	}
	if p, i, _ := q2.Stats(); p != 1 || i != 0 {
		t.Fatalf("Stats = (%d,%d,..), want pending=1 inflight=0", p, i)
	}

	m, newToken, err := q2.Deliver()
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if m.Deliveries != 2 {
		t.Fatalf("Deliveries = %d, want 2 (real deliveries carried over)", m.Deliveries)
	}
	if newToken == oldToken {
		t.Fatal("redelivery reused the old token")
	}
	if err := q2.Ack(newToken); err != nil {
		t.Fatalf("Ack(new token): %v", err)
	}
}

// Restoring twice from the same snapshot must be idempotent: no duplicated
// messages, no phantom deliveries, no duplicated dead letters.
func TestDuplicateRecoveryIsIdempotent(t *testing.T) {
	clk := NewVirtualClock(testStart)
	q := newTestQueue(clk)
	// Push "doomed" through maxDeliveries expiries into the DLQ first.
	q.Enqueue(&Message{ID: "doomed"})
	for i := 0; i < testMax; i++ {
		if _, _, err := q.Deliver(); err != nil {
			t.Fatalf("Deliver doomed round %d: %v", i, err)
		}
		clk.Advance(testLease + time.Second)
		q.ReapExpired()
	}
	// One live in-flight lease and one untouched pending message.
	q.Enqueue(&Message{ID: "flight-1"})
	if _, _, err := q.Deliver(); err != nil {
		t.Fatalf("Deliver flight-1: %v", err)
	}
	q.Enqueue(&Message{ID: "pending-1"})

	snap1, err := q.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// First recovery, then snapshot again and recover a second time.
	q1 := restore(t, snap1, clk)
	snap2, err := q1.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after first restore: %v", err)
	}
	q2 := restore(t, snap2, clk)

	p, i, d := q2.Stats()
	if p != 1 || i != 1 || d != 1 {
		t.Fatalf("Stats after double restore = (%d,%d,%d), want (1,1,1)", p, i, d)
	}
	dead := q2.Dead()
	if len(dead) != 1 || dead[0].ID != "doomed" {
		t.Fatalf("Dead = %v, want exactly [doomed]", dead)
	}
	if dead[0].Deliveries != testMax {
		t.Fatalf("dead Deliveries = %d, want %d", dead[0].Deliveries, testMax)
	}
	pend := q2.Pending()
	if len(pend) != 1 || pend[0].ID != "pending-1" || pend[0].Deliveries != 0 {
		t.Fatalf("Pending = %+v, want [pending-1] untouched", pend)
	}
}

// The dead-letter threshold counts real deliveries only; a message reaches
// the DLQ exactly once, even across repeated recoveries.
func TestDeadLetterThresholdAndUniqueness(t *testing.T) {
	clk := NewVirtualClock(testStart)
	q := newTestQueue(clk)
	q.Enqueue(&Message{ID: "m1"})

	var snap []byte
	qq := q
	for round := 1; round <= testMax; round++ {
		m, token, err := qq.Deliver()
		if err != nil {
			t.Fatalf("round %d Deliver: %v", round, err)
		}
		if m.Deliveries != round {
			t.Fatalf("round %d Deliveries = %d", round, m.Deliveries)
		}
		// Recover mid-flight each round: delivery count must survive.
		var err2 error
		snap, err2 = qq.Snapshot()
		if err2 != nil {
			t.Fatalf("round %d Snapshot: %v", round, err2)
		}
		clk.Advance(testLease + time.Second) // expire the lease
		qq = restore(t, snap, clk)
		if err := qq.Ack(token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("round %d stale Ack = %v, want ErrInvalidToken", round, err)
		}
	}

	dead := qq.Dead()
	if len(dead) != 1 || dead[0].ID != "m1" {
		t.Fatalf("Dead = %v, want exactly one m1", dead)
	}
	if dead[0].Deliveries != testMax {
		t.Fatalf("dead Deliveries = %d, want %d", dead[0].Deliveries, testMax)
	}

	// Further recoveries must not duplicate the dead letter.
	for i := 0; i < 3; i++ {
		s, err := qq.Snapshot()
		if err != nil {
			t.Fatalf("extra Snapshot: %v", err)
		}
		clk.Advance(time.Hour)
		qq = restore(t, s, clk)
	}
	if dead := qq.Dead(); len(dead) != 1 {
		t.Fatalf("Dead after repeated recovery = %d entries, want 1", len(dead))
	}
	if p, i, d := qq.Stats(); p != 0 || i != 0 || d != 1 {
		t.Fatalf("Stats = (%d,%d,%d), want (0,0,1)", p, i, d)
	}
}

// Nack before the threshold returns to pending; at the threshold it
// dead-letters, again by real delivery count.
func TestNackRouting(t *testing.T) {
	clk := NewVirtualClock(testStart)
	q := newTestQueue(clk)
	q.Enqueue(&Message{ID: "m1"})

	for round := 1; round <= testMax; round++ {
		_, token := mustDeliver(t, q)
		if err := q.Nack(token); err != nil {
			t.Fatalf("round %d Nack: %v", round, err)
		}
	}
	if dead := q.Dead(); len(dead) != 1 || dead[0].Deliveries != testMax {
		t.Fatalf("Dead = %+v, want one entry with %d deliveries", dead, testMax)
	}
	if _, _, err := q.Deliver(); !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("Deliver after dead-letter = %v, want ErrQueueEmpty", err)
	}
}
