package authbackoff

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func testConfig(clock Clock) Config {
	return Config{
		BaseBackoff: time.Second,
		MaxBackoff:  10 * time.Second,
		MaxFailures: 5,
		Clock:       clock,
	}
}

func TestBackoffGrowsExponentiallyAndExpires(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	const acct = "alice"
	wantWindow := []time.Duration{
		0,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second, // capped by MaxBackoff
		10 * time.Second, // MaxFailures reached -> max window
	}

	for fail := 1; fail <= 6; fail++ {
		clock.Advance(11 * time.Second) // ensure any prior lock has expired
		lease, _, err := mgr.Begin(acct)
		if err != nil {
			t.Fatalf("failure %d: Begin: %v", fail, err)
		}
		if err := lease.End(AuthFailure); err != nil {
			t.Fatalf("failure %d: End: %v", fail, err)
		}
		if got := mgr.Failures(acct); got != uint64(fail) {
			t.Fatalf("failure %d: counter = %d", fail, got)
		}
		lockedUntil := mgr.LockedUntil(acct)
		if got := lockedUntil.Sub(clock.Now()); got != wantWindow[fail] {
			t.Fatalf("failure %d: window = %v, want %v", fail, got, wantWindow[fail])
		}

		// Still inside the window: Begin is rejected with the unlock time.
		if _, unlockAt, err := mgr.Begin(acct); !errors.Is(err, ErrAccountLocked) || !unlockAt.Equal(lockedUntil) {
			t.Fatalf("failure %d: locked Begin = (%v, %v)", fail, unlockAt, err)
		}
	}

	// After the window elapses the account accepts attempts again.
	clock.Advance(10 * time.Second)
	if d := mgr.RetryAfter(acct, clock.Now()); d != 0 {
		t.Fatalf("RetryAfter after expiry = %v, want 0", d)
	}
	lease, _, err := mgr.Begin(acct)
	if err != nil {
		t.Fatalf("Begin after expiry: %v", err)
	}
	if err := lease.End(AuthSuccess); err != nil {
		t.Fatalf("End success: %v", err)
	}
	if mgr.Failures(acct) != 0 || !mgr.LockedUntil(acct).IsZero() {
		t.Fatalf("success did not clear state: %+v", mgr.LockedUntil(acct))
	}
}

func TestConcurrentBeginSerializesReservation(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	const devices = 64
	start := newBarrier()
	var wg sync.WaitGroup
	leases := make([]*Lease, devices)
	errs := make([]error, devices)

	for i := 0; i < devices; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.wait() // all devices call Begin at the same instant
			leases[i], _, errs[i] = mgr.Begin("alice")
		}(i)
	}
	start.release()
	wg.Wait()

	seen := make(map[uint64]bool, devices)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
		if leases[i] == nil || seen[leases[i].Sequence()] {
			t.Fatalf("device %d: duplicate or missing lease", i)
		}
		seen[leases[i].Sequence()] = true
	}
	if len(seen) != devices {
		t.Fatalf("reserved %d unique attempts, want %d", len(seen), devices)
	}
}

func TestConcurrentBeginWhileLockedAllRejected(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	lease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.End(AuthFailure); err != nil {
		t.Fatal(err)
	}

	const devices = 64
	start := newBarrier()
	var wg sync.WaitGroup
	rejected := make([]bool, devices)

	for i := 0; i < devices; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.wait()
			l, _, beginErr := mgr.Begin("alice")
			rejected[i] = errors.Is(beginErr, ErrAccountLocked) && l == nil
		}(i)
	}
	start.release()
	wg.Wait()

	for i, ok := range rejected {
		if !ok {
			t.Fatalf("device %d slipped through the lock", i)
		}
	}
	if mgr.Failures("alice") != 1 {
		t.Fatal("a rejected attempt must not touch the failure counter")
	}
}

