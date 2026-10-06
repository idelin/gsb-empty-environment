package jobq

import (
	"sync"
	"testing"
	"time"
)

func newTestQueue(lease time.Duration) (*Queue, *FakeClock) {
	fc := NewFakeClock(time.Unix(1000, 0))
	return New(WithClock(fc), WithLease(lease)), fc
}

func payloads(leases []Lease) []interface{} {
	out := make([]interface{}, len(leases))
	for i, l := range leases {
		out[i] = l.Payload
	}
	return out
}

func TestClaimOrdersByPriorityThenSeq(t *testing.T) {
	q, _ := newTestQueue(time.Minute)
	q.Enqueue(1, "low")
	q.Enqueue(3, "high-first")
	q.Enqueue(3, "high-second")
	q.Enqueue(2, "mid")

	got := payloads(q.Claim(4))
	want := []interface{}{"high-first", "high-second", "mid", "low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim order = %v, want %v", got, want)
		}
	}
}

func TestAgingOnlyAppliesOnExplicitTick(t *testing.T) {
	q, fc := newTestQueue(time.Minute)
	q.Enqueue(0, "old")

	// Wall-clock time passing must not age the waiting job.
	fc.Advance(time.Hour)
	q.Enqueue(5, "new")
	if got := q.Claim(1)[0].Payload; got != "new" {
		t.Fatalf("without Tick, got %v, want new", got)
	}

	// Six explicit ticks lift the old job above a fresh priority-5 job.
	for i := 0; i < 6; i++ {
		q.Tick()
	}
	q.Enqueue(5, "newer")
	if got := q.Claim(1)[0].Payload; got != "old" {
		t.Fatalf("after ticks, got %v, want old", got)
	}
}

func TestBatchClaimIsAtomic(t *testing.T) {
	q, _ := newTestQueue(time.Minute)
	const total = 20
	for i := 0; i < total; i++ {
		q.Enqueue(1, i)
	}

	// Concurrent batch claims must not hand out the same job twice.
	var mu sync.Mutex
	seen := map[ID]int{}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, l := range q.Claim(5) {
				mu.Lock()
				seen[l.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != total {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %d claimed %d times", id, n)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("ready len = %d, want 0", q.Len())
	}
}

func TestLeaseExpiryRequeueKeepsOriginalSeq(t *testing.T) {
	q, fc := newTestQueue(10 * time.Second)
	q.Enqueue(1, "a")
	q.Enqueue(1, "b")
	first := q.Claim(2)
	if len(first) != 2 {
		t.Fatalf("claimed %d, want 2", len(first))
	}

	// Both leases expire; requeued jobs keep their original sequence
	// numbers and therefore stay ahead of anything enqueued later.
	fc.Advance(11 * time.Second)
	q.Enqueue(1, "c")

	got := payloads(q.Claim(3))
	want := []interface{}{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requeue order = %v, want %v", got, want)
		}
	}
}

func TestStaleReceiptRejected(t *testing.T) {
	q, fc := newTestQueue(10 * time.Second)
	q.Enqueue(1, "job")

	old := q.Claim(1)[0]
	fc.Advance(11 * time.Second) // lease expires
	fresh := q.Claim(1)[0]

	if fresh.Receipt.Generation == old.Receipt.Generation {
		t.Fatal("re-claim did not bump generation")
	}
	if err := q.Complete(old.Receipt); err != ErrStaleReceipt {
		t.Fatalf("old receipt: got %v, want ErrStaleReceipt", err)
	}
	if err := q.Complete(fresh.Receipt); err != nil {
		t.Fatalf("fresh receipt: got %v, want nil", err)
	}
}

func TestDuplicateCompleteRejected(t *testing.T) {
	q, _ := newTestQueue(time.Minute)
	q.Enqueue(1, "job")
	lease := q.Claim(1)[0]

	if err := q.Complete(lease.Receipt); err != nil {
		t.Fatalf("first complete: got %v, want nil", err)
	}
	if err := q.Complete(lease.Receipt); err != ErrUnknownJob {
		t.Fatalf("duplicate complete: got %v, want ErrUnknownJob", err)
	}
}

func TestSimultaneousExpiry(t *testing.T) {
	q, fc := newTestQueue(10 * time.Second)
	q.Enqueue(1, "a")
	q.Enqueue(1, "b")
	q.Enqueue(1, "c")
	leases := q.Claim(3) // all share the same ExpiresAt

	// Advance to exactly the expiry instant: all three lapse together.
	fc.Advance(10 * time.Second)

	for _, l := range leases {
		if err := q.Complete(l.Receipt); err != ErrStaleReceipt {
			t.Fatalf("expired receipt for %v: got %v, want ErrStaleReceipt",
				l.Payload, err)
		}
	}
	if n := q.Len(); n != 3 {
		t.Fatalf("ready after expiry = %d, want 3", n)
	}

	// Reclaimed in original sequence order with bumped generations.
	reclaimed := q.Claim(3)
	for i, want := range []interface{}{"a", "b", "c"} {
		if reclaimed[i].Payload != want {
			t.Fatalf("reclaim order[%d] = %v, want %v", i, reclaimed[i].Payload, want)
		}
		if reclaimed[i].Receipt.Generation != leases[i].Receipt.Generation+1 {
			t.Fatalf("generation for %v = %d, want %d",
				want, reclaimed[i].Receipt.Generation, leases[i].Receipt.Generation+1)
		}
	}
}

func TestCompleteUnknownJob(t *testing.T) {
	q, _ := newTestQueue(time.Minute)
	if err := q.Complete(Receipt{ID: 999, Generation: 1}); err != ErrUnknownJob {
		t.Fatalf("got %v, want ErrUnknownJob", err)
	}
}
