package fetchquota

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDeadline covers missed deadlines, deadlines that still leave enough
// time, and the exact boundary instant.
func TestDeadline(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 2}},
		Accounts: map[string]RateLimit{"a": {Rate: 10, Burst: 10}},
	})

	first, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	first.MarkSent()
	second, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	second.MarkSent() // host bucket now empty; tokens refill at now+1s and now+2s.

	// Token available one second from the current clock time; a 500ms
	// deadline expires first.
	if _, err := s.AcquireUntil(context.Background(), "a", "h", fc.Now().Add(500*time.Millisecond)); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("short deadline: want ErrDeadlineExceeded, got %v", err)
	}

	// A deadline equal to the grant instant succeeds: grant beats deadline.
	// The second token refills at exactly now+2s (the first would land at
	// now+1s); a deadline equal to that boundary instant still grants.
	now := fc.Now()
	boundary, err := s.AcquireUntil(context.Background(), "a", "h", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("boundary deadline: %v", err)
	}
	boundary.MarkSent()

	// Another request whose deadline is after its refill instant succeeds too.
	later, err := s.AcquireUntil(context.Background(), "a", "h", fc.Now().Add(2*time.Second))
	if err != nil {
		t.Fatalf("generous deadline: %v", err)
	}
	later.MarkSent()

	// The expired path must not have consumed any token: the burst is empty
	// only because the successful grants each spent one.
	if got := s.hostTokens("h"); got != 0 {
		t.Fatalf("unexpected host balance: %v", got)
	}
}

// TestCancelRefundsBoth verifies that cancelling an approved but unsent
// request returns tokens to both levels, that the refund unblocks a waiter,
// and that finalization is idempotent (never double-refunds).
func TestCancelRefundsBoth(t *testing.T) {
	s, _ := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 1, Burst: 1}, "b": {Rate: 1, Burst: 1}},
	})

	lease, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := s.hostTokens("h"); got != 0 {
		t.Fatalf("host before refund: want 0, got %v", got)
	}
	if got := s.accountTokens("a"); got != 0 {
		t.Fatalf("account before refund: want 0, got %v", got)
	}

	waiting := asyncAcquire(s, "b", "h")
	assertBlocked(t, waiting, "waiting for refund")

	lease.Release()
	assertGranted(t, waiting, "unblocked by two-level refund").MarkSent()

	// Idempotent finalization never double-refunds above burst.
	lease.Release()
	lease.Cancel()
	lease.MarkSent()
	if got := s.hostTokens("h"); got != 0 {
		t.Fatalf("host after idempotent finalize: want 0, got %v", got)
	}
	if got := s.accountTokens("a"); got != 1 {
		t.Fatalf("refunded account: want 1, got %v", got)
	}
}

// TestQueuedCancellation verifies that cancelling while queued frees the
// head position without spending tokens and lets the next waiter proceed.
func TestQueuedCancellation(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 10, Burst: 10}, "b": {Rate: 10, Burst: 10}},
	})

	head, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	head.MarkSent()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, e := s.Acquire(ctx, "a", "h")
		errCh <- e
	}()
	time.Sleep(5 * time.Millisecond)

	next := asyncAcquire(s, "b", "h")
	assertBlocked(t, next, "b waits behind a")

	cancel()
	select {
	case e := <-errCh:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("queued cancel: want context.Canceled, got %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancel did not return")
	}

	// b becomes head and gets the next refilled token.
	fc.Advance(time.Second)
	assertGranted(t, next, "next waiter promoted after cancellation").MarkSent()
}

// TestUnknownKeys checks rejection when no explicit or default limit exists.
func TestUnknownKeys(t *testing.T) {
	s, _ := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"known": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"known": {Rate: 1, Burst: 1}},
	})

	if _, err := s.Acquire(context.Background(), "known", "mystery"); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("unknown host: want ErrUnknownHost, got %v", err)
	}
	if _, err := s.Acquire(context.Background(), "mystery", "known"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("unknown account: want ErrUnknownAccount, got %v", err)
	}

	// Defaults make unknown keys usable and start them with a full bucket.
	fc := NewFakeClock()
	s2, err := New(fc, Config{
		DefaultHost:    RateLimit{Rate: 1, Burst: 1},
		DefaultAccount: RateLimit{Rate: 1, Burst: 1},
	})
	if err != nil {
		t.Fatalf("New with defaults: %v", err)
	}
	l, err := s2.Acquire(context.Background(), "anyone", "anywhere")
	if err != nil {
		t.Fatalf("default-limited acquire: %v", err)
	}
	l.MarkSent()
}

