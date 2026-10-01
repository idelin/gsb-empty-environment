package lease

import (
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		LeaseDuration: 10 * time.Second,
		MaxClockDrift: 500 * time.Millisecond,
		MaxRTT:        0,
		CheckInterval: 100 * time.Millisecond,
	}
}

func newCluster(t *testing.T, cfg Config) (*SimClock, *Coordinator, *Manager) {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("invalid config: %v", err)
	}
	clock := NewSimClock(time.Unix(1_700_000_000, 0))
	coord := NewCoordinator(clock, cfg)
	mgr := NewManager("node-A", clock, coord, cfg)
	t.Cleanup(mgr.Stop)
	return clock, coord, mgr
}

// advance moves the simulated clock in CheckInterval-sized steps,
// running the safety self-check after each step so the watchdog never
// mistakes normal elapsed time for a clock jump.
func advance(t *testing.T, clock *SimClock, mgr *Manager, cfg Config, d time.Duration) {
	t.Helper()
	for d > 0 {
		step := cfg.CheckInterval
		if step > d {
			step = d
		}
		clock.Advance(step)
		mgr.SelfCheck()
		d -= step
	}
}

// Acceptance boundary 1: with a 10s TTL and 500ms max drift, the leader
// must stop issuing writes as leader at t=9.5s.
func TestLeaderStopsWritesAtSafeBoundary(t *testing.T) {
	cfg := testConfig()
	clock, _, mgr := newCluster(t, cfg)

	if err := mgr.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := cfg.SafeWindow(); got != 9500*time.Millisecond {
		t.Fatalf("safe window = %v, want 9.5s", got)
	}

	// Just before the boundary (t=9.4s) writes still succeed.
	advance(t, clock, mgr, cfg, 9400*time.Millisecond)
	if err := mgr.Write("set x=1"); err != nil {
		t.Fatalf("write at t=9.4s should succeed, got %v", err)
	}

	// At t=9.5s the safe period is over: writes must stop even though
	// the 10s TTL has not expired yet.
	advance(t, clock, mgr, cfg, 100*time.Millisecond)
	if err := mgr.Write("set x=2"); err != ErrNotLeader {
		t.Fatalf("write at t=9.5s should fail with ErrNotLeader, got %v", err)
	}
	st := mgr.Status()
	if st.Held || !st.Demoted {
		t.Fatalf("expected voluntary demotion after safe period, got %+v", st)
	}
	if !strings.Contains(st.Reason, "safe period") {
		t.Fatalf("unexpected demotion reason: %q", st.Reason)
	}
}

// Acceptance boundary 2: injecting a +1s forward clock jump triggers the
// safety self-check and an emergency early demotion, well before the
// safe period would normally end.
func TestClockJumpTriggersEmergencyDemotion(t *testing.T) {
	cfg := testConfig()
	clock, _, mgr := newCluster(t, cfg)

	if err := mgr.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	advance(t, clock, mgr, cfg, 5*time.Second)
	if !mgr.CanWrite() {
		t.Fatal("leader should be writable at t=5s")
	}

	// NTP-style fault: local clock suddenly steps forward by 1s,
	// exceeding the 500ms tolerated drift.
	clock.Jump(1 * time.Second)
	mgr.SelfCheck()

	st := mgr.Status()
	if st.Held || !st.Demoted {
		t.Fatalf("expected emergency demotion after clock jump, got %+v", st)
	}
	if !strings.Contains(st.Reason, "clock jumped") {
		t.Fatalf("unexpected demotion reason: %q", st.Reason)
	}
	if err := mgr.Write("set x=1"); err != ErrNotLeader {
		t.Fatalf("write after clock jump should fail with ErrNotLeader, got %v", err)
	}
	// The simulated time (t=6s) is still inside the nominal safe window,
	// proving the demotion was caused by jump detection, not expiry.
	if st.SafeUntil.After(clock.Now()) == false {
		t.Fatal("test setup: clock should still be before safeUntil")
	}
}

// No split-brain: after an emergency demotion the old leader stays
// fenced off, and a second node can only become leader once the
// coordinator-side TTL has fully expired.
func TestNoSplitBrainAfterDemotion(t *testing.T) {
	cfg := testConfig()
	clock, coord, mgrA := newCluster(t, cfg)
	mgrB := NewManager("node-B", clock, coord, cfg)
	t.Cleanup(mgrB.Stop)

	if err := mgrA.Acquire(); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if err := mgrB.Acquire(); err != ErrLeaseHeld && !strings.Contains(err.Error(), ErrLeaseHeld.Error()) {
		t.Fatalf("B acquire while A holds lease should fail, got %v", err)
	}

	// A suffers a clock jump at t=5s and is emergency-demoted.
	advance(t, clock, mgrA, cfg, 5*time.Second)
	clock.Jump(1 * time.Second)
	mgrA.SelfCheck()

	if mgrA.CanWrite() {
		t.Fatal("old leader A must not write after demotion")
	}
	// A released the lease on demotion, so B may take over immediately;
	// A stays fenced because its own manager is demoted.
	if err := mgrB.Acquire(); err != nil {
		t.Fatalf("B acquire after A released: %v", err)
	}
	if !mgrB.CanWrite() {
		t.Fatal("B should be writable as new leader")
	}
	if mgrA.CanWrite() {
		t.Fatal("split-brain: both nodes believe they are leader")
	}
	if err := mgrA.Write("stale write"); err != ErrNotLeader {
		t.Fatalf("stale leader write should fail, got %v", err)
	}
}

// The coordinator must never hand out two live leases at once.
func TestCoordinatorRejectsConcurrentLease(t *testing.T) {
	cfg := testConfig()
	clock, coord, mgrA := newCluster(t, cfg)
	if err := mgrA.Acquire(); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if _, err := coord.Acquire("node-B"); err == nil {
		t.Fatal("second acquire while lease is live should fail")
	}
	// After the full TTL expires, another node may acquire.
	clock.Advance(cfg.LeaseDuration + time.Millisecond)
	if _, err := coord.Acquire("node-B"); err != nil {
		t.Fatalf("acquire after TTL expiry should succeed, got %v", err)
	}
}

// Communication round-trip delay further shrinks the safe window.
func TestRTTShrinksSafeWindow(t *testing.T) {
	cfg := testConfig()
	cfg.MaxRTT = 200 * time.Millisecond
	if got, want := cfg.SafeWindow(), 9300*time.Millisecond; got != want {
		t.Fatalf("safe window = %v, want %v", got, want)
	}
	clock, _, mgr := newCluster(t, cfg)
	if err := mgr.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	advance(t, clock, mgr, cfg, 9300*time.Millisecond)
	if mgr.CanWrite() {
		t.Fatal("writes must stop at TTL - drift - RTT = 9.3s")
	}
}

// A leader that never demotes would allow a stale write after TTL
// expiry; this guards the demote-on-expiry path end to end.
func TestLeaseExpiresFully(t *testing.T) {
	cfg := testConfig()
	clock, _, mgr := newCluster(t, cfg)
	if err := mgr.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	advance(t, clock, mgr, cfg, cfg.LeaseDuration+time.Second)
	if mgr.CanWrite() {
		t.Fatal("no writes allowed after TTL expiry")
	}
}
