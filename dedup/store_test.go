package dedup

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

var testEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// clock is a manually advanced time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: testEpoch} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestReferencedResultSurvivesRetention(t *testing.T) {
	clk := newClock()
	s := NewStore(time.Minute)
	s.SetClock(clk.Now)

	s.Record("order-1", "receipt-1", 100)

	h, err := s.Acquire("order-1")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Retention expires while the reference is held.
	clk.Advance(2 * time.Minute)
	evicted, _ := s.Sweep()
	if evicted != 0 {
		t.Fatalf("sweep evicted a referenced result")
	}
	if st, _ := s.Resolve("order-1"); st != StateLive {
		t.Fatalf("referenced result must stay live, got %v", st)
	}

	h.Release()
	clk.Advance(time.Minute)
	evicted, _ = s.Sweep()
	if evicted != 1 {
		t.Fatalf("expected result eviction after release+expiry, got %d", evicted)
	}

	// The result is gone but the tombstone still guards the key:
	// the business operation must NOT be re-executed.
	if st, _ := s.Resolve("order-1"); st != StateDuplicate {
		t.Fatalf("expected duplicate guard after eviction, got %v", st)
	}
	if err := s.Ledger().Verify(); err != nil {
		t.Fatalf("ledger: %v", err)
	}
}

func TestTombstoneLifecycleFollowsWatermark(t *testing.T) {
	clk := newClock()
	s := NewStore(time.Minute)
	s.SetClock(clk.Now)

	s.Record("order-2", "receipt-2", 42)
	clk.Advance(2 * time.Minute)
	s.Sweep() // evicts result, tombstone remains

	// Watermark below the record's source position: tombstone stays.
	s.AdvanceWatermark(41)
	if _, tomb := s.Sweep(); tomb != 0 {
		t.Fatalf("tombstone removed before watermark confirmation")
	}
	if st, _ := s.Resolve("order-2"); st != StateDuplicate {
		t.Fatalf("tombstone must survive until watermark, got %v", st)
	}

	// Watermark confirmed past the source position: tombstone collected.
	s.AdvanceWatermark(42)
	if _, tomb := s.Sweep(); tomb != 1 {
		t.Fatalf("expected tombstone collection after watermark, got %d", tomb)
	}
	if st, _ := s.Resolve("order-2"); st != StateUnknown {
		t.Fatalf("key should be unknown only after watermark confirmation, got %v", st)
	}
	if err := s.Ledger().Verify(); err != nil {
		t.Fatalf("ledger: %v", err)
	}
}

func TestAcquireUnknownKey(t *testing.T) {
	s := NewStore(time.Minute)
	if _, err := s.Acquire("nope"); err != ErrKeyUnknown {
		t.Fatalf("expected ErrKeyUnknown, got %v", err)
	}
}

func TestRecordRefreshesRetention(t *testing.T) {
	clk := newClock()
	s := NewStore(time.Minute)
	s.SetClock(clk.Now)

	s.Record("k", "v1", 1)
	clk.Advance(50 * time.Second)
	s.Record("k", "v2", 2)
	clk.Advance(50 * time.Second) // past original expiry, within refreshed

	if evicted, _ := s.Sweep(); evicted != 0 {
		t.Fatalf("record refresh must extend retention")
	}
	if st, v := s.Resolve("k"); st != StateLive || v != "v2" {
		t.Fatalf("expected refreshed live result, got %v %v", st, v)
	}
}

// TestCleanupNeverTriggersReexecution hammers the store with concurrent
// acquirers and sweepers, then audits the ledger: at no point may a key
// that was ever known become unknown while references are outstanding.
func TestCleanupNeverTriggersReexecution(t *testing.T) {
	clk := newClock()
	s := NewStore(5 * time.Millisecond)
	s.SetClock(clk.Now)

	const keys = 16
	const workers = 8
	const rounds = 200

	for i := 0; i < keys; i++ {
		s.Record(fmt.Sprintf("key-%d", i), i, uint64(i+1))
	}

	stop := make(chan struct{})
	var sweeper sync.WaitGroup
	sweeper.Add(1)
	go func() {
		defer sweeper.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clk.Advance(10 * time.Millisecond)
				s.Sweep()
			}
		}
	}()

	// Workers: acquire, use, release; re-record to simulate reprocessing
	// only when the key is genuinely unknown.
	var workersWg sync.WaitGroup
	for w := 0; w < workers; w++ {
		workersWg.Add(1)
		go func(id int) {
			defer workersWg.Done()
			for r := 0; r < rounds; r++ {
				key := fmt.Sprintf("key-%d", (id+r)%keys)
				h, err := s.Acquire(key)
				if err == ErrKeyUnknown {
					// Only legal after watermark confirmation; re-record.
					s.Record(key, id, uint64(1000+r))
					continue
				}
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				_ = h.Value()
				h.Release()
			}
		}(w)
	}

	workersWg.Wait()
	close(stop)
	sweeper.Wait()

	if err := s.Ledger().Verify(); err != nil {
		t.Fatalf("ledger verification failed: %v", err)
	}
}

func TestLedgerDetectsUnsafeEviction(t *testing.T) {
	// Forge an unsafe history directly against the ledger to prove the
	// verifier catches it: a result evicted while a reference is live.
	l := NewLedger()
	l.acquire("k")
	l.evictResult("k", &Result{Key: "k", refs: 1})
	if err := l.Verify(); err == nil {
		t.Fatalf("verifier must reject eviction under live reference")
	}

	l2 := NewLedger()
	l2.acquire("k")
	l2.evictTombstone("k", &Tombstone{Key: "k"})
	if err := l2.Verify(); err == nil {
		t.Fatalf("verifier must reject tombstone eviction under live reference")
	}
}