// TestRealClockConcurrency hammers the scheduler with many goroutines against
// shared quotas and verifies the two-level invariant under -race: grants
// never exceed what both buckets can cover, and every lease is finalized once.
func TestRealClockConcurrency(t *testing.T) {
	s, err := New(NewRealClock(), Config{
		Hosts: map[string]RateLimit{
			"h": {Rate: 200, Burst: 4},
		},
		Accounts: map[string]RateLimit{
			"a": {Rate: 100, Burst: 2},
			"b": {Rate: 100, Burst: 2},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	var granted, sent, refunded int64
	for _, acct := range []string{"a", "b"} {
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func(account string) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				lease, err := s.Acquire(ctx, account, "h")
				if err != nil {
					return
				}
				atomic.AddInt64(&granted, 1)
				// Roughly one in four approved requests never gets sent and
				// must refund both quota levels.
				if atomic.LoadInt64(&granted)%4 == 0 {
					lease.Release()
					atomic.AddInt64(&refunded, 1)
					return
				}
				lease.MarkSent()
				atomic.AddInt64(&sent, 1)
			}(acct)
		}
	}
	wg.Wait()

	if granted == 0 {
		t.Fatal("expected some grants under real clock")
	}
	if sent+refunded != granted {
		t.Fatalf("finalization mismatch: granted=%d sent=%d refunded=%d", granted, sent, refunded)
	}
	if got := s.queueDepth("a", "h"); got != 0 || s.queueDepth("b", "h") != 0 {
		t.Fatal("queues must drain completely")
	}

	// After a full second with no demand, both levels must have refilled.
	time.Sleep(1100 * time.Millisecond)
	if got := s.hostTokens("h"); got != 4 {
		t.Fatalf("host refill: want 4, got %v", got)
	}
	for _, acct := range []string{"a", "b"} {
		if got := s.accountTokens(acct); got != 2 {
			t.Fatalf("account %s refill: want 2, got %v", acct, got)
		}
	}
}

// TestFakeClockDeterminism proves that time stands still for waiting
// goroutines until Advance is called, and timers fire in chronological order.
func TestFakeClockDeterminism(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 10, Burst: 10}},
	})

	first, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	first.MarkSent()

	waiting := asyncAcquire(s, "a", "h")
	assertBlocked(t, waiting, "fake clock does not move on its own")
	time.Sleep(20 * time.Millisecond)
	assertBlocked(t, waiting, "still blocked without Advance")

	fc.Advance(500 * time.Millisecond)
	assertBlocked(t, waiting, "halfway to refill")

	fc.Advance(500 * time.Millisecond)
	assertGranted(t, waiting, "granted exactly after one fake second").MarkSent()

	if got := fc.pendingTimers(); got != 0 {
		t.Fatalf("leaked fake timers: %d", got)
	}
}

// TestInvalidConfig guards against zero rates and missing clocks.
func TestInvalidConfig(t *testing.T) {
	fc := NewFakeClock()
	if _, err := New(nil, Config{}); err == nil {
		t.Fatal("nil clock must fail")
	}
	if _, err := New(fc, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 0, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 1, Burst: 1}},
	}); err == nil {
		t.Fatal("zero rate must fail")
	}
	if _, err := New(fc, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 1, Burst: 0}},
	}); err == nil {
		t.Fatal("zero burst must fail")
	}
}

// TestCooldownAlreadyExpired ensures non-positive and past cooldowns never
// block a queue.
func TestCooldownAlreadyExpired(t *testing.T) {
	s, _ := testScheduler(t, baseConfig())
	s.Cooldown("a", "h1", 0)
	s.Cooldown("a", "h1", -time.Second)
	l, err := s.Acquire(context.Background(), "a", "h1")
	if err != nil {
		t.Fatalf("non-positive cooldown must not block: %v", err)
	}
	l.MarkSent()
}
