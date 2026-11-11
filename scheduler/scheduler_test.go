package scheduler

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakeLookup struct {
	mu       sync.Mutex
	accounts map[AccountID]*Account
	errFor   map[AccountID]error
	calls    int
}

func newFakeLookup() *fakeLookup {
	return &fakeLookup{
		accounts: make(map[AccountID]*Account),
		errFor:   make(map[AccountID]error),
	}
}

func (f *fakeLookup) withAccount(acc *Account) *fakeLookup {
	f.accounts[acc.ID] = acc
	return f
}

func (f *fakeLookup) Lookup(_ context.Context, _ TenantID, id AccountID) (*Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err, ok := f.errFor[id]; ok {
		return nil, err
	}
	if acc, ok := f.accounts[id]; ok {
		return acc, nil
	}
	return nil, nil
}

type instantVerifier struct {
	mu        sync.Mutex
	seen      []*Account
	allow     map[AccountID]bool
	callCount int
}

func (v *instantVerifier) Verify(_ context.Context, acc *Account, _ []byte) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.callCount++
	v.seen = append(v.seen, acc)
	return v.allow[acc.ID], nil
}

func (v *instantVerifier) calls() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.callCount
}

type blockingCall struct {
	account *Account
	secret  []byte
	release chan struct{}
	once    sync.Once
}

func (c *blockingCall) fire() { c.once.Do(func() { close(c.release) }) }

type blockingVerifier struct {
	mu    sync.Mutex
	calls []*blockingCall
}

func newBlockingVerifier() *blockingVerifier { return &blockingVerifier{} }

func (v *blockingVerifier) Verify(_ context.Context, acc *Account, secret []byte) (bool, error) {
	call := &blockingCall{
		account: acc,
		secret:  append([]byte(nil), secret...),
		release: make(chan struct{}),
	}
	v.mu.Lock()
	v.calls = append(v.calls, call)
	v.mu.Unlock()

	<-call.release
	return false, nil
}

func (v *blockingVerifier) nth(n int) *blockingCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	if n >= len(v.calls) {
		return nil
	}
	return v.calls[n]
}

func (v *blockingVerifier) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.calls)
}

func testScheduler(t *testing.T, lookup LookupFunc, verify VerifyFunc, clock Clock, perTenant, global int, delay time.Duration) *Scheduler {
	t.Helper()
	s, err := New(Config{
		PerTenantCap: perTenant,
		GlobalTokens: global,
		MinDelay:     delay,
		Lookup:       lookup,
		Verify:       verify,
		Dummy:        &Account{ID: "dummy", Record: "dummy-record"},
		Clock:        clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func runAuth(ctx context.Context, s *Scheduler, tenant TenantID, account AccountID, secret []byte) chan error {
	ch := make(chan error, 1)
	go func() { ch <- s.Authenticate(ctx, tenant, account, secret) }()
	return ch
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("condition not met: %s", msg)
		case <-time.After(time.Millisecond):
		}
	}
}

func assertNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: now %d, baseline %d", runtime.NumGoroutine(), baseline)
}