func TestStaleSuccessCannotEraseLaterFailure(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	deviceA, _, err := mgr.Begin("alice") // seq 1, slow device
	if err != nil {
		t.Fatal(err)
	}
	deviceB, _, err := mgr.Begin("alice") // seq 2, fast device
	if err != nil {
		t.Fatal(err)
	}

	// The later-started attempt finishes first with a wrong password.
	if err := deviceB.End(AuthFailure); err != nil {
		t.Fatal(err)
	}

	// The older attempt's late success must not wipe the newer failure.
	if err := deviceA.End(AuthSuccess); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != 1 {
		t.Fatalf("stale success erased the failure: counter = %d", mgr.Failures("alice"))
	}
	if mgr.LockedUntil("alice").IsZero() {
		t.Fatal("stale success cleared the backoff window")
	}

	// A genuinely new success (sequence above the failure's) clears state.
	clock.Advance(10 * time.Second)
	deviceC, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := deviceC.End(AuthSuccess); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != 0 || !mgr.LockedUntil("alice").IsZero() {
		t.Fatal("fresh success failed to reset the account")
	}
}

func TestFailureAlwaysCountsEvenWhenFinishedLate(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	first, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}

	// Newer attempt succeeds first.
	if err := second.End(AuthSuccess); err != nil {
		t.Fatal(err)
	}
	// The older attempt then reports a wrong password: evidence an attacker is
	// still guessing, so it must be recorded.
	if err := first.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != 1 {
		t.Fatalf("late failure not counted: %d", mgr.Failures("alice"))
	}
	if mgr.LockedUntil("alice").IsZero() {
		t.Fatal("late failure did not arm backoff")
	}
}

func TestFaultAndTimeoutDoNotPenalizeOrBlockSuccess(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	// Seed one real failure.
	failed, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.End(AuthFailure); err != nil {
		t.Fatal(err)
	}

	clock.Advance(10 * time.Second)
	faulty, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	timedOut, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := faulty.End(SystemFault); err != nil {
		t.Fatal(err)
	}
	if err := timedOut.End(Timeout); err != nil {
		t.Fatal(err)
	}

	if mgr.Failures("alice") != 1 {
		t.Fatalf("fault/timeout changed the counter: %d", mgr.Failures("alice"))
	}

	// Faults and timeouts do not advance the ordering watermark, so the next
	// completed success is still allowed to clear the state.
	retry, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := retry.End(AuthSuccess); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != 0 || !mgr.LockedUntil("alice").IsZero() {
		t.Fatal("fault/timeout blocked the subsequent success reset")
	}

	// A fault finished after a newer failure leaves the failure intact
	// without extending the window.
	slowFault, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	newer, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := newer.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	window := mgr.LockedUntil("alice")
	if err := slowFault.End(SystemFault); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != 1 || !mgr.LockedUntil("alice").Equal(window) {
		t.Fatal("late fault altered penalty state")
	}
}

func TestClockRollbackKeepsLockAndWindow(t *testing.T) {
	start := time.Unix(1000, 0)
	clock := newFakeClock(start)
	mgr := NewManager(testConfig(clock))

	lease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	lockedUntil := mgr.LockedUntil("alice")

	// Wall clock jumps backwards: the existing window stays in force.
	clock.Set(start.Add(-time.Hour))
	if _, _, err := mgr.Begin("alice"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Begin after rollback: %v", err)
	}
	if d := mgr.RetryAfter("alice", clock.Now()); d != lockedUntil.Sub(clock.Now()) || d <= 0 {
		t.Fatalf("RetryAfter after rollback = %v", d)
	}

	// Jumping forward again does not unlock early.
	clock.Set(lockedUntil.Add(-time.Millisecond))
	if _, _, err := mgr.Begin("alice"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Begin just before unlock: %v", err)
	}

	// A failure after expiry followed by another rollback extends, never
	// shrinks, the window.
	clock.Set(lockedUntil)
	next, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := next.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	secondWindow := mgr.LockedUntil("alice")
	clock.Set(lockedUntil.Add(-time.Hour))
	if !mgr.LockedUntil("alice").Equal(secondWindow) {
		t.Fatal("rollback shrank the backoff window")
	}
	if _, _, err := mgr.Begin("alice"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Begin under rolled-back clock: %v", err)
	}
}

