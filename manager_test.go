package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func scriptedGen(codes ...string) CodeGenerator {
	i := 0
	return func() (string, error) {
		if i >= len(codes) {
			return "", errors.New("scripted generator exhausted")
		}
		code := codes[i]
		i++
		return code, nil
	}
}

func newTestManager(t *testing.T, clock *FakeClock, opts ...Option) *Manager {
	t.Helper()
	all := append([]Option{
		WithClock(clock),
		WithLifetime(time.Minute),
		WithPollInterval(5 * time.Second),
	}, opts...)
	return NewManager(all...)
}

func TestGrantLifecycleApprove(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock)

	grant, err := mgr.CreateGrant()
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	if grant.DeviceCode == "" || grant.UserCode == "" || grant.DeviceCode == grant.UserCode {
		t.Fatalf("device and user codes must be distinct non-empty strings: %+v", grant)
	}
	if grant.State != StatePending {
		t.Fatalf("new grant state = %v, want pending", grant.State)
	}

	view, err := mgr.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if view.State != StatePending {
		t.Fatalf("Poll state = %v, want pending", view.State)
	}

	if _, err := mgr.Claim(grant.UserCode); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	approved, err := mgr.Approve(grant.UserCode)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.State != StateApproved {
		t.Fatalf("Approve state = %v, want approved", approved.State)
	}

	// Terminal state is sticky and stays readable through polling.
	for i := 0; i < 3; i++ {
		view, err := mgr.Poll(grant.DeviceCode)
		if err != nil {
			t.Fatalf("terminal Poll %d: %v", i, err)
		}
		if view.State != StateApproved {
			t.Fatalf("terminal Poll %d state = %v, want approved", i, view.State)
		}
	}
}

func TestDenyIsTerminal(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()
	if _, err := mgr.Claim(grant.UserCode); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	denied, err := mgr.Deny(grant.UserCode)
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if denied.State != StateDenied {
		t.Fatalf("Deny state = %v", denied.State)
	}

	if _, err := mgr.Approve(grant.UserCode); !errors.Is(err, ErrTerminal) {
		t.Fatalf("Approve after deny err = %v, want ErrTerminal", err)
	}
	view, err := mgr.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after deny: %v", err)
	}
	if view.State != StateDenied {
		t.Fatalf("Poll state = %v, want denied (single terminal state)", view.State)
	}
}

func TestSlowDownBacksOffWithoutExtendingExpiry(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()
	expiresAt := grant.ExpiresAt

	// First poll is immediately allowed and schedules the next one.
	if _, err := mgr.Poll(grant.DeviceCode); err != nil {
		t.Fatalf("first Poll: %v", err)
	}

	previous := start.Add(5 * time.Second)
	for i := 0; i < 3; i++ {
		if _, err := mgr.Poll(grant.DeviceCode); !errors.Is(err, ErrPollingTooFast) {
			t.Fatalf("fast Poll %d err = %v, want ErrPollingTooFast", i, err)
		}
	}

	// Each slow_down must push the earliest next-poll instant forward.
	stored := mgr.byDevice[grant.DeviceCode]
	stored.mu.Lock()
	nextPollAt := stored.nextPollAt
	gotExpiresAt := stored.expiresAt
	stored.mu.Unlock()
	if !nextPollAt.After(previous) {
		t.Fatalf("NextPollAt = %v, want after %v", nextPollAt, previous)
	}
	if !gotExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt moved to %v, want unchanged %v", gotExpiresAt, expiresAt)
	}

	// After waiting until the backoff instant, polling is allowed again.
	clock.Set(nextPollAt)
	view, err := mgr.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after waiting: %v", err)
	}
	if view.State != StatePending {
		t.Fatalf("Poll state = %v, want pending", view.State)
	}
}

func TestConcurrentPollsCannotBypassInterval(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()

	const n = 50
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	results := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			_, err := mgr.Poll(grant.DeviceCode)
			results <- err
		}()
	}
	close(startGate)
	wg.Wait()
	close(results)

	successes, tooFast := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrPollingTooFast):
			tooFast++
		default:
			t.Fatalf("unexpected Poll error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent polls = %d, want exactly 1", successes)
	}
	if tooFast != n-1 {
		t.Fatalf("back-pressured polls = %d, want %d", tooFast, n-1)
	}
}

func TestApproveAtExactExpiryInstant(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock, WithLifetime(time.Minute))

	// Grant A: claim and approve both land on the exact expiry instant.
	grantA, _ := mgr.CreateGrant()
	clock.Set(grantA.ExpiresAt)

	claimed, err := mgr.Claim(grantA.UserCode)
	if err != nil {
		t.Fatalf("Claim at expiry instant: %v", err)
	}
	if claimed.State != StateClaimed {
		t.Fatalf("Claim state = %v", claimed.State)
	}
	approved, err := mgr.Approve(grantA.UserCode)
	if err != nil {
		t.Fatalf("Approve at expiry instant: %v", err)
	}
	if approved.State != StateApproved {
		t.Fatalf("Approve state = %v, want approved (explicit intent at last instant wins)",
			approved.State)
	}

	// One tick past the instant, the approval is still the only terminal state.
	clock.Advance(time.Nanosecond)
	view, err := mgr.Poll(grantA.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after expiry instant: %v", err)
	}
	if view.State != StateApproved {
		t.Fatalf("Poll state = %v, want approved, not expired", view.State)
	}

	// Grant B: untouched at the expiry instant expires one tick later,
	// and a same-instant approval can no longer override expiry.
	clock.Set(start)
	grantB, _ := mgr.CreateGrant()
	clock.Set(grantB.ExpiresAt.Add(time.Nanosecond))
	if _, err := mgr.Approve(grantB.UserCode); !errors.Is(err, ErrGrantExpired) {
		t.Fatalf("late Approve err = %v, want ErrGrantExpired", err)
	}
	view, err = mgr.Poll(grantB.DeviceCode)
	if err != nil {
		t.Fatalf("Poll expired grant: %v", err)
	}
	if view.State != StateExpired {
		t.Fatalf("Poll state = %v, want expired", view.State)
	}
	if _, err := mgr.Approve(grantB.UserCode); !errors.Is(err, ErrTerminal) {
		t.Fatalf("Approve after expiry err = %v, want ErrTerminal", err)
	}
}