// Unknown accounts take the fixed dummy record and, like every other failed
// outcome, surface the single ErrDenied only after the uniform minimum delay.
func TestUnknownAccountDummyAndUniformFailure(t *testing.T) {
	lookup := newFakeLookup()
	realAcc := &Account{ID: "alice", Record: "alice-record"}
	lookup.accounts["alice"] = realAcc
	lookup.errFor["broken"] = errors.New("lookup boom")

	// All subtests expect failure; the standalone success section below builds
	// its own allowing verifier.
	verifier := &instantVerifier{allow: map[AccountID]bool{}}

	cases := []struct {
		name    string
		account AccountID
		wantErr error
	}{
		{"unknown account", "ghost", ErrDenied},
		{"wrong secret", "alice", ErrDenied},
		{"lookup error", "broken", ErrDenied},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(time.Unix(1_700_000_000, 0))
			s := testScheduler(t, lookup.Lookup, verifier.Verify, clock, 4, 8, 50*time.Millisecond)

			before := clock.Now()
			errCh := runAuth(context.Background(), s, "tenant-a", tc.account, []byte("secret"))

			select {
			case err := <-errCh:
				t.Fatalf("returned %v before minimum delay", err)
			case <-time.After(20 * time.Millisecond):
			}

			if !clock.waitForTimers(1, 2*time.Second) {
				t.Fatal("floor timer was never registered")
			}
			clock.Advance(50 * time.Millisecond)
			select {
			case err := <-errCh:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Authenticate did not return after advancing the clock")
			}

			if clock.Now().Sub(before) < 50*time.Millisecond {
				t.Fatalf("elapsed %v < MinDelay", clock.Now().Sub(before))
			}
		})
	}

	verifier.mu.Lock()
	if len(verifier.seen) != 3 {
		t.Fatalf("verifier calls = %d, want 3", len(verifier.seen))
	}
	for _, acc := range verifier.seen {
		if acc.ID != "dummy" && acc.ID != "alice" {
			t.Fatalf("unexpected verified account %q", acc.ID)
		}
	}
	verifier.mu.Unlock()

	// Success is also floored to the same minimum delay.
	clock := NewFakeClock(time.Unix(1_700_000_000, 0))
	allowing := &instantVerifier{allow: map[AccountID]bool{"alice": true}}
	s := testScheduler(t, lookup.Lookup, allowing.Verify, clock, 4, 8, 50*time.Millisecond)
	before := clock.Now()
	okCh := runAuth(context.Background(), s, "tenant-a", "alice", []byte("correct"))
	if !clock.waitForTimers(1, 2*time.Second) {
		t.Fatal("floor timer was never registered")
	}
	clock.Advance(50 * time.Millisecond)
	if err := <-okCh; err != nil {
		t.Fatalf("successful auth: %v", err)
	}
	if clock.Now().Sub(before) < 50*time.Millisecond {
		t.Fatal("success returned before MinDelay")
	}
}