func TestFailureCounterSaturates(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))
	mgr.accounts["alice"] = &account{failures: math.MaxUint64 - 1}

	lease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != math.MaxUint64 {
		t.Fatalf("counter = %d, want MaxUint64", mgr.Failures("alice"))
	}

	// Further failures stay saturated instead of wrapping to zero.
	clock.Advance(time.Hour)
	lease, _, err = mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	if mgr.Failures("alice") != math.MaxUint64 {
		t.Fatalf("saturated counter wrapped: %d", mgr.Failures("alice"))
	}
	if window := mgr.LockedUntil("alice").Sub(clock.Now()); window != mgr.cfg.MaxBackoff {
		t.Fatalf("saturated window = %v, want %v", window, mgr.cfg.MaxBackoff)
	}
}

func TestSequenceSaturatesWithoutOverflow(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))
	mgr.accounts["alice"] = &account{seq: math.MaxUint64}

	lease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Sequence() != math.MaxUint64 {
		t.Fatalf("seq = %d, want MaxUint64", lease.Sequence())
	}

	// Two saturated reservations share the sequence; the second lease ending
	// first must not make the first's End a no-op.
	other, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if other.Sequence() != math.MaxUint64 {
		t.Fatalf("seq = %d, want MaxUint64", other.Sequence())
	}
	if err := other.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	if err := lease.End(AuthSuccess); err != nil {
		t.Fatalf("saturated-seq success should still be accepted: %v", err)
	}
	// Success at the same saturated sequence is not older, so it clears.
	if mgr.Failures("alice") != 0 {
		t.Fatalf("counter = %d, want 0", mgr.Failures("alice"))
	}
}

func TestDoubleEndAndUnknownOutcome(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(testConfig(clock))

	lease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.End(Outcome(99)); err == nil {
		t.Fatal("unknown outcome must error")
	}
	if err := lease.End(AuthFailure); err != nil {
		t.Fatal(err)
	}
	if err := lease.End(AuthFailure); !errors.Is(err, ErrLeaseFinished) {
		t.Fatalf("second End = %v, want ErrLeaseFinished", err)
	}
	if mgr.Failures("alice") != 1 {
		t.Fatalf("double End counted twice: %d", mgr.Failures("alice"))
	}
}

func TestInterleavedBeginCannotBypassFreshLock(t *testing.T) {
	// Deterministically interleave two devices using a clock hook. Device B
	// samples the clock (and blocks) before device A finishes; A then reserves
	// an attempt and arms backoff before B's lock check runs.
	baseClock := newFakeClock(time.Unix(1000, 0))
	bSampled := make(chan struct{})
	releaseB := make(chan struct{})
	var calls int
	clock := &hookClock{
		inner: baseClock,
		hook: func() {
			calls++
			if calls == 1 {
				close(bSampled) // device B has entered Begin
				<-releaseB
			}
		},
	}
	mgr := NewManager(testConfig(clock))

	var (
		wg      sync.WaitGroup
		bLease  *Lease
		bUnlock time.Time
		bErr    error
	)

	// Device B starts first and parks on the second clock sample overall.
	wg.Add(1)
	go func() {
		defer wg.Done()
		bLease, bUnlock, bErr = mgr.Begin("alice")
	}()
	<-bSampled // B is parked inside Begin, before its lock check

	// Device A runs to completion and records a failure. Its clock sample is
	// the second one, so it never blocks on the hook.
	aLease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatalf("device A Begin: %v", err)
	}
	if err := aLease.End(AuthFailure); err != nil {
		t.Fatalf("device A End: %v", err)
	}

	// Now let device B reach the lock check: the lock A just armed must win.
	close(releaseB)
	wg.Wait()

	if !errors.Is(bErr, ErrAccountLocked) || bLease != nil {
		t.Fatalf("device B bypassed the lock: lease=%v err=%v", bLease, bErr)
	}
	if !bUnlock.Equal(mgr.LockedUntil("alice")) {
		t.Fatalf("unlock hint %v != lockedUntil %v", bUnlock, mgr.LockedUntil("alice"))
	}
	if mgr.Failures("alice") != 1 {
		t.Fatalf("counter = %d, want 1", mgr.Failures("alice"))
	}
}

