package fetchquota

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testScheduler(t *testing.T, cfg Config) (*Scheduler, *FakeClock) {
	t.Helper()
	fc := NewFakeClock()
	s, err := New(fc, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, fc
}

func baseConfig() Config {
	return Config{
		Hosts: map[string]RateLimit{
			"h1": {Rate: 1, Burst: 2},
			"h2": {Rate: 1, Burst: 2},
		},
		Accounts: map[string]RateLimit{
			"a": {Rate: 1, Burst: 2},
			"b": {Rate: 1, Burst: 2},
			"c": {Rate: 1, Burst: 2},
		},
	}
}

type acquireResult struct {
	lease *Lease
	err   error
}

// asyncAcquire runs Acquire in a goroutine. The small delay gives fake-clock
// tests a deterministic guarantee that the caller has queued itself.
func asyncAcquire(s *Scheduler, account, host string) <-chan acquireResult {
	out := make(chan acquireResult, 1)
	go func() {
		l, err := s.Acquire(context.Background(), account, host)
		out <- acquireResult{lease: l, err: err}
	}()
	time.Sleep(5 * time.Millisecond)
	return out
}

func assertBlocked(t *testing.T, ch <-chan acquireResult, name string) {
	t.Helper()
	select {
	case res := <-ch:
		t.Fatalf("%s: expected to block, got %+v", name, res)
	case <-time.After(10 * time.Millisecond):
	}
}

func assertGranted(t *testing.T, ch <-chan acquireResult, name string) *Lease {
	t.Helper()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("%s: unexpected error %v", name, res.err)
		}
		return res.lease
	case <-time.After(time.Second):
		t.Fatalf("%s: expected grant, blocked", name)
		return nil
	}
}

func assertError(t *testing.T, ch <-chan acquireResult, want error, name string) {
	t.Helper()
	select {
	case res := <-ch:
		if !errors.Is(res.err, want) {
			t.Fatalf("%s: want error %v, got %v", name, want, res.err)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s: expected error %v, blocked", name, want)
	}
}

// TestTwoLevelQuota verifies a request needs both host and account tokens,
// and that whichever bucket exhausts first becomes the limiting one.
func TestTwoLevelQuota(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 2}},
		Accounts: map[string]RateLimit{"a": {Rate: 1, Burst: 3}, "b": {Rate: 1, Burst: 3}},
	})

	l1, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	l2, err := s.Acquire(context.Background(), "b", "h")
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	if got := s.hostTokens("h"); got != 0 {
		t.Fatalf("host tokens after 2 grants: want 0, got %v", got)
	}

	blocked := asyncAcquire(s, "a", "h")
	assertBlocked(t, blocked, "host exhausted")

	// The account bucket refills first (burst 3, it spent one), but the empty
	// host remains the limiting level.
	fc.Advance(500 * time.Millisecond)
	assertBlocked(t, blocked, "host still empty")

	fc.Advance(500 * time.Millisecond) // host's second token completes at t=1s.
	lease := assertGranted(t, blocked, "after host refill")
	lease.MarkSent()

	l1.MarkSent()
	l2.MarkSent()
}

// TestCooldownIsolation uses competing accounts and hosts to prove that a
// Retry-After cools only the single (account, host) queue that received it.
func TestCooldownIsolation(t *testing.T) {
	s, fc := testScheduler(t, baseConfig())

	lease, err := s.Acquire(context.Background(), "a", "h1")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	lease.MarkSent()

	// Retry-After for account a on h1.
	s.Cooldown("a", "h1", 5*time.Second)

	cooled := asyncAcquire(s, "a", "h1")
	assertBlocked(t, cooled, "cooled queue a/h1")

	// Another account on the same host is unaffected.
	otherAccount, err := s.Acquire(context.Background(), "b", "h1")
	if err != nil {
		t.Fatalf("other account on cooled host: %v", err)
	}
	otherAccount.MarkSent()

	// The same account against another host is unaffected.
	otherHost, err := s.Acquire(context.Background(), "a", "h2")
	if err != nil {
		t.Fatalf("cooled account on other host: %v", err)
	}
	otherHost.MarkSent()

	// An unrelated queue keeps running while the cooled one stays blocked.
	unrelated := asyncAcquire(s, "c", "h2")
	assertGranted(t, unrelated, "unrelated queue c/h2").MarkSent()
	assertBlocked(t, cooled, "cooled queue still blocked")

	fc.Advance(4 * time.Second)
	assertBlocked(t, cooled, "cooled queue before expiry")

	fc.Advance(time.Second)
	assertGranted(t, cooled, "cooled queue at expiry").MarkSent()
}

