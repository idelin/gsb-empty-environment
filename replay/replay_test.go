package replay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) Rollback(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(-d)
}

func testKey(clock *fakeClock, seq uint32) Key {
	return Key{
		ClientPrincipal:  "alice@REALM",
		ServicePrincipal: "host/server@REALM",
		TicketDigest:     []byte{1, 2, 3, 4, 9, 8, 7, 6},
		ClientTime:       clock.Now(),
		Sequence:         seq,
	}
}

func countEntries(records []Record) (entries, watermarks int) {
	for _, rec := range records {
		switch rec.Kind {
		case KindEntry:
			entries++
		case KindWatermark:
			watermarks++
		}
	}
	return entries, watermarks
}

func newTestCache(t *testing.T, storage Storage, clock Clock) *Cache {
	t.Helper()
	cache, err := New(Config{
		Storage:         storage,
		Clock:           clock,
		PastWindow:      time.Minute,
		FutureTolerance: time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}

func TestAcceptOnceWithinWindow(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	storage := NewMemoryStorage()
	cache := newTestCache(t, storage, clock)

	key := testKey(clock, 1)
	if err := cache.Accept(ctx, key); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if err := cache.Accept(ctx, key); !errors.Is(err, ErrReplay) {
		t.Fatalf("second Accept = %v, want ErrReplay", err)
	}

	// A different microsecond sequence is a different ticket key.
	other := key
	other.Sequence = 2
	if err := cache.Accept(ctx, other); err != nil {
		t.Fatalf("distinct key Accept: %v", err)
	}

	records := storage.Snapshot()
	entries, watermarks := countEntries(records)
	if entries != 2 || watermarks != 2 {
		t.Fatalf("records = %d entries / %d watermarks, want 2/2", entries, watermarks)
	}
}

func TestFarFutureRejectedWithoutAdvancingBound(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	storage := NewMemoryStorage()
	cache := newTestCache(t, storage, clock)

	farFuture := testKey(clock, 1)
	farFuture.ClientTime = clock.Now().Add(time.Hour)
	if err := cache.Accept(ctx, farFuture); !errors.Is(err, ErrFuture) {
		t.Fatalf("future Accept = %v, want ErrFuture", err)
	}
	if records := storage.Snapshot(); len(records) != 0 {
		t.Fatalf("future rejection wrote %d records, want 0", len(records))
	}

	// A normal request is still accepted and starts the lower bound from the
	// current time, proving the future timestamp never touched the bound.
	normal := testKey(clock, 2)
	if err := cache.Accept(ctx, normal); err != nil {
		t.Fatalf("normal Accept after future rejection: %v", err)
	}
}

func TestStaleOutsidePastWindow(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	cache := newTestCache(t, NewMemoryStorage(), clock)

	old := testKey(clock, 1)
	old.ClientTime = clock.Now().Add(-2 * time.Minute)
	if err := cache.Accept(ctx, old); !errors.Is(err, ErrStale) {
		t.Fatalf("stale Accept = %v, want ErrStale", err)
	}
}

func TestPersistedLowerBoundSurvivesClockRollback(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	storage := NewMemoryStorage()
	cache, err := New(Config{
		Storage:         storage,
		Clock:           clock,
		PastWindow:      time.Minute,
		FutureTolerance: time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	first := testKey(clock, 1)
	if err := cache.Accept(ctx, first); err != nil {
		t.Fatalf("first Accept: %v", err)
	}

	// Host clock rolls back by ten minutes after a restart-like event.
	clock.Rollback(10 * time.Minute)

	// A fresh ticket stamped with the rolled-back host clock is still below the
	// persisted lower bound and must be rejected.
	rolledBack := testKey(clock, 2)
	if err := cache.Accept(ctx, rolledBack); !errors.Is(err, ErrStale) {
		t.Fatalf("rollback Accept = %v, want ErrStale", err)
	}

	// The original key remains a replay rather than becoming stale. The
	// tolerance is wide enough that its original (pre-rollback) client time is
	// still inside the future window while the duplicate key is detected.
	if err := cache.Accept(ctx, first); !errors.Is(err, ErrReplay) {
		t.Fatalf("rollback replay = %v, want ErrReplay", err)
	}

	// Time catches up beyond the persisted bound; new keys are accepted again.
	clock.Advance(11 * time.Minute)
	caughtUp := testKey(clock, 3)
	if err := cache.Accept(ctx, caughtUp); err != nil {
		t.Fatalf("caught-up Accept: %v", err)
	}

	_, watermarks := countEntries(storage.Snapshot())
	if watermarks != 2 {
		t.Fatalf("watermark records = %d, want 2", watermarks)
	}
}

func TestRestartRejectsReplayFromDurableLog(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	storage := NewMemoryStorage()

	cache := newTestCache(t, storage, clock)
	key := testKey(clock, 1)
	if err := cache.Accept(ctx, key); err != nil {
		t.Fatalf("first Accept: %v", err)
	}

	// A brand-new cache over the same durable store represents a restart.
	restarted, err := New(Config{
		Storage:         storage,
		Clock:           clock,
		PastWindow:      time.Minute,
		FutureTolerance: time.Minute,
	})
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	if err := restarted.Accept(ctx, key); !errors.Is(err, ErrReplay) {
		t.Fatalf("restart replay = %v, want ErrReplay", err)
	}
}

func TestKeyIDStableAndBoundariesUnambiguous(t *testing.T) {
	base := Key{
		ClientPrincipal:  "alice",
		ServicePrincipal: "server",
		TicketDigest:     []byte{0xAA},
		ClientTime:       time.Unix(1_700_000_000, 123),
		Sequence:         7,
	}
	id := base.ID()
	if id != base.ID() {
		t.Fatal("Key.ID is not deterministic")
	}

	// Moving a character across a length-prefixed field boundary must change
	// the identifier.
	moved := base
	moved.ClientPrincipal = "alices"
	moved.ServicePrincipal = "erve"
	if moved.ID() == id {
		t.Fatal("distinct field boundaries produced identical key IDs")
	}

	mutated := base
	mutated.TicketDigest = []byte{0xAB}
	if mutated.ID() == id {
		t.Fatal("distinct ticket digests produced identical key IDs")
	}
}

func TestAcceptValidatesKey(t *testing.T) {
	cache := newTestCache(t, NewMemoryStorage(), newFakeClock(time.Unix(1_700_000_000, 0)))
	if err := cache.Accept(context.Background(), Key{}); err == nil {
		t.Fatal("empty key accepted, want validation error")
	}
}

func TestNewRejectsConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New without storage succeeded")
	}
	if _, err := New(Config{Storage: NewMemoryStorage(), PastWindow: -1}); err == nil {
		t.Fatal("New with negative window succeeded")
	}
}

func TestConcurrentInProcessAcceptance(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	storage := NewMemoryStorage()
	cache := newTestCache(t, storage, clock)
	key := testKey(clock, 1)

	const goroutines = 32
	var wg sync.WaitGroup
	results := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.Accept(ctx, key)
		}()
	}
	wg.Wait()
	close(results)

	var success, replay int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrReplay):
			replay++
		default:
			t.Fatalf("unexpected Accept error: %v", err)
		}
	}
	if success != 1 || replay != goroutines-1 {
		t.Fatalf("success=%d replay=%d, want 1 and %d", success, replay, goroutines-1)
	}
}