// Tenant slots, global tokens, goroutines and secret copies must all balance
// back to zero across completion and both queued-cancellation phases.
func TestResourceConservation(t *testing.T) {
	baseline := runtime.NumGoroutine()

	clock := NewFakeClock(time.Now())
	verifier := newBlockingVerifier()
	s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 1, 1, 0)

	if s.liveSecrets() != 0 {
		t.Fatalf("initial live secrets = %d, want 0", s.liveSecrets())
	}

	// Saturate the single tenant + global slot with an in-flight verification.
	first := runAuth(context.Background(), s, "t", "a", []byte("secret-1"))
	waitFor(t, func() bool { return verifier.count() == 1 }, "first verification entered")
	if s.tenantInflight("t") != 1 || s.globalInflight() != 1 {
		t.Fatalf("inflight = tenant %d global %d, want 1/1", s.tenantInflight("t"), s.globalInflight())
	}

	// Tenant-queue phase: second request waits and holds nothing.
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	second := runAuth(queuedCtx, s, "t", "b", []byte("secret-2"))
	waitFor(t, func() bool { return s.tenantQueued("t") == 1 }, "second request queued on tenant semaphore")

	cancelQueued()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("tenant-queue cancel err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tenant-queued request did not return after cancel")
	}
	waitFor(t, func() bool { return s.tenantQueued("t") == 0 }, "tenant queue drained")
	if verifier.count() != 1 {
		t.Fatalf("verifier calls = %d, want 1; queued work must not start", verifier.count())
	}
	if s.liveSecrets() != 1 {
		t.Fatalf("live secrets after queued cancel = %d, want 1", s.liveSecrets())
	}
	if !s.lastScrubbed.Load() {
		t.Fatal("canceled queued secret buffer was not zeroed")
	}

	// Finish the in-flight verification: both slots return to the pools.
	verifier.nth(0).fire()
	select {
	case err := <-first:
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("first err = %v, want ErrDenied", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not return")
	}
	waitFor(t, func() bool {
		return s.tenantInflight("t") == 0 && s.globalInflight() == 0
	}, "slots released after completion")
	waitFor(t, func() bool { return s.liveSecrets() == 0 }, "secret copies released")
	if !s.lastScrubbed.Load() {
		t.Fatal("completed secret buffer was not zeroed")
	}

	// Global-queue phase: occupy the single global token from tenant A, then
	// tenant B (free tenant slot) queues on the global semaphore.
	g1 := runAuth(context.Background(), s, "A", "x", []byte("gx"))
	waitFor(t, func() bool { return verifier.count() == 2 }, "global holder entered")

	gCtx, cancelGlobal := context.WithCancel(context.Background())
	g2 := runAuth(gCtx, s, "B", "y", []byte("gy"))
	waitFor(t, func() bool { return s.globalQueued() == 1 }, "global token waiter queued")
	if s.globalInflight() != 1 {
		t.Fatalf("global inflight = %d, want 1", s.globalInflight())
	}

	cancelGlobal()
	select {
	case err := <-g2:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("global-queue cancel err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("global-queued request did not return after cancel")
	}
	waitFor(t, func() bool { return s.globalQueued() == 0 }, "global queue drained")
	if verifier.count() != 2 {
		t.Fatalf("verifier calls = %d, want 2; global-queued work must not start", verifier.count())
	}

	verifier.nth(1).fire()
	<-g1
	waitFor(t, func() bool { return s.globalInflight() == 0 && s.liveSecrets() == 0 }, "final cleanup")

	assertNoGoroutineLeak(t, baseline)
}

// Fair FIFO admission drains a saturated tenant instead of starving it, and
// one tenant cannot consume another tenant's concurrency budget.
func TestStarvationAndTenantIsolation(t *testing.T) {
	t.Run("strict FIFO per tenant", func(t *testing.T) {
		clock := NewFakeClock(time.Now())
		verifier := newBlockingVerifier()
		s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 1, 16, 0)

		first := runAuth(context.Background(), s, "t", "a0", []byte("p0"))
		waitFor(t, func() bool { return verifier.count() == 1 }, "first entered")

		queued := make([]chan error, 5)
		for i := range queued {
			queued[i] = runAuth(context.Background(), s, "t", AccountID("q"), []byte("p"))
		}
		waitFor(t, func() bool { return s.tenantQueued("t") == 5 }, "five requests queued")

		// Releasing the head admits waiters strictly in FIFO order and keeps
		// the tenant at its cap rather than bursting.
		for i := 0; i < 5; i++ {
			verifier.nth(i).fire()
			waitFor(t, func() bool { return verifier.count() == i+2 }, "next FIFO waiter admitted")
			if s.tenantInflight("t") != 1 {
				t.Fatalf("inflight = %d, want exactly 1", s.tenantInflight("t"))
			}
		}
		verifier.nth(5).fire()
		<-first
		for _, ch := range queued {
			<-ch
		}
		waitFor(t, func() bool { return s.tenantInflight("t") == 0 && s.tenantQueued("t") == 0 }, "tenant drained")
		if verifier.count() != 6 {
			t.Fatalf("verifier calls = %d, want 6 (no starvation, no overshoot)", verifier.count())
		}
	})

	t.Run("tenant caps are independent", func(t *testing.T) {
		clock := NewFakeClock(time.Now())
		verifier := newBlockingVerifier()
		s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 2, 16, 0)

		// Saturate tenant A.
		aErr := make([]chan error, 2)
		for i := range aErr {
			aErr[i] = runAuth(context.Background(), s, "A", AccountID("a"), []byte("p"))
		}
		waitFor(t, func() bool { return verifier.count() == 2 }, "tenant A saturated")
		if s.tenantInflight("A") != 2 {
			t.Fatalf("tenant A inflight = %d, want 2", s.tenantInflight("A"))
		}

		// Tenant B must still get its own full cap despite A being full.
		bErr := make([]chan error, 2)
		for i := range bErr {
			bErr[i] = runAuth(context.Background(), s, "B", AccountID("b"), []byte("p"))
		}
		waitFor(t, func() bool { return verifier.count() == 4 }, "tenant B admitted independently")
		if s.tenantInflight("B") != 2 {
			t.Fatalf("tenant B inflight = %d, want 2 despite saturated A", s.tenantInflight("B"))
		}
		if s.globalInflight() != 4 {
			t.Fatalf("global inflight = %d, want 4", s.globalInflight())
		}

		for i := 0; i < 4; i++ {
			verifier.nth(i).fire()
		}
		for _, ch := range append(aErr, bErr...) {
			<-ch
		}
		waitFor(t, func() bool { return s.globalInflight() == 0 }, "all global tokens returned")
	})

	t.Run("global token cap is never exceeded", func(t *testing.T) {
		clock := NewFakeClock(time.Now())
		verifier := newBlockingVerifier()
		s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 4, 3, 0)

		errs := make([]chan error, 6)
		for i := range errs {
			errs[i] = runAuth(context.Background(), s, TenantID("t"+string(rune('A'+i))), AccountID("x"), []byte("p"))
		}
		waitFor(t, func() bool { return verifier.count() == 3 }, "exactly global cap admitted")
		if s.globalInflight() != 3 {
			t.Fatalf("global inflight = %d, want 3", s.globalInflight())
		}
		waitFor(t, func() bool { return s.globalQueued() == 3 }, "remaining requests wait on global tokens")

		// Each completion admits exactly one queued global waiter.
		for i := 0; i < 3; i++ {
			verifier.nth(i).fire()
			waitFor(t, func() bool { return verifier.count() == 4+i }, "global slot reused in order")
			if s.globalInflight() != 3 {
				t.Fatalf("global inflight = %d, want steady 3", s.globalInflight())
			}
		}
		for i := 3; i < 6; i++ {
			verifier.nth(i).fire()
		}
		for _, ch := range errs {
			<-ch
		}
		waitFor(t, func() bool { return s.globalInflight() == 0 && s.globalQueued() == 0 }, "global pool drained")
	})
}