// TestCooldownBoundaryNoDoubleGrant stages two competing requests that both
// become eligible at the exact cooldown end instant while only one host token
// exists. Exactly one may be granted at that boundary; the other must wait.
func TestCooldownBoundaryNoDoubleGrant(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 1, Burst: 1}, "b": {Rate: 1, Burst: 1}},
	})

	first, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	first.MarkSent() // host empty; refill and cooldown both land at t=1s.

	s.Cooldown("a", "h", time.Second)

	wa := asyncAcquire(s, "a", "h")
	wb := asyncAcquire(s, "b", "h")
	assertBlocked(t, wa, "a before boundary")
	assertBlocked(t, wb, "b before boundary")

	// Advance exactly to the boundary: cooldown ends and one host token
	// refills at the same instant.
	fc.Advance(time.Second)

	var stillBlocked <-chan acquireResult
	select {
	case res := <-wa:
		if res.err != nil {
			t.Fatalf("a grant error: %v", res.err)
		}
		res.lease.MarkSent()
		stillBlocked = wb
	case res := <-wb:
		if res.err != nil {
			t.Fatalf("b grant error: %v", res.err)
		}
		res.lease.MarkSent()
		stillBlocked = wa
	case <-time.After(time.Second):
		t.Fatal("no request granted exactly at cooldown boundary")
	}

	assertBlocked(t, stillBlocked, "only one grant at boundary")
	if got := s.hostTokens("h"); got != 0 {
		t.Fatalf("host must be empty after single boundary grant, got %v", got)
	}

	fc.Advance(time.Second)
	assertGranted(t, stillBlocked, "second request after another refill").MarkSent()
	if got := s.hostTokens("h"); got != 0 {
		t.Fatalf("host must be empty again, got %v", got)
	}
}

// TestRefillBoundaryNoDoubleGrant checks the same boundary rule for pure
// token refills without any cooldown.
func TestRefillBoundaryNoDoubleGrant(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 10, Burst: 10}},
	})

	first, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	first.MarkSent()

	w1 := asyncAcquire(s, "a", "h")
	w2 := asyncAcquire(s, "a", "h")
	assertBlocked(t, w1, "w1 waiting for refill")
	assertBlocked(t, w2, "w2 waiting behind w1")

	fc.Advance(time.Second) // exactly one token materializes.
	assertGranted(t, w1, "head of queue at refill boundary").MarkSent()
	assertBlocked(t, w2, "second request cannot share the boundary token")

	fc.Advance(time.Second)
	assertGranted(t, w2, "second request after next refill").MarkSent()
}

// TestFIFO proves strict first-come-first-served within one queue even when a
// later request belongs to an account with spare tokens.
func TestFIFO(t *testing.T) {
	s, fc := testScheduler(t, Config{
		Hosts:    map[string]RateLimit{"h": {Rate: 1, Burst: 1}},
		Accounts: map[string]RateLimit{"a": {Rate: 10, Burst: 10}},
	})

	first, err := s.Acquire(context.Background(), "a", "h")
	if err != nil {
		t.Fatalf("initial acquire: %v", err)
	}
	first.MarkSent()

	// Three requests queue in one (account, host) FIFO. The account has ample
	// tokens, so every wait is forced by the shared host bucket.
	queued1 := asyncAcquire(s, "a", "h")
	queued2 := asyncAcquire(s, "a", "h")
	queued3 := asyncAcquire(s, "a", "h")
	if d := s.queueDepth("a", "h"); d != 3 {
		t.Fatalf("queue depth: want 3, got %d", d)
	}

	fc.Advance(time.Second)
	assertGranted(t, queued1, "first queued request").MarkSent()
	assertBlocked(t, queued2, "second still queued")
	assertBlocked(t, queued3, "third still queued")

	fc.Advance(time.Second)
	assertGranted(t, queued2, "second queued request").MarkSent()
	assertBlocked(t, queued3, "third still queued")

	fc.Advance(time.Second)
	assertGranted(t, queued3, "third queued request").MarkSent()
}
