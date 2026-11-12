package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestGrantStateStringAndTerminal(t *testing.T) {
	cases := []struct {
		state GrantState
		name  string
		final bool
	}{
		{StatePending, "pending", false},
		{StateClaimed, "claimed", false},
		{StateApproved, "approved", true},
		{StateDenied, "denied", true},
		{StateExpired, "expired", true},
		{GrantState(99), "unknown", false},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.name {
			t.Errorf("state %d String() = %q, want %q", tc.state, got, tc.name)
		}
		if got := tc.state.IsTerminal(); got != tc.final {
			t.Errorf("state %s IsTerminal() = %v, want %v", tc.name, got, tc.final)
		}
	}
}

func TestSystemClockAndFakeClock(t *testing.T) {
	before := SystemClock{}.Now()
	if time.Since(before) < 0 {
		t.Fatal("SystemClock.Now returned a future time")
	}

	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	if !clock.Now().Equal(start) {
		t.Fatalf("FakeClock.Now = %v, want %v", clock.Now(), start)
	}
	moved := clock.Advance(3 * time.Second)
	if !moved.Equal(start.Add(3*time.Second)) || !clock.Now().Equal(moved) {
		t.Fatalf("Advance moved clock to %v", moved)
	}
	clock.Set(start)
	if !clock.Now().Equal(start) {
		t.Fatalf("Set moved clock to %v, want %v", clock.Now(), start)
	}
}

func TestClaimAfterExpiry(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()
	clock.Set(grant.ExpiresAt.Add(time.Nanosecond))

	view, err := mgr.Claim(grant.UserCode)
	if !errors.Is(err, ErrGrantExpired) {
		t.Fatalf("Claim after expiry err = %v, want ErrGrantExpired", err)
	}
	if view.State != StateExpired {
		t.Fatalf("Claim view state = %v, want expired", view.State)
	}
	if _, err := mgr.Claim(grant.UserCode); !errors.Is(err, ErrTerminal) {
		t.Fatalf("Claim expired grant again err = %v, want ErrTerminal", err)
	}
}

func TestDecideAfterTerminal(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()
	if _, err := mgr.Deny(grant.UserCode); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if _, err := mgr.Deny(grant.UserCode); !errors.Is(err, ErrTerminal) {
		t.Fatalf("second Deny err = %v, want ErrTerminal", err)
	}
}

func TestGeneratorErrorPropagates(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	boom := errors.New("entropy source down")
	failing := func() (string, error) { return "", boom }
	mgr := newTestManager(t, clock, WithCodeGenerators(failing, scriptedGen("U1")))

	if _, err := mgr.CreateGrant(); !errors.Is(err, boom) {
		t.Fatalf("CreateGrant err = %v, want generator error", err)
	}

	// No partial grant may be indexed when a generator fails.
	if len(mgr.byDevice) != 0 || len(mgr.byUserCode) != 0 {
		t.Fatal("failed CreateGrant left partial state behind")
	}

	mgr2 := newTestManager(t, clock, WithCodeGenerators(scriptedGen("D1"), failing))
	if _, err := mgr2.CreateGrant(); !errors.Is(err, boom) {
		t.Fatalf("CreateGrant user-gen err = %v, want generator error", err)
	}
}

func TestDefaultRandomGenerators(t *testing.T) {
	user, err := randomUserCode()
	if err != nil {
		t.Fatalf("randomUserCode: %v", err)
	}
	if len(user) != userCodeLength {
		t.Fatalf("user code length = %d, want %d", len(user), userCodeLength)
	}
	device, err := randomDeviceCode()
	if err != nil {
		t.Fatalf("randomDeviceCode: %v", err)
	}
	if len(device) != deviceCodeLength {
		t.Fatalf("device code length = %d, want %d", len(device), deviceCodeLength)
	}

	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		code, err := randomDeviceCode()
		if err != nil {
			t.Fatalf("randomDeviceCode %d: %v", i, err)
		}
		if seen[code] {
			t.Fatalf("duplicate random device code %q", code)
		}
		seen[code] = true
	}
}

func TestPollLazilyExpiresGrant(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()
	clock.Set(grant.ExpiresAt.Add(time.Nanosecond))

	view, err := mgr.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after lifetime: %v", err)
	}
	if view.State != StateExpired {
		t.Fatalf("Poll state = %v, want expired", view.State)
	}
	if !view.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("ExpiresAt = %v, want unchanged %v", view.ExpiresAt, grant.ExpiresAt)
	}

	// Repeated polls keep returning the single expired terminal state.
	again, err := mgr.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	if again.State != StateExpired {
		t.Fatalf("second Poll state = %v, want expired", again.State)
	}
}

func TestPollBackoffChainsMultipleSlowDowns(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	mgr := newTestManager(t, clock)

	grant, _ := mgr.CreateGrant()
	stored := mgr.byDevice[grant.DeviceCode]

	if _, err := mgr.Poll(grant.DeviceCode); err != nil {
		t.Fatalf("first Poll: %v", err)
	}

	stored.mu.Lock()
	firstDeadline := stored.nextPollAt
	stored.mu.Unlock()

	// Three ignored backoffs stack: each adds the base interval.
	for i := 0; i < 3; i++ {
		if _, err := mgr.Poll(grant.DeviceCode); !errors.Is(err, ErrPollingTooFast) {
			t.Fatalf("Poll %d err = %v, want ErrPollingTooFast", i, err)
		}
	}

	stored.mu.Lock()
	chained := stored.nextPollAt
	stored.mu.Unlock()
	want := firstDeadline.Add(3 * 5 * time.Second)
	if !chained.Equal(want) {
		t.Fatalf("chained NextPollAt = %v, want %v", chained, want)
	}

	// Moving to just before the deadline is still too early.
	clock.Set(chained.Add(-time.Nanosecond))
	if _, err := mgr.Poll(grant.DeviceCode); !errors.Is(err, ErrPollingTooFast) {
		t.Fatalf("Poll one ns early err = %v, want ErrPollingTooFast", err)
	}

	// That early poll itself adds another interval to the deadline.
	stored.mu.Lock()
	finalDeadline := stored.nextPollAt
	stored.mu.Unlock()
	clock.Set(finalDeadline)
	if _, err := mgr.Poll(grant.DeviceCode); err != nil {
		t.Fatalf("Poll at deadline: %v", err)
	}
}