// Cancellation has two distinct semantics: a queued cancel aborts before the
// verification starts, while a cancel after admission cannot interrupt the
// already-started verification, which always runs to completion.
func TestCancellationDistinction(t *testing.T) {
	t.Run("queued cancel never starts verification", func(t *testing.T) {
		baseline := runtime.NumGoroutine()
		clock := NewFakeClock(time.Now())
		verifier := newBlockingVerifier()
		s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 1, 4, 0)

		holder := runAuth(context.Background(), s, "t", "a", []byte("p"))
		waitFor(t, func() bool { return verifier.count() == 1 }, "holder entered")

		ctx, cancel := context.WithCancel(context.Background())
		waiter := runAuth(ctx, s, "t", "b", []byte("queued-secret"))
		waitFor(t, func() bool { return s.tenantQueued("t") == 1 }, "waiter queued")

		cancel()
		select {
		case err := <-waiter:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("queued cancel err = %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("queued cancel did not return promptly")
		}
		if verifier.count() != 1 {
			t.Fatalf("verifier calls = %d, want 1; queued verification must not start", verifier.count())
		}
		waitFor(t, func() bool { return s.liveSecrets() == 1 && s.lastScrubbed.Load() }, "queued secret scrubbed")

		verifier.nth(0).fire()
		<-holder
		waitFor(t, func() bool { return s.tenantInflight("t") == 0 && s.liveSecrets() == 0 }, "cleanup")
		assertNoGoroutineLeak(t, baseline)
	})

	t.Run("running verification is uninterruptible", func(t *testing.T) {
		baseline := runtime.NumGoroutine()
		clock := NewFakeClock(time.Now())
		verifier := newBlockingVerifier()
		// A large floor so the terminal delay is observable with the fake clock.
		s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 1, 4, 100*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		errCh := runAuth(ctx, s, "t", "a", []byte("running-secret"))
		waitFor(t, func() bool { return verifier.count() == 1 }, "verification started")

		cancel()
		// The result must not appear while the verification is still blocked,
		// proving the in-flight check cannot be interrupted.
		select {
		case err := <-errCh:
			t.Fatalf("returned %v before uninterruptible verification finished", err)
		case <-time.After(30 * time.Millisecond):
		}
		if verifier.count() != 1 {
			t.Fatalf("verifier calls changed after cancel: %d", verifier.count())
		}

		// Complete the verification. The uniform failure is then held until the
		// minimum delay even though the caller already canceled.
		verifier.nth(0).fire()
		if !clock.waitForTimers(1, 2*time.Second) {
			t.Fatal("floor timer was never registered after verification")
		}
		select {
		case <-errCh:
			t.Fatal("returned before uniform minimum delay")
		case <-time.After(30 * time.Millisecond):
		}
		clock.Advance(100 * time.Millisecond)
		select {
		case err := <-errCh:
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("post-cancel err = %v, want uniform ErrDenied", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("did not return after floor elapsed")
		}
		waitFor(t, func() bool { return s.liveSecrets() == 0 && s.lastScrubbed.Load() }, "running secret scrubbed")
		assertNoGoroutineLeak(t, baseline)
	})

	t.Run("deadline while queued reports deadline error", func(t *testing.T) {
		clock := NewFakeClock(time.Now())
		verifier := newBlockingVerifier()
		s := testScheduler(t, newFakeLookup().Lookup, verifier.Verify, clock, 1, 4, 0)

		holder := runAuth(context.Background(), s, "t", "a", []byte("p"))
		waitFor(t, func() bool { return verifier.count() == 1 }, "holder entered")

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := s.Authenticate(ctx, "t", "b", []byte("p"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}

		verifier.nth(0).fire()
		<-holder
	})
}

func TestConfigValidation(t *testing.T) {
	lookup := newFakeLookup().Lookup
	verifier := (&instantVerifier{}).Verify
	dummy := &Account{ID: "dummy"}

	base := Config{PerTenantCap: 2, GlobalTokens: 4, MinDelay: time.Millisecond, Lookup: lookup, Verify: verifier, Dummy: dummy}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero tenant cap", func(c *Config) { c.PerTenantCap = 0 }},
		{"negative global tokens", func(c *Config) { c.GlobalTokens = -1 }},
		{"negative min delay", func(c *Config) { c.MinDelay = -1 }},
		{"missing lookup", func(c *Config) { c.Lookup = nil }},
		{"missing verify", func(c *Config) { c.Verify = nil }},
		{"missing dummy", func(c *Config) { c.Dummy = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("New returned nil error, want validation failure")
			}
		})
	}

	s, err := New(base)
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if _, ok := s.cfg.Clock.(RealClock); !ok {
		t.Fatalf("default clock = %T, want RealClock", s.cfg.Clock)
	}
}

