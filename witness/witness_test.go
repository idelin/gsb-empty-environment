package witness

import (
	"testing"
	"time"
)

const ttl = 100 * time.Millisecond

func TestSingleActiveLease(t *testing.T) {
	w := New()

	l1, ok := w.Acquire("dc1", ttl)
	if !ok || l1.Epoch != 1 || l1.Holder != "dc1" {
		t.Fatalf("dc1 should get epoch 1, got %+v ok=%v", l1, ok)
	}

	// While dc1's lease is alive, dc2 must be refused: no dual-primary.
	l2, ok := w.Acquire("dc2", ttl)
	if ok {
		t.Fatalf("dc2 must not acquire a live lease, got %+v", l2)
	}
	if l2.Holder != "dc1" || l2.Epoch != 1 {
		t.Fatalf("refusal should report current lease, got %+v", l2)
	}
}

func TestRenewOnlyByHolderWithMatchingEpoch(t *testing.T) {
	w := New()
	w.Acquire("dc1", ttl)

	if _, ok := w.Renew("dc2", 1, ttl); ok {
		t.Fatal("renew by non-holder must be rejected")
	}
	if _, ok := w.Renew("dc1", 99, ttl); ok {
		t.Fatal("renew with wrong epoch must be rejected")
	}
	l, ok := w.Renew("dc1", 1, ttl)
	if !ok || l.Epoch != 1 {
		t.Fatalf("valid renew should succeed, got %+v ok=%v", l, ok)
	}
}

func TestTakeoverAfterExpiryBumpsEpoch(t *testing.T) {
	w := New()
	w.Acquire("dc1", ttl)

	// DC1 dies (power loss); its lease expires at the Witness.
	time.Sleep(2 * ttl)

	l2, ok := w.Acquire("dc2", ttl)
	if !ok || l2.Holder != "dc2" || l2.Epoch != 2 {
		t.Fatalf("dc2 takeover should yield epoch 2, got %+v ok=%v", l2, ok)
	}

	// DC1 comes back and presents its old credential: must be rejected.
	if _, ok := w.Renew("dc1", 1, ttl); ok {
		t.Fatal("stale renew from recovered dc1 must be rejected")
	}
	if _, ok := w.Acquire("dc1", ttl); ok {
		t.Fatal("dc1 must not re-acquire while dc2 holds a live lease")
	}
}

func TestHandoverIsAtomicUnderConcurrency(t *testing.T) {
	w := New()
	done := make(chan struct{})
	grants := make(chan uint64, 256)
	for _, id := range []string{"dc1", "dc2"} {
		go func(id string) {
			for {
				select {
				case <-done:
					return
				default:
				}
				if l, ok := w.Acquire(id, 30*time.Millisecond); ok {
					grants <- l.Epoch
					time.Sleep(35 * time.Millisecond) // hold past expiry, never renew
				}
			}
		}(id)
	}
	time.Sleep(300 * time.Millisecond)
	close(done)

	prev := uint64(0)
	for len(grants) > 0 {
		e := <-grants
		if e <= prev {
			t.Fatalf("epochs must strictly increase across handovers: %d after %d", e, prev)
		}
		prev = e
	}
}
