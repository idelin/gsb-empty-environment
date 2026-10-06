package dedup

import (
	"sync"
	"testing"
	"time"
)

// testClock is a manually advanced clock for deterministic retention
// control.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1700000000, 0)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestStore(retention time.Duration) (*Store, *testClock) {
	clock := newTestClock()
	return New(retention, WithClock(clock.Now)), clock
}

func pos(offset uint64) Position {
	return Position{Source: "orders", Offset: offset}
}

func TestDoExecutesBusinessLogicOnlyOnce(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	calls := 0
	fn := func() (interface{}, error) {
		calls++
		return "result-1", nil
	}

	v, st, err := s.Do("k1", pos(1), fn)
	if err != nil || st != StatusExecuted || v != "result-1" {
		t.Fatalf("first Do = (%v, %v, %v), want (result-1, executed, nil)", v, st, err)
	}

	v, st, err = s.Do("k1", pos(1), fn)
	if err != nil || st != StatusReplayed || v != "result-1" {
		t.Fatalf("second Do = (%v, %v, %v), want (result-1, replayed, nil)", v, st, err)
	}
	if calls != 1 {
		t.Fatalf("business fn ran %d times, want 1", calls)
	}
}

func TestEvictionRequiresExpiredRetention(t *testing.T) {
	s, clock := newTestStore(time.Hour)
	if _, _, err := s.Do("k1", pos(1), func() (interface{}, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}

	clock.Advance(30 * time.Minute) // retention not over
	if evicted := s.Evict(); len(evicted) != 0 {
		t.Fatalf("evicted %v before retention expired", evicted)
	}
	if !s.HasResult("k1") {
		t.Fatal("result gone before retention expired")
	}

	clock.Advance(31 * time.Minute) // retention now over
	if evicted := s.Evict(); len(evicted) != 1 || evicted[0] != "k1" {
		t.Fatalf("evicted = %v, want [k1]", evicted)
	}
	if s.HasResult("k1") {
		t.Fatal("result still present after eviction")
	}
	if !s.HasTombstone("k1") {
		t.Fatal("eviction did not leave a tombstone")
	}
}

func TestEvictionRespectsActiveReferences(t *testing.T) {
	s, clock := newTestStore(time.Hour)
	if _, _, err := s.Do("k1", pos(1), func() (interface{}, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}

	ref, ok := s.Acquire("k1")
	if !ok {
		t.Fatal("Acquire failed for live result")
	}

	clock.Advance(2 * time.Hour) // retention long expired
	if evicted := s.Evict(); len(evicted) != 0 {
		t.Fatalf("evicted %v while a reference is active", evicted)
	}
	if !s.HasResult("k1") {
		t.Fatal("result evicted despite active reference")
	}
	if got := ref.Value(); got != 1 {
		t.Fatalf("ref.Value() = %v, want 1", got)
	}

	ref.Release()
	if evicted := s.Evict(); len(evicted) != 1 {
		t.Fatalf("evicted = %v after release, want [k1]", evicted)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify after release: %v", err)
	}
}

// TestCleanupCannotTriggerReexecution is the core guarantee: after the
// result entity is evicted, a duplicate delivery hits the tombstone and
// the business function is NOT re-executed.
func TestCleanupCannotTriggerReexecution(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	calls := 0
	fn := func() (interface{}, error) {
		calls++
		return "r", nil
	}

	if _, _, err := s.Do("k1", pos(7), fn); err != nil {
		t.Fatal(err)
	}

	clock.Advance(2 * time.Minute)
	if evicted := s.Evict(); len(evicted) != 1 {
		t.Fatalf("evicted = %v, want [k1]", evicted)
	}

	// Duplicate redelivery after cleanup: tombstone must suppress it.
	v, st, err := s.Do("k1", pos(7), fn)
	if st != StatusDuplicate || err != ErrDuplicate || v != nil {
		t.Fatalf("Do after eviction = (%v, %v, %v), want (nil, duplicate, ErrDuplicate)", v, st, err)
	}
	if calls != 1 {
		t.Fatalf("business fn ran %d times after cleanup, want 1", calls)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestTombstoneDroppedOnlyAfterWatermarkConfirmation(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	if _, _, err := s.Do("k1", pos(7), func() (interface{}, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	s.Evict()
	if !s.HasTombstone("k1") {
		t.Fatal("expected tombstone after eviction")
	}

	// Watermark below the recorded position: tombstone must survive.
	if dropped := s.ConfirmWatermark("orders", 6); dropped != 0 {
		t.Fatalf("dropped %d tombstones below watermark, want 0", dropped)
	}
	if !s.HasTombstone("k1") {
		t.Fatal("tombstone dropped before watermark covered its position")
	}

	// Watermark from a different source must not affect it.
	if dropped := s.ConfirmWatermark("payments", 100); dropped != 0 {
		t.Fatalf("dropped %d tombstones for wrong source, want 0", dropped)
	}
	if !s.HasTombstone("k1") {
		t.Fatal("tombstone dropped by unrelated source watermark")
	}

	// Watermark covering the position: tombstone is dropped.
	if dropped := s.ConfirmWatermark("orders", 7); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if s.HasTombstone("k1") {
		t.Fatal("tombstone survived watermark confirmation")
	}
}

func TestLedgerAccountingAndVerify(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	if _, _, err := s.Do("k1", pos(1), func() (interface{}, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}

	r1, ok1 := s.Acquire("k1")
	r2, ok2 := s.Acquire("k1")
	if !ok1 || !ok2 {
		t.Fatal("Acquire failed for live result")
	}
	if got := s.ledger.count("k1"); got != 2 {
		t.Fatalf("ledger count = %d, want 2", got)
	}

	r1.Release()
	r1.Release() // double release must be a no-op
	if got := s.ledger.count("k1"); got != 1 {
		t.Fatalf("ledger count after releases = %d, want 1", got)
	}

	r2.Release()
	if got := s.ledger.count("k1"); got != 0 {
		t.Fatalf("ledger count = %d, want 0", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestAcquireFailsForMissingOrEvictedResult(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	if _, ok := s.Acquire("missing"); ok {
		t.Fatal("Acquire succeeded for missing key")
	}
	if _, _, err := s.Do("k1", pos(1), func() (interface{}, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	s.Evict()
	if _, ok := s.Acquire("k1"); ok {
		t.Fatal("Acquire succeeded for evicted key")
	}
}

func TestConcurrentUseKeepsLedgerConsistent(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = s.Do("k1", pos(1), func() (interface{}, error) { return 1, nil })
			if ref, ok := s.Acquire("k1"); ok {
				_ = ref.Value()
				ref.Release()
			}
		}()
	}
	wg.Wait()
	if got := s.ledger.count("k1"); got != 0 {
		t.Fatalf("ledger count = %d after concurrent use, want 0", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