// A verifier callback error must collapse into the same uniform ErrDenied.
func TestVerifierErrorIsUniformDenial(t *testing.T) {
	clock := NewFakeClock(time.Unix(1_700_000_000, 0))
	lookup := newFakeLookup()
	lookup.accounts["alice"] = &Account{ID: "alice"}
	failVerify := func(context.Context, *Account, []byte) (bool, error) {
		return false, errors.New("bcrypt exploded")
	}
	s := testScheduler(t, lookup.Lookup, failVerify, clock, 2, 4, 25*time.Millisecond)

	errCh := runAuth(context.Background(), s, "t", "alice", []byte("p"))
	if !clock.waitForTimers(1, 2*time.Second) {
		t.Fatal("floor timer was never registered")
	}
	clock.Advance(25 * time.Millisecond)
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("verifier error produced %v, want ErrDenied", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Authenticate did not return")
	}
}

// Smoke-test the real clock path and timer abstractions end to end.
func TestRealClockEndToEnd(t *testing.T) {
	verifier := &instantVerifier{allow: map[AccountID]bool{"alice": true}}
	s := testScheduler(t, newFakeLookup().
		withAccount(&Account{ID: "alice"}).Lookup,
		verifier.Verify, RealClock{}, 2, 4, 15*time.Millisecond)

	start := time.Now()
	if err := s.Authenticate(context.Background(), "t", "alice", []byte("p")); err != nil {
		t.Fatalf("auth: %v", err)
	}
	if time.Since(start) < 15*time.Millisecond {
		t.Fatal("real clock did not enforce MinDelay")
	}

	// Directly exercise Timer.Stop on both clock implementations.
	rt := RealClock{}.NewTimer(time.Hour)
	if !rt.Stop() {
		t.Fatal("real timer Stop returned false for a live timer")
	}

	fc := NewFakeClock(time.Now())
	ft := fc.NewTimer(time.Hour)
	if !ft.Stop() {
		t.Fatal("fake timer Stop returned false for a live timer")
	}
	if fc.pending() != 0 {
		t.Fatalf("pending after Stop = %d, want 0", fc.pending())
	}
	fc.Advance(2 * time.Hour)
	select {
	case <-ft.Chan():
		t.Fatal("stopped fake timer fired")
	default:
	}
}