func TestManyDevicesStress(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	mgr := NewManager(Config{
		BaseBackoff: time.Millisecond,
		MaxBackoff:  100 * time.Millisecond,
		MaxFailures: 10,
		Clock:       clock,
	})

	const devices = 200
	const rounds = 8
	start := newBarrier()
	var wg sync.WaitGroup

	for d := 0; d < devices; d++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			start.wait()
			for r := 0; r < rounds; r++ {
				lease, _, err := mgr.Begin("alice")
				if err != nil {
					if !errors.Is(err, ErrAccountLocked) {
						t.Errorf("unexpected Begin error: %v", err)
						return
					}
					continue
				}
				switch (seed + r) % 7 {
				case 0:
					err = lease.End(AuthSuccess)
				case 1, 2, 3:
					err = lease.End(AuthFailure)
				case 4, 5:
					err = lease.End(SystemFault)
				default:
					err = lease.End(Timeout)
				}
				if err != nil {
					t.Errorf("End: %v", err)
					return
				}
			}
		}(d)
	}

	start.release()

	// Advance the fake clock concurrently while attempts run, exercising
	// time movement under contention.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				clock.Advance(time.Millisecond)
			}
		}
	}()
	wg.Wait()
	close(stop)

	failures := mgr.Failures("alice")
	if failures > uint64(devices*rounds) {
		t.Fatalf("impossible failure count: %d", failures)
	}
	// Repeated End must be impossible; verify a fresh lease still works.
	clock.Advance(time.Hour)
	lease, _, err := mgr.Begin("alice")
	if err != nil {
		t.Fatalf("final Begin: %v", err)
	}
	if err := lease.End(AuthSuccess); err != nil {
		t.Fatalf("final End: %v", err)
	}
	if mgr.Failures("alice") != 0 {
		t.Fatalf("counter after final success = %d", mgr.Failures("alice"))
	}
}

func TestDefaultsNilAccountAndShiftOverflow(t *testing.T) {
	// Zero Config gets safe defaults and the wall clock.
	mgr := NewManager(Config{})
	if mgr.cfg.BaseBackoff != time.Second || mgr.cfg.MaxBackoff != 30*time.Minute {
		t.Fatalf("defaults not applied: %+v", mgr.cfg)
	}
	if mgr.cfg.Clock == nil {
		t.Fatal("default clock not installed")
	}

	// Unknown accounts read as unlocked with a zero counter.
	if mgr.Failures("ghost") != 0 || !mgr.LockedUntil("ghost").IsZero() {
		t.Fatal("unknown account must report zero state")
	}
	if d := mgr.RetryAfter("ghost", time.Unix(1, 0)); d != 0 {
		t.Fatalf("unknown account RetryAfter = %v", d)
	}

	// Very large failure counts must hit the cap without an overflowing shift.
	clock := newFakeClock(time.Unix(1000, 0))
	overflowMgr := NewManager(testConfig(clock))
	overflowMgr.accounts["alice"] = &account{failures: 1 << 30}
	if got := overflowMgr.backoffWindow(1 << 30); got != overflowMgr.cfg.MaxBackoff {
		t.Fatalf("huge failure window = %v, want %v", got, overflowMgr.cfg.MaxBackoff)
	}
	if got := overflowMgr.backoffWindow(0); got != 0 {
		t.Fatalf("zero failure window = %v, want 0", got)
	}

	// MaxBackoff below BaseBackoff is raised instead of producing zero windows.
	weird := NewManager(Config{BaseBackoff: 5 * time.Second, MaxBackoff: time.Second})
	if weird.cfg.MaxBackoff < weird.cfg.BaseBackoff {
		t.Fatal("MaxBackoff must never be below BaseBackoff")
	}

	// A tiny base duration shifted past the int64 range overflows to a
	// non-positive window and must fall back to MaxBackoff.
	tiny := NewManager(Config{BaseBackoff: time.Nanosecond, MaxBackoff: time.Second})
	if got := tiny.backoffWindow(62); got != tiny.cfg.MaxBackoff {
		t.Fatalf("overflowing window = %v, want %v", got, tiny.cfg.MaxBackoff)
	}
}
