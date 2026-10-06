package jobqueue

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newQueue(lease time.Duration, agingStep int) (*Queue, *FakeClock) {
	c := NewFakeClock(t0)
	return NewQueue(c, lease, agingStep), c
}

func ids(claimed []ClaimedJob) []string {
	out := make([]string, len(claimed))
	for i, c := range claimed {
		out[i] = c.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestClaimOrdersByPriorityThenSequence(t *testing.T) {
	q, _ := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "low-first", Priority: 1})
	q.Enqueue(Job{ID: "high", Priority: 9})
	q.Enqueue(Job{ID: "low-second", Priority: 1})

	got := ids(q.Claim(3))
	want := []string{"high", "low-first", "low-second"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAgingOnlyOnExplicitTick(t *testing.T) {
	q, clock := newQueue(time.Minute, 2)
	q.Enqueue(Job{ID: "old", Priority: 1})

	// Time passing alone must not age the waiting job.
	clock.Advance(10 * time.Minute)
	q.Enqueue(Job{ID: "new", Priority: 5})
	got := q.Claim(1)
	if !equal(ids(got), []string{"new"}) {
		t.Fatalf("without tick, got %v, want [new]", got)
	}
	if err := q.Complete("new", got[0].Generation); err != nil {
		t.Fatalf("complete new: %v", err)
	}

	// Three explicit ticks: old ages to 1 + 3*2 = 7, overtaking a
	// higher-priority newcomer.
	q.Tick()
	q.Tick()
	q.Tick()
	q.Enqueue(Job{ID: "fresh", Priority: 6})
	if got := ids(q.Claim(1)); !equal(got, []string{"old"}) {
		t.Fatalf("after aging ticks, got %v, want [old]", got)
	}
}

func TestBatchClaimIsAtomic(t *testing.T) {
	q, _ := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "a", Priority: 3})
	q.Enqueue(Job{ID: "b", Priority: 2})
	q.Enqueue(Job{ID: "c", Priority: 1})

	first := q.Claim(2)
	if !equal(ids(first), []string{"a", "b"}) {
		t.Fatalf("first batch = %v", ids(first))
	}
	// Everything claimed in the batch is occupied; only the remainder is claimable.
	second := q.Claim(2)
	if !equal(ids(second), []string{"c"}) {
		t.Fatalf("second batch = %v, want [c]", ids(second))
	}
	if got := q.Claim(1); len(got) != 0 {
		t.Fatalf("queue should be drained, got %v", ids(got))
	}
}

func TestLeaseExpiryRequeuesKeepingSequence(t *testing.T) {
	q, clock := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "x", Priority: 5})
	q.Enqueue(Job{ID: "y", Priority: 5})

	first := q.Claim(1) // x, generation 1
	if first[0].ID != "x" || first[0].Generation != 1 {
		t.Fatalf("first claim = %+v", first[0])
	}
	clock.Advance(2 * time.Minute) // lease on x expires

	second := q.Claim(2)
	// x keeps its original (earlier) sequence number, so it leads again,
	// now with generation 2.
	if !equal(ids(second), []string{"x", "y"}) {
		t.Fatalf("reclaim order = %v, want [x y]", ids(second))
	}
	if second[0].Generation != 2 {
		t.Fatalf("x generation = %d, want 2", second[0].Generation)
	}
}

func TestStaleReceiptRejected(t *testing.T) {
	q, clock := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "j", Priority: 1})

	first := q.Claim(1)
	clock.Advance(2 * time.Minute) // lease expires
	q.Claim(1)                     // reclaimed, generation now 2

	if err := q.Complete("j", first[0].Generation); err != ErrStaleReceipt {
		t.Fatalf("stale receipt err = %v, want ErrStaleReceipt", err)
	}
	if got := q.Pending(); got != 0 {
		t.Fatalf("job must stay leased to the new claim, pending = %d", got)
	}
}

func TestLateReceiptAfterExpiryRejected(t *testing.T) {
	q, clock := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "j", Priority: 1})
	c := q.Claim(1)

	clock.Advance(time.Minute) // exactly at the deadline: expired
	if err := q.Complete("j", c[0].Generation); err != ErrStaleReceipt {
		t.Fatalf("late receipt err = %v, want ErrStaleReceipt", err)
	}
	if got := q.Pending(); got != 1 {
		t.Fatalf("expired job should be requeued, pending = %d", got)
	}
}

func TestDuplicateCompleteRejected(t *testing.T) {
	q, _ := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "j", Priority: 1})
	c := q.Claim(1)

	if err := q.Complete("j", c[0].Generation); err != nil {
		t.Fatalf("first complete err = %v", err)
	}
	if err := q.Complete("j", c[0].Generation); err != ErrAlreadyCompleted {
		t.Fatalf("duplicate complete err = %v, want ErrAlreadyCompleted", err)
	}
	// A completed job never comes back.
	if got := q.Claim(1); len(got) != 0 {
		t.Fatalf("completed job reclaimed: %v", ids(got))
	}
}

func TestSimultaneousExpiry(t *testing.T) {
	q, clock := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "a", Priority: 1})
	q.Enqueue(Job{ID: "b", Priority: 1})
	q.Enqueue(Job{ID: "c", Priority: 1})

	batch := q.Claim(3) // all three leased with the same deadline
	if len(batch) != 3 {
		t.Fatalf("batch = %v", ids(batch))
	}
	clock.Advance(time.Minute) // all leases expire at the same instant

	if got := q.Pending(); got != 3 {
		t.Fatalf("all leases should expire together, pending = %d", got)
	}
	// Reclaimed in original sequence order, each with a bumped generation.
	again := q.Claim(3)
	if !equal(ids(again), []string{"a", "b", "c"}) {
		t.Fatalf("reclaim order = %v, want [a b c]", ids(again))
	}
	for _, cj := range again {
		if cj.Generation != 2 {
			t.Fatalf("%s generation = %d, want 2", cj.ID, cj.Generation)
		}
	}
}

func TestCompleteValidation(t *testing.T) {
	q, _ := newQueue(time.Minute, 0)
	q.Enqueue(Job{ID: "j", Priority: 1})

	if err := q.Complete("missing", 1); err != ErrUnknownJob {
		t.Fatalf("unknown job err = %v", err)
	}
	if err := q.Complete("j", 1); err != ErrNotLeased {
		t.Fatalf("unleased complete err = %v, want ErrNotLeased", err)
	}
	c := q.Claim(1)
	if err := q.Complete("j", c[0].Generation+99); err != ErrStaleReceipt {
		t.Fatalf("wrong generation err = %v, want ErrStaleReceipt", err)
	}
	if err := q.Complete("j", c[0].Generation); err != nil {
		t.Fatalf("valid complete err = %v", err)
	}
}