func TestSweepExpired(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()

	clock.Set(grant.ExpiresAt)
	if n := mgr.SweepExpired(); n != 0 {
		t.Fatalf("SweepExpired at exact expiry = %d, want 0 (inclusive boundary)", n)
	}

	clock.Advance(time.Nanosecond)
	if n := mgr.SweepExpired(); n != 1 {
		t.Fatalf("SweepExpired = %d, want 1", n)
	}
	if n := mgr.SweepExpired(); n != 0 {
		t.Fatalf("second SweepExpired = %d, want 0 (already terminal)", n)
	}

	view, err := mgr.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after sweep: %v", err)
	}
	if view.State != StateExpired {
		t.Fatalf("Poll state = %v, want expired", view.State)
	}
}

func TestUserCodeCollisionRetries(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock,
		WithCodeAttempts(3),
		WithCodeGenerators(
			scriptedGen("D1", "D1", "D2", "D3"),
			scriptedGen("U1", "U1", "U2", "U3"),
		),
	)

	first, err := mgr.CreateGrant()
	if err != nil {
		t.Fatalf("first CreateGrant: %v", err)
	}
	if first.DeviceCode != "D1" || first.UserCode != "U1" {
		t.Fatalf("first grant codes = %s/%s, want D1/U1", first.DeviceCode, first.UserCode)
	}

	// Both generators collide once and must retry rather than overwrite.
	second, err := mgr.CreateGrant()
	if err != nil {
		t.Fatalf("second CreateGrant: %v", err)
	}
	if second.DeviceCode != "D2" || second.UserCode != "U2" {
		t.Fatalf("second grant codes = %s/%s, want D2/U2", second.DeviceCode, second.UserCode)
	}

	if mgr.byDevice["D1"] == mgr.byDevice["D2"] {
		t.Fatal("colliding device code overwrote the original grant")
	}
	if mgr.byUserCode["U1"] == mgr.byUserCode["U2"] {
		t.Fatal("colliding user code overwrote the original grant")
	}
}

func TestCodeSpaceExhausted(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	alwaysSame := func() (string, error) { return "SAME", nil }
	mgr := newTestManager(t, clock,
		WithCodeAttempts(2),
		WithCodeGenerators(alwaysSame, alwaysSame),
	)

	if _, err := mgr.CreateGrant(); err != nil {
		t.Fatalf("first CreateGrant: %v", err)
	}
	if _, err := mgr.CreateGrant(); !errors.Is(err, ErrCodeSpaceExhausted) {
		t.Fatalf("second CreateGrant err = %v, want ErrCodeSpaceExhausted", err)
	}
}

func TestDeviceAndUserCodeNamespacesIndependent(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock,
		WithCodeAttempts(2),
		WithCodeGenerators(scriptedGen("X1"), scriptedGen("X1")),
	)

	grant, err := mgr.CreateGrant()
	if err != nil {
		t.Fatalf("CreateGrant with identical code strings: %v", err)
	}
	if grant.DeviceCode != "X1" || grant.UserCode != "X1" {
		t.Fatalf("codes = %s/%s, want X1/X1 in separate namespaces",
			grant.DeviceCode, grant.UserCode)
	}

	if _, err := mgr.Poll("X1"); err != nil {
		t.Fatalf("Poll by device code X1: %v", err)
	}
	if _, err := mgr.Claim("X1"); err != nil {
		t.Fatalf("Claim by user code X1: %v", err)
	}
}

func TestDuplicateClaim(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()

	first, err := mgr.Claim(grant.UserCode)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if first.State != StateClaimed {
		t.Fatalf("first Claim state = %v, want claimed", first.State)
	}

	// Only the first presentation of a short code may claim the grant.
	if _, err := mgr.Claim(grant.UserCode); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second Claim err = %v, want ErrAlreadyClaimed", err)
	}

	// Concurrent duplicate presentations: exactly one may succeed.
	other, _ := mgr.CreateGrant()
	const n = 25
	var wg sync.WaitGroup
	gate := make(chan struct{})
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-gate
			_, err := mgr.Claim(other.UserCode)
			errs <- err
		}()
	}
	close(gate)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyClaimed):
		default:
			t.Fatalf("unexpected Claim error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims = %d, want exactly 1", successes)
	}
}

func TestUnknownCodes(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock)

	if _, err := mgr.Poll("nope"); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("Poll unknown err = %v, want ErrUnknownGrant", err)
	}
	if _, err := mgr.Claim("nope"); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("Claim unknown err = %v, want ErrUnknownGrant", err)
	}
	if _, err := mgr.Approve("nope"); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("Approve unknown err = %v, want ErrUnknownGrant", err)
	}
	if _, err := mgr.Deny("nope"); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("Deny unknown err = %v, want ErrUnknownGrant", err)
	}
}
